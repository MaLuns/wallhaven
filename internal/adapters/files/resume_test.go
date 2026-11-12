package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func resumeOptions(t *testing.T) Options {
	t.Helper()
	return Options{StateDir: filepath.Join(t.TempDir(), "state")}
}

func seedPartial(t *testing.T, d *Downloader, wallpaper domain.Wallpaper, directory, key string, partial []byte, total int64, etag, modified string) checkpoint {
	t.Helper()
	state := checkpoint{Version: 1, Key: key, ID: wallpaper.ID, URL: wallpaper.Path, Directory: directory, ETag: etag, LastModified: modified, Total: total}
	path, err := d.checkpointPath(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partialPath(state), partial, 0600); err != nil {
		t.Fatal(err)
	}
	return state
}

func savedBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertSavedOriginal(t *testing.T, path string, body []byte) {
	t.Helper()
	if !bytes.Equal(savedBytes(t, path), body) {
		t.Fatalf("original bytes changed: %s", path)
	}
}

func TestResumablePauseImmediateResumeNewInstanceAndCompletionReceipt(t *testing.T) {
	body := picture(t, "jpeg", 120, 90)
	cut := len(body) / 2
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("ETag", `"version-one"`)
		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body[:cut])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if r.Header.Get("Range") != fmt.Sprintf("bytes=%d-", cut) || r.Header.Get("If-Range") != `"version-one"` {
			t.Errorf("resume headers: range=%q if-range=%q", r.Header.Get("Range"), r.Header.Get("If-Range"))
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", cut, len(body)-1, len(body)))
		w.Header().Set("Content-Length", strconv.Itoa(len(body)-cut))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[cut:])
	}))
	defer server.Close()
	options := resumeOptions(t)
	directory := t.TempDir()
	w := domain.Wallpaper{ID: "abc123", Path: server.URL}
	key := "download-abc123"
	d := New(options)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		path string
		err  error
	}
	resumed := make(chan result, 1)
	var once sync.Once
	_, err := d.DownloadResumable(ctx, w, directory, key, func(received, _ int64) {
		if received > 0 {
			once.Do(func() {
				cancel()
				// Starting immediately on another instance must wait for the
				// canceled writer, including its final Sync/Close, to leave.
				go func() {
					path, err := New(options).DownloadResumable(context.Background(), w, directory, key, nil)
					resumed <- result{path, err}
				}()
			})
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pause did not cancel the old call: %v", err)
	}
	var completed result
	select {
	case completed = <-resumed:
	case <-time.After(3 * time.Second):
		t.Fatal("immediate resume did not finish")
	}
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	assertSavedOriginal(t, completed.path, body)
	statePath, _ := d.checkpointPath(key)
	state, err := d.readCheckpoint(context.Background(), statePath, key)
	if err != nil || state == nil || !state.Complete || state.Published != filepath.Base(completed.path) {
		t.Fatalf("missing completion receipt: %+v %v", state, err)
	}
	assertNoTemporaryFiles(t, directory)
	// Simulate the downloader finishing just before UI state was persisted.
	// A restarted task must reuse its validated receipt with no third GET.
	again, err := New(options).DownloadResumable(context.Background(), w, directory, key, nil)
	if err != nil || again != completed.path || requests.Load() != 2 {
		t.Fatalf("receipt did not prevent another download: path=%s requests=%d err=%v", again, requests.Load(), err)
	}
	if err := d.Discard(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	assertSavedOriginal(t, completed.path, body)
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discard retained the completion sidecar")
	}
}

func TestResumableNetworkDisconnectRetainsActualOffset(t *testing.T) {
	body := picture(t, "png", 120, 90)
	cut := len(body) / 2
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"same"`)
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body[:cut])
			return // net/http reports unexpected EOF after this short body.
		}
		if r.Header.Get("Range") != fmt.Sprintf("bytes=%d-", cut) {
			t.Errorf("resumed from UI metadata instead of file size: %s", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", cut, len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[cut:])
	}))
	defer server.Close()
	options := resumeOptions(t)
	d := New(options)
	w := domain.Wallpaper{ID: "abc123", Path: server.URL}
	directory, key := t.TempDir(), "download-abc123"
	if _, err := d.DownloadResumable(context.Background(), w, directory, key, nil); err == nil {
		t.Fatal("short network response claimed success")
	}
	statePath, _ := d.checkpointPath(key)
	state, err := d.readCheckpoint(context.Background(), statePath, key)
	if err != nil || state == nil {
		t.Fatalf("network failure lost checkpoint: %v", err)
	}
	assertSavedOriginal(t, partialPath(*state), body[:cut])
	path, err := New(options).DownloadResumable(context.Background(), w, directory, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSavedOriginal(t, path, body)
}

func TestResumableChangedValidatorAndIgnoredRangeRestartSafely(t *testing.T) {
	old := picture(t, "png", 80, 60)
	fresh := picture(t, "jpeg", 100, 80)
	cut := len(old) / 3
	for _, mode := range []string{"ignore-range", "changed-etag-206", "no-validator"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				w.Header().Set("ETag", `"new"`)
				if mode == "no-validator" && r.Header.Get("Range") != "" {
					t.Error("appended without a representation validator")
				}
				if mode == "changed-etag-206" && n == 1 {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", cut, len(fresh)-1, len(fresh)))
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write(fresh[cut:])
					return
				}
				if mode == "changed-etag-206" && r.Header.Get("Range") != "" {
					t.Error("validator mismatch did not issue a fresh request")
				}
				_, _ = w.Write(fresh)
			}))
			defer server.Close()
			d := New(resumeOptions(t))
			w := domain.Wallpaper{ID: "abc123", Path: server.URL}
			directory, key := t.TempDir(), "download-abc123"
			etag := `"old"`
			if mode == "no-validator" {
				etag = ""
			}
			seedPartial(t, d, w, directory, key, old[:cut], int64(len(old)), etag, "")
			path, err := d.DownloadResumable(context.Background(), w, directory, key, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertSavedOriginal(t, path, fresh)
			wantRequests := int32(1)
			if mode == "changed-etag-206" {
				wantRequests = 2
			}
			if requests.Load() != wantRequests {
				t.Fatalf("requests=%d want%d", requests.Load(), wantRequests)
			}
		})
	}
}

func TestResumableUsesLastModifiedIfRange(t *testing.T) {
	body := picture(t, "png", 40, 30)
	cut := len(body) / 2
	modified := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Format(http.TimeFormat)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Range") != modified {
			t.Errorf("If-Range=%q", r.Header.Get("If-Range"))
		}
		w.Header().Set("Last-Modified", modified)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", cut, len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[cut:])
	}))
	defer server.Close()
	d := New(resumeOptions(t))
	directory, key := t.TempDir(), "download-abc123"
	w := domain.Wallpaper{ID: "abc123", Path: server.URL}
	seedPartial(t, d, w, directory, key, body[:cut], int64(len(body)), "", modified)
	path, err := d.DownloadResumable(context.Background(), w, directory, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSavedOriginal(t, path, body)
}

func TestResumableRejectsInvalidContentRangeWithoutAppending(t *testing.T) {
	body := picture(t, "png", 40, 30)
	cut := len(body) / 2
	for _, header := range []string{"", "items 1-2/3", "bytes 0-1/2", "bytes 10-1/20", "bytes 1-20/20", "bytes 1-2/*", fmt.Sprintf("bytes %d-%d/%d", cut, len(body)-2, len(body))} {
		t.Run(header, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"same"`)
				w.Header().Set("Content-Range", header)
				w.Header().Set("Content-Length", strconv.Itoa(len(body)-cut))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(body[cut:])
			}))
			defer server.Close()
			d := New(resumeOptions(t))
			directory, key := t.TempDir(), "download-abc123"
			wallpaper := domain.Wallpaper{ID: "abc123", Path: server.URL}
			state := seedPartial(t, d, wallpaper, directory, key, body[:cut], int64(len(body)), `"same"`, "")
			if _, err := d.DownloadResumable(context.Background(), wallpaper, directory, key, nil); err == nil {
				t.Fatal("invalid range claimed success")
			}
			assertSavedOriginal(t, partialPath(state), body[:cut])
			if err := d.Discard(context.Background(), key); err != nil {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(directory)
			if len(entries) != 0 {
				t.Fatal("discard left unfinished data")
			}
		})
	}
}

func TestResumable416ValidatesCompletePartialAndKeepsUnrelatedFile(t *testing.T) {
	body := picture(t, "png", 40, 30)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != fmt.Sprintf("bytes=%d-", len(body)) {
			t.Errorf("range=%q", r.Header.Get("Range"))
		}
		w.Header().Set("ETag", `"same"`)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(body)))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer server.Close()
	d := New(resumeOptions(t))
	directory, key := t.TempDir(), "download-abc123"
	w := domain.Wallpaper{ID: "abc123", Path: server.URL}
	seedPartial(t, d, w, directory, key, body, int64(len(body)), `"same"`, "")
	unrelated := filepath.Join(directory, "wallhaven-abc123.png")
	if err := os.WriteFile(unrelated, []byte("user original"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := d.DownloadResumable(context.Background(), w, directory, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path == unrelated {
		t.Fatal("published over an unrelated file")
	}
	assertSavedOriginal(t, path, body)
	if string(savedBytes(t, unrelated)) != "user original" {
		t.Fatal("unrelated file changed")
	}
	if err := d.Discard(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	assertSavedOriginal(t, path, body)
}

func TestResumableTimeoutAndExplicitDiscard(t *testing.T) {
	body := picture(t, "png", 40, 30)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"same"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body[:len(body)/2])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	options := resumeOptions(t)
	options.Timeout = 50 * time.Millisecond
	d := New(options)
	directory, key := t.TempDir(), "download-abc123"
	w := domain.Wallpaper{ID: "abc123", Path: server.URL}
	if _, err := d.DownloadResumable(context.Background(), w, directory, key, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout=%v", err)
	}
	statePath, _ := d.checkpointPath(key)
	state, err := d.readCheckpoint(context.Background(), statePath, key)
	if err != nil || state == nil {
		t.Fatalf("timeout lost checkpoint: %v", err)
	}
	assertSavedOriginal(t, partialPath(*state), body[:len(body)/2])
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Discard(canceled, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discard=%v", err)
	}
	assertSavedOriginal(t, partialPath(*state), body[:len(body)/2])
	if err := d.Discard(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partialPath(*state)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discard left partial")
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discard left checkpoint")
	}
}

func TestResumableCorruptCheckpointAndSymlinksCannotDeleteUserFiles(t *testing.T) {
	for _, mode := range []string{"truncated", "wrong-key", "path-traversal", "unknown-partial", "symlink-sidecar", "symlink-partial"} {
		t.Run(mode, func(t *testing.T) {
			d := New(resumeOptions(t))
			directory, key := t.TempDir(), "download-abc123"
			wallpaper := domain.Wallpaper{ID: "abc123", Path: "https://example.test/original.png"}
			state := seedPartial(t, d, wallpaper, directory, key, []byte("own partial"), 20, `"same"`, "")
			statePath, _ := d.checkpointPath(key)
			userFile := filepath.Join(t.TempDir(), "user.png")
			if err := os.WriteFile(userFile, []byte("unrelated"), 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "truncated":
				_ = os.WriteFile(statePath, []byte(`{"version":1`), 0600)
			case "wrong-key":
				state.Key = "wallpaper-other"
				data, _ := json.Marshal(state)
				_ = os.WriteFile(statePath, data, 0600)
			case "path-traversal":
				state.Published = "../user.png"
				state.Size, state.Total = 20, 20
				state.Digest = strings.Repeat("0", 64)
				data, _ := json.Marshal(state)
				_ = os.WriteFile(statePath, data, 0600)
			case "unknown-partial":
				data := bytes.TrimSuffix(savedBytes(t, statePath), []byte("}"))
				data = append(data, []byte(`,"partial":"`+userFile+`"}`)...)
				_ = os.WriteFile(statePath, data, 0600)
			case "symlink-sidecar":
				_ = os.Remove(statePath)
				if err := os.Symlink(userFile, statePath); err != nil {
					t.Skip(err)
				}
			case "symlink-partial":
				_ = os.Remove(partialPath(state))
				if err := os.Symlink(userFile, partialPath(state)); err != nil {
					t.Skip(err)
				}
			}
			if err := d.Discard(context.Background(), key); err == nil {
				t.Fatal("unsafe checkpoint was accepted for discard")
			}
			if _, err := d.DownloadResumable(context.Background(), wallpaper, directory, key, nil); err == nil {
				t.Fatal("unsafe checkpoint was accepted for download")
			}
			if string(savedBytes(t, userFile)) != "unrelated" {
				t.Fatal("unrelated user file was changed")
			}
		})
	}
}

func TestResumableConcurrentSameKeyAndChangedCompletionDirectory(t *testing.T) {
	body := picture(t, "png", 40, 30)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write(body) }))
	defer server.Close()
	options := resumeOptions(t)
	options.Originals = NewOriginalCache(1 << 20)
	directory, key := t.TempDir(), "download-abc123"
	wallpaper := domain.Wallpaper{ID: "abc123", Path: server.URL}
	var workers sync.WaitGroup
	paths := make(chan string, 8)
	for range 8 {
		workers.Go(func() {
			path, err := New(options).DownloadResumable(context.Background(), wallpaper, directory, key, nil)
			if err != nil {
				t.Error(err)
			}
			paths <- path
		})
	}
	workers.Wait()
	close(paths)
	first := ""
	for path := range paths {
		if first == "" {
			first = path
		}
		if path != first {
			t.Fatalf("same-key requests published different originals: %s/%s", first, path)
		}
	}
	if first == "" || requests.Load() != 1 {
		t.Fatalf("same-key requests=%d path=%q", requests.Load(), first)
	}
	assertSavedOriginal(t, first, body)
	newDirectory := t.TempDir()
	second, err := New(options).DownloadResumable(context.Background(), wallpaper, newDirectory, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(second) != newDirectory || requests.Load() != 1 {
		t.Fatalf("new directory did not reuse encoded original: %s requests=%d", second, requests.Load())
	}
	assertSavedOriginal(t, first, body)
	assertSavedOriginal(t, second, body)
	if err := New(options).Discard(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	assertSavedOriginal(t, first, body)
	assertSavedOriginal(t, second, body)
}

func TestEncodedOriginalCacheSavesWithoutHTTPInBothDownloadAPIs(t *testing.T) {
	body := picture(t, "jpeg", 40, 30)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	for _, resumable := range []bool{false, true} {
		t.Run(fmt.Sprint(resumable), func(t *testing.T) {
			cache := NewOriginalCache(1 << 20)
			cache.Put(server.URL, body)
			options := resumeOptions(t)
			options.Originals = cache
			d := New(options)
			wallpaper := domain.Wallpaper{ID: "abc123", Path: server.URL}
			var path string
			var err error
			if resumable {
				path, err = d.DownloadResumable(context.Background(), wallpaper, t.TempDir(), "download-abc123", nil)
			} else {
				path, err = d.Download(context.Background(), wallpaper, t.TempDir(), nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			assertSavedOriginal(t, path, body)
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("encoded cache made %d requests", requests.Load())
	}
}
