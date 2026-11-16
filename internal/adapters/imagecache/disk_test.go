package imagecache

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func diskOptions(directory string) Options {
	return Options{Directory: directory, MaxBytes: 1 << 20, MaxEntryBytes: 1 << 16, MaxEntries: 64}
}

func requireHit(t *testing.T, cache *Disk, key string, expected []byte) {
	t.Helper()
	got, found := cache.Get(key)
	if !found || !bytes.Equal(got, expected) {
		t.Fatalf("cache hit found=%v bytes=%q, want %q", found, got, expected)
	}
}

func requireMiss(t *testing.T, cache *Disk, key string) {
	t.Helper()
	if data, found := cache.Get(key); found || data != nil {
		t.Fatalf("unexpected cache hit: found=%v bytes=%q", found, data)
	}
}

func TestDiskLazyInitializationRestartAndPrivateFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "images")
	options := diskOptions(directory)
	cache := New(options)
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("constructing a cache performed disk I/O: %v", err)
	}
	key := "https://example.com/image.png?private=secret"
	input := []byte("encoded image snapshot")
	expected := bytes.Clone(input)
	cache.Put(key, input)
	input[0] = 'X'
	requireHit(t, New(options), key, expected)
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 1 || !isCacheName(files[0].Name()) || strings.Contains(files[0].Name(), "secret") {
		t.Fatalf("unexpected cache files: %v, %v", files, err)
	}
	if runtime.GOOS != "windows" {
		fileInfo, fileErr := files[0].Info()
		dirInfo, dirErr := os.Stat(directory)
		if fileErr != nil || dirErr != nil || fileInfo.Mode().Perm() != 0600 || dirInfo.Mode().Perm() != 0700 {
			t.Fatalf("cache permissions: file=%v/%v directory=%v/%v", fileInfo, fileErr, dirInfo, dirErr)
		}
	}
	cache.Delete(key)
	requireMiss(t, New(options), key)
}

func TestDiskLRUReadSurvivesRestartAndBoundsBytesAndCount(t *testing.T) {
	for _, bound := range []string{"bytes", "count"} {
		t.Run(bound, func(t *testing.T) {
			directory := t.TempDir()
			options := diskOptions(directory)
			payload := []byte("12345678")
			if bound == "bytes" {
				options.MaxBytes = 2 * int64(headerSize+len(payload))
			} else {
				options.MaxEntries = 2
			}
			cache := New(options)
			cache.Put("first", payload)
			cache.Put("second", payload)
			// Deterministic old ordering, without sleeps or filesystem timestamp
			// resolution assumptions. A real Get must persist its new recency.
			for index, key := range []string{"first", "second"} {
				stamp := time.Now().Add(time.Duration(index-3) * time.Hour)
				if err := os.Chtimes(filepath.Join(directory, cacheName(key)), stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			requireHit(t, New(options), "first", payload)
			restarted := New(options)
			restarted.Put("third", payload)
			requireHit(t, restarted, "first", payload)
			requireMiss(t, restarted, "second")
			requireHit(t, restarted, "third", payload)
			var used int64
			count := 0
			files, err := os.ReadDir(directory)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range files {
				if isCacheName(file.Name()) {
					info, err := file.Info()
					if err != nil {
						t.Fatal(err)
					}
					used += info.Size()
					count++
				}
			}
			if used > options.MaxBytes || count > options.MaxEntries {
				t.Fatalf("disk bounds exceeded: bytes=%d count=%d", used, count)
			}
		})
	}
}

func TestDiskCorruptTruncatedAndOversizedEntriesMissAndCanBeReplaced(t *testing.T) {
	mutations := map[string]func([]byte) []byte{
		"checksum": func(data []byte) []byte { data[len(data)-1] ^= 0xff; return data },
		"magic":    func(data []byte) []byte { data[0] ^= 0xff; return data },
		"header":   func(data []byte) []byte { return data[:5] },
		"payload":  func(data []byte) []byte { return data[:len(data)-2] },
		"extra":    func(data []byte) []byte { return append(data, 0) },
		"length": func(data []byte) []byte {
			binary.BigEndian.PutUint64(data[len(magic):len(magic)+8], ^uint64(0))
			return data
		},
		"oversize": func(data []byte) []byte { return append(data, make([]byte, 1<<16)...) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			options := diskOptions(t.TempDir())
			cache := New(options)
			payload := []byte("complete image")
			cache.Put("image", payload)
			path := filepath.Join(options.Directory, cacheName("image"))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, mutate(data), 0600); err != nil {
				t.Fatal(err)
			}
			restarted := New(options)
			requireMiss(t, restarted, "image")
			restarted.Put("image", payload)
			requireHit(t, New(options), "image", payload)
		})
	}
}

func TestDiskIgnoresUnrelatedFilesAndOnlyCleansStaleOwnTemporaryFiles(t *testing.T) {
	directory := t.TempDir()
	unrelated := []string{"notes.txt", cacheName("backup") + ".bak", temporaryPrefix + "external" + temporarySuffix}
	for _, name := range unrelated {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ownedDirectory := filepath.Join(directory, cacheName("directory"))
	if err := os.Mkdir(ownedDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(directory, temporaryPrefix+"12345"+temporarySuffix)
	fresh := filepath.Join(directory, temporaryPrefix+"67890"+temporarySuffix)
	for _, path := range []string{stale, fresh} {
		if err := os.WriteFile(path, []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	options := diskOptions(directory)
	options.MaxEntries = 1
	cache := New(options)
	cache.Put("one", []byte("one"))
	cache.Put("two", []byte("two"))
	for _, name := range unrelated {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(data) != "keep" {
			t.Fatalf("unrelated file was modified: %q, %v", name, err)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale cache temporary file remains: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh temporary file was removed: %v", err)
	}
	if info, err := os.Stat(ownedDirectory); err != nil || !info.IsDir() {
		t.Fatalf("non-cache directory was removed: %v", err)
	}
}

func TestDiskDisabledOrUnwritableDirectoryAndOversizedWrites(t *testing.T) {
	for _, options := range []Options{{}, diskOptions(filepath.Join(t.TempDir(), "not-a-directory", "child"))} {
		if options.Directory != "" {
			if err := os.WriteFile(filepath.Dir(options.Directory), []byte("file"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		cache := New(options)
		cache.Put("image", []byte("data"))
		requireMiss(t, cache, "image")
		cache.Delete("image")
	}
	if runtime.GOOS != "windows" {
		directory := t.TempDir()
		if err := os.Chmod(directory, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(directory, 0700) })
		cache := New(diskOptions(directory))
		cache.Put("image", []byte("data"))
		requireMiss(t, cache, "image")
		files, err := os.ReadDir(directory)
		if err != nil || len(files) != 0 {
			t.Fatalf("unwritable directory was changed: %v, %v", files, err)
		}
	}
	options := diskOptions(t.TempDir())
	options.MaxEntryBytes = 4
	cache := New(options)
	cache.Put("image", []byte("valid"))
	requireMiss(t, cache, "image")
	cache.Put("image", []byte("good"))
	requireHit(t, cache, "image", []byte("good"))
	cache.Put("image", []byte("oversized replacement"))
	requireHit(t, cache, "image", []byte("good"))
}

func TestDiskConcurrentInstancesPublishOnlyCompletePayloads(t *testing.T) {
	options := diskOptions(t.TempDir())
	options.MaxBytes, options.MaxEntries = 8<<20, 256
	caches := []*Disk{New(options), New(options)}
	for _, cache := range caches {
		requireMiss(t, cache, "initialization")
	}
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			cache := caches[worker%len(caches)]
			other := caches[(worker+1)%len(caches)]
			for index := range 24 {
				payload := bytes.Repeat([]byte{byte(worker + 1)}, 1024+worker)
				cache.Put("shared", payload)
				if data, found := other.Get("shared"); found {
					if len(data) < 1024 || len(data) > 1031 || !bytes.Equal(data, bytes.Repeat(data[:1], len(data))) {
						t.Errorf("concurrent writer published partial payload: length=%d", len(data))
					}
				}
				key := fmt.Sprintf("worker-%d-%d", worker, index)
				cache.Put(key, payload)
				if data, found := other.Get(key); found && !bytes.Equal(data, payload) {
					t.Error("concurrent read returned a different key's bytes")
				}
			}
		})
	}
	workers.Wait()
	final := []byte("final complete image")
	caches[0].Put("shared", final)
	requireHit(t, New(options), "shared", final)
}

func TestDiskStatsIncludesHeadersAndSeesOtherInstances(t *testing.T) {
	options := diskOptions(t.TempDir())
	cache := New(options)
	cache.Put("one", []byte("one"))
	other := New(options)
	other.Put("two", []byte("second"))
	want := Usage{Bytes: int64(2*headerSize + len("one") + len("second")), Entries: 2, Limit: options.MaxBytes}
	usage, err := cache.Stats()
	if err != nil || usage != want {
		t.Fatalf("usage = %+v, %v; want %+v", usage, err, want)
	}
	other.Delete("one")
	usage, err = cache.Stats()
	want.Bytes, want.Entries = int64(headerSize+len("second")), 1
	if err != nil || usage != want {
		t.Fatalf("external deletion was not reflected: %+v, %v; want %+v", usage, err, want)
	}
}

func TestDiskSetLimitShrinksLRUImmediatelyAndRejectsInvalidLimits(t *testing.T) {
	options := diskOptions(t.TempDir())
	cache := New(options)
	payload := []byte("image")
	cache.Put("one", payload)
	cache.Put("two", payload)
	for index, key := range []string{"one", "two"} {
		stamp := time.Now().Add(time.Duration(index-3) * time.Hour)
		if err := os.Chtimes(filepath.Join(options.Directory, cacheName(key)), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	requireHit(t, cache, "one", payload)
	limit := int64(headerSize + len(payload))
	if err := cache.SetLimit(limit); err != nil {
		t.Fatal(err)
	}
	requireHit(t, cache, "one", payload)
	requireMiss(t, cache, "two")
	if usage, err := cache.Stats(); err != nil || usage != (Usage{Bytes: limit, Entries: 1, Limit: limit}) {
		t.Fatalf("shrunk usage = %+v, %v", usage, err)
	}
	for _, invalid := range []int64{0, -1} {
		if err := cache.SetLimit(invalid); err == nil {
			t.Fatalf("invalid limit %d accepted", invalid)
		}
	}
	if err := cache.SetLimit(1); err != nil {
		t.Fatal(err)
	}
	cache.Put("too large", payload)
	if usage, err := cache.Stats(); err != nil || usage != (Usage{Limit: 1}) {
		t.Fatalf("sub-header limit retained or admitted entries: %+v, %v", usage, err)
	}
}

func TestDiskRestoredHigherLimitAppliesBeforeStartupEviction(t *testing.T) {
	options := diskOptions(t.TempDir())
	payload := bytes.Repeat([]byte("image"), 256)
	previous := New(options)
	for _, key := range []string{"one", "two", "three"} {
		previous.Put(key, payload)
	}
	initial := options
	initial.MaxBytes = int64(headerSize + len(payload))
	restarted := New(initial)
	if restarted.Limit() != initial.MaxBytes {
		t.Fatal("configured initial limit was not retained")
	}
	if err := restarted.SetLimit(options.MaxBytes); err != nil {
		t.Fatal(err)
	}
	usage, err := restarted.Stats()
	want := Usage{Bytes: 3 * int64(headerSize+len(payload)), Entries: 3, Limit: options.MaxBytes}
	if err != nil || usage != want || restarted.Limit() != options.MaxBytes {
		t.Fatalf("saved higher limit was applied after eviction: %+v, %v; want %+v", usage, err, want)
	}
	for _, key := range []string{"one", "two", "three"} {
		requireHit(t, restarted, key, payload)
	}
}

func TestDiskLimitGetterDoesNotInitializeAndFailedSetKeepsPreviousLimit(t *testing.T) {
	var absent *Disk
	if absent.Limit() != 0 {
		t.Fatal("nil cache has a limit")
	}
	directory := filepath.Join(t.TempDir(), "not-created")
	options := diskOptions(directory)
	cache := New(options)
	if cache.Limit() != options.MaxBytes {
		t.Fatal("limit getter returned an incorrect value")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("limit getter performed disk I/O: %v", err)
	}
	disabled := New(Options{MaxBytes: 1024})
	if err := disabled.SetLimit(4096); err == nil || disabled.Limit() != 1024 {
		t.Fatalf("failed initialization changed the configured limit: %d, %v", disabled.Limit(), err)
	}
}

func TestDiskReadyGatePreventsImageReadInitialization(t *testing.T) {
	options := diskOptions(filepath.Join(t.TempDir(), "not-initialized"))
	ready := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(ready) }) }
	t.Cleanup(release)
	options.Ready = ready
	cache := New(options)
	finished := make(chan bool, 1)
	go func() {
		_, found := cache.Get("missing")
		finished <- found
	}()
	select {
	case <-finished:
		t.Fatal("image read did not wait for restored preferences")
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := os.Stat(options.Directory); !os.IsNotExist(err) {
		t.Fatalf("gated image read initialized the directory: %v", err)
	}
	if cache.Generation() != 0 || cache.Limit() != options.MaxBytes {
		t.Fatal("metadata getters changed while the image gate was closed")
	}
	release()
	select {
	case found := <-finished:
		if found {
			t.Fatal("missing image became a cache hit")
		}
	case <-time.After(time.Second):
		t.Fatal("image read did not resume after readiness")
	}
	if info, err := os.Stat(options.Directory); err != nil || !info.IsDir() {
		t.Fatalf("released image read did not initialize the cache: %v", err)
	}
}

func TestDiskReadyGateAllowsRestoredLimitBeforeReadAndDefaultPruning(t *testing.T) {
	options := diskOptions(t.TempDir())
	payload := bytes.Repeat([]byte("image"), 256)
	seed := New(options)
	for _, key := range []string{"one", "two", "three"} {
		seed.Put(key, payload)
	}
	ready := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(ready) }) }
	t.Cleanup(release)
	initial := options
	initial.MaxBytes = int64(headerSize + len(payload))
	initial.Ready = ready
	cache := New(initial)
	type readResult struct {
		data  []byte
		found bool
	}
	read := make(chan readResult, 1)
	go func() {
		data, found := cache.Get("one")
		read <- readResult{data, found}
	}()
	select {
	case <-read:
		t.Fatal("image read bypassed the closed startup gate")
	case <-time.After(30 * time.Millisecond):
	}
	configured := make(chan error, 1)
	go func() { configured <- cache.SetLimit(options.MaxBytes) }()
	select {
	case err := <-configured:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("limit restoration waited for the image readiness gate")
	}
	usage, err := cache.Stats()
	want := Usage{Bytes: 3 * int64(headerSize+len(payload)), Entries: 3, Limit: options.MaxBytes}
	if err != nil || usage != want {
		t.Fatalf("startup defaults evicted saved images: %+v, %v; want %+v", usage, err, want)
	}
	release()
	select {
	case result := <-read:
		if !result.found || !bytes.Equal(result.data, payload) {
			t.Fatal("released image read lost its persistent cache hit")
		}
	case <-time.After(time.Second):
		t.Fatal("image read remained blocked after preference restoration")
	}
}

func TestDiskReadyGateRejectsPreClearWriterAfterRelease(t *testing.T) {
	options := diskOptions(t.TempDir())
	ready := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(ready) }) }
	t.Cleanup(release)
	options.Ready = ready
	cache := New(options)
	previous := cache.Generation()
	finished := make(chan struct{})
	go func() {
		cache.PutIfCurrent("late response", []byte("must not persist"), previous)
		close(finished)
	}()
	select {
	case <-finished:
		t.Fatal("image write bypassed the startup gate")
	case <-time.After(30 * time.Millisecond):
	}
	cleared := make(chan error, 1)
	go func() { cleared <- cache.Clear() }()
	select {
	case err := <-cleared:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("clear waited for the image readiness gate")
	}
	release()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("stale image write remained blocked after readiness")
	}
	requireMiss(t, cache, "late response")
	if usage, err := cache.Stats(); err != nil || usage.Entries != 0 {
		t.Fatalf("released stale writer repopulated disk: %+v, %v", usage, err)
	}
}

func TestDiskClearStrictScopeAndGenerationPreventsLateWriters(t *testing.T) {
	options := diskOptions(t.TempDir())
	cache := New(options)
	cache.Put("one", []byte("one"))
	New(options).Put("from another instance", []byte("two"))
	unrelated := []string{"wallhaven-downloaded.jpg", cacheName("backup") + ".bak", temporaryPrefix + "external.tmp"}
	for _, name := range unrelated {
		if err := os.WriteFile(filepath.Join(options.Directory, name), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ownedNameDirectory := filepath.Join(options.Directory, cacheName("directory"))
	if err := os.Mkdir(ownedNameDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(options.Directory, temporaryPrefix+"12345.tmp")
	fresh := filepath.Join(options.Directory, temporaryPrefix+"67890.tmp")
	for _, path := range []string{stale, fresh} {
		if err := os.WriteFile(path, []byte("pending"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stamp := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	previous := cache.Generation()
	if err := cache.Clear(); err != nil {
		t.Fatal(err)
	}
	if cache.Generation() != previous+1 {
		t.Fatal("clearing did not invalidate earlier writers")
	}
	cache.PutIfCurrent("late response", []byte("discard"), previous)
	if usage, err := cache.Stats(); err != nil || usage != (Usage{Limit: options.MaxBytes}) {
		t.Fatalf("clear retained or repopulated images: %+v, %v", usage, err)
	}
	for _, name := range unrelated {
		if data, err := os.ReadFile(filepath.Join(options.Directory, name)); err != nil || string(data) != "keep" {
			t.Fatalf("clearing changed an unrelated file %q: %q, %v", name, data, err)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale own temporary file remains: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh temporary file was deleted: %v", err)
	}
	if info, err := os.Stat(ownedNameDirectory); err != nil || !info.IsDir() {
		t.Fatalf("directory was removed: %v", err)
	}
	cache.PutIfCurrent("new response", []byte("keep"), cache.Generation())
	requireHit(t, cache, "new response", []byte("keep"))
	cache.Put("ordinary put", []byte("current"))
	requireHit(t, cache, "ordinary put", []byte("current"))
}

func TestDiskManagementReportsUnavailableAndRuntimeDirectoryFailures(t *testing.T) {
	for _, cache := range []*Disk{nil, New(Options{})} {
		if _, err := cache.Stats(); err == nil {
			t.Fatal("unavailable cache reported successful statistics")
		}
		if err := cache.SetLimit(1024); err == nil {
			t.Fatal("unavailable cache reported successful limit change")
		}
		if err := cache.Clear(); err == nil {
			t.Fatal("unavailable cache reported successful clear")
		}
	}
	root := t.TempDir()
	options := diskOptions(filepath.Join(root, "cache"))
	cache := New(options)
	cache.Put("one", []byte("one"))
	parked := filepath.Join(root, "parked")
	if err := os.Rename(options.Directory, parked); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(options.Directory, []byte("unrelated replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Stats(); err == nil {
		t.Fatal("runtime directory failure was hidden by statistics")
	}
	if err := cache.SetLimit(1); err == nil {
		t.Fatal("runtime directory failure was hidden by limit change")
	}
	previous := cache.Generation()
	if err := cache.Clear(); err == nil {
		t.Fatal("runtime directory failure was hidden by clear")
	}
	if cache.Generation() != previous+1 {
		t.Fatal("failed clear did not reject earlier writers")
	}
	if data, err := os.ReadFile(options.Directory); err != nil || string(data) != "unrelated replacement" {
		t.Fatalf("clear modified the replacement file: %q, %v", data, err)
	}
	if err := os.Remove(options.Directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parked, options.Directory); err != nil {
		t.Fatal(err)
	}
	cache.PutIfCurrent("late", []byte("discard"), previous)
	requireMiss(t, cache, "late")
	if usage, err := cache.Stats(); err != nil || usage.Entries != 1 || usage.Limit != options.MaxBytes {
		t.Fatalf("management did not recover after directory repair: %+v, %v", usage, err)
	}
}

func TestDiskClearWithConcurrentLateResponsesAndGenerationDoesNotBlock(t *testing.T) {
	cache := New(diskOptions(t.TempDir()))
	previous := cache.Generation()
	cache.mu.Lock()
	generationRead := make(chan uint64, 1)
	go func() { generationRead <- cache.Generation() }()
	select {
	case generation := <-generationRead:
		if generation != previous {
			t.Fatal("unexpected generation")
		}
	case <-time.After(time.Second):
		cache.mu.Unlock()
		t.Fatal("Generation blocked on disk I/O mutex")
	}
	cache.mu.Unlock()
	release := make(chan struct{})
	var writers sync.WaitGroup
	for index := range 16 {
		writers.Go(func() {
			<-release
			cache.PutIfCurrent(fmt.Sprintf("late-%d", index), []byte("response"), previous)
		})
	}
	if err := cache.Clear(); err != nil {
		t.Fatal(err)
	}
	close(release)
	writers.Wait()
	if usage, err := cache.Stats(); err != nil || usage.Entries != 0 || usage.Bytes != 0 {
		t.Fatalf("late concurrent responses repopulated cleared cache: %+v, %v", usage, err)
	}
}

func TestDiskManagementReportsRemovalFailuresWithoutClaimingFilesWereCleared(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires ordinary Unix directory permission enforcement")
	}
	options := diskOptions(t.TempDir())
	cache := New(options)
	cache.Put("one", []byte("one"))
	cache.Put("two", []byte("two"))
	if err := os.Chmod(options.Directory, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(options.Directory, 0700) })
	if err := cache.SetLimit(1); err == nil {
		t.Fatal("failed eviction reported success")
	}
	previous := cache.Generation()
	if err := cache.Clear(); err == nil {
		t.Fatal("failed removals reported a successful clear")
	}
	usage, err := cache.Stats()
	if err != nil || usage.Entries != 2 || usage.Bytes == 0 || usage.Limit != 1 || cache.Generation() != previous+1 {
		t.Fatalf("failed removals lost accurate state: %+v, %v", usage, err)
	}
	if err := os.Chmod(options.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := cache.Clear(); err != nil {
		t.Fatal(err)
	}
	if usage, err := cache.Stats(); err != nil || usage.Entries != 0 || usage.Bytes != 0 {
		t.Fatalf("clear did not recover after permission repair: %+v, %v", usage, err)
	}
}
