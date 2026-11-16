package images

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/files"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func heldCacheImage(t *testing.T, body []byte) (*httptest.Server, <-chan struct{}, func(), *atomic.Int32) {
	t.Helper()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var requests atomic.Int32
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			_, _ = w.Write(body)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(unblock)
	return server, started, unblock, &requests
}

func awaitCacheImage(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("image request did not start")
	}
}

func TestThumbnailClearDoesNotPersistInflightResponseOrDropVisibleMemory(t *testing.T) {
	server, started, release, requests := heldCacheImage(t, picture(t, 24, 16))
	directory := t.TempDir()
	disk := thumbnailDisk(directory)
	updates, notify := notifications()
	cache := New(Options{Disk: disk, OnChange: notify})
	t.Cleanup(cache.Close)
	cache.Get(server.URL)
	awaitCacheImage(t, started)
	if err := disk.Clear(); err != nil {
		t.Fatal(err)
	}
	release()
	bitmap := waitImage(t, cache, server.URL, updates)
	requireThumbnailSize(t, bitmap, 24, 16)
	if usage, err := disk.Stats(); err != nil || usage.Entries != 0 {
		t.Fatalf("pre-clear response repopulated disk: %+v, %v", usage, err)
	}
	if hit, loading := cache.Get(server.URL); loading || hit != bitmap || requests.Load() != 1 {
		t.Fatal("clearing disk discarded the currently visible memory image")
	}
	cache.Close()
	updates, notify = notifications()
	restarted := New(Options{Disk: thumbnailDisk(directory), OnChange: notify})
	t.Cleanup(restarted.Close)
	requireThumbnailSize(t, waitImage(t, restarted, server.URL, updates), 24, 16)
	if requests.Load() != 2 {
		t.Fatal("fresh post-clear request did not fetch the removed cache entry")
	}
	if usage, err := restarted.disk.Stats(); err != nil || usage.Entries != 1 {
		t.Fatal("fresh post-clear request could not populate disk")
	}
}

func TestOriginalClearDoesNotPersistInflightPreviewOrDownload(t *testing.T) {
	for _, kind := range []string{"preview", "download"} {
		t.Run(kind, func(t *testing.T) {
			body := detailPicture(t, "png", 24, 16)
			server, started, release, requests := heldCacheImage(t, body)
			directory := t.TempDir()
			disk := originalDisk(t, directory)
			cache := files.NewOriginalCacheWithDisk(1<<20, disk)
			finished := make(chan error, 1)
			var saved string
			if kind == "preview" {
				loader := NewDetailLoader(DetailOptions{Originals: cache})
				go func() { _, err := loader.Load(context.Background(), server.URL, nil); finished <- err }()
			} else {
				downloader := files.New(files.Options{Originals: cache, StateDir: t.TempDir()})
				target := t.TempDir()
				go func() {
					var err error
					saved, err = downloader.DownloadResumable(context.Background(), domain.Wallpaper{ID: "abc123", Path: server.URL}, target, "download:abc123", nil)
					finished <- err
				}()
			}
			awaitCacheImage(t, started)
			if err := disk.Clear(); err != nil {
				t.Fatal(err)
			}
			release()
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("image operation did not finish after cache clear")
			}
			if usage, err := disk.Stats(); err != nil || usage.Entries != 0 {
				t.Fatalf("pre-clear %s persisted after clear: %+v, %v", kind, usage, err)
			}
			if data, found := cache.Get(server.URL); !found || !bytes.Equal(data, body) {
				t.Fatal("disk-only clear broke the completed image's in-memory result")
			}
			if saved != "" {
				if data, err := os.ReadFile(saved); err != nil || !bytes.Equal(data, body) {
					t.Fatal("cache clear affected the downloaded wallpaper")
				}
			}
			fresh := NewDetailLoader(DetailOptions{Originals: files.NewOriginalCacheWithDisk(1<<20, originalDisk(t, directory))})
			if _, err := fresh.Load(context.Background(), server.URL, nil); err != nil || requests.Load() != 2 {
				t.Fatalf("post-clear original request did not refill from source: %v", err)
			}
		})
	}
}
