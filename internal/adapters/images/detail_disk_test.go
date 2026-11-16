package images

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/files"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/imagecache"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type originalDiskTransport func(*http.Request) (*http.Response, error)

func (f originalDiskTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func originalDisk(t *testing.T, directory string) *imagecache.Disk {
	t.Helper()
	return imagecache.New(imagecache.Options{Directory: directory, MaxBytes: 1 << 20, MaxEntryBytes: 1 << 20, MaxEntries: 16})
}

func TestOriginalDiskCacheSurvivesRestartForPreviewAndDownload(t *testing.T) {
	body := detailPicture(t, "png", 80, 50)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	directory := t.TempDir()
	firstCache := files.NewOriginalCacheWithDisk(1<<20, originalDisk(t, directory))
	first := NewDetailLoader(DetailOptions{Originals: firstCache})
	if _, err := first.Load(context.Background(), server.URL, nil); err != nil {
		t.Fatal(err)
	}
	var offlineRequests atomic.Int32
	offline := &http.Client{Transport: originalDiskTransport(func(*http.Request) (*http.Response, error) {
		offlineRequests.Add(1)
		return nil, errors.New("network is offline")
	})}
	// Recreate both cache layers and loader, retaining only files on disk.
	restartedCache := files.NewOriginalCacheWithDisk(1<<20, originalDisk(t, directory))
	restarted := NewDetailLoader(DetailOptions{Originals: restartedCache, HTTPClient: offline})
	bitmap, err := restarted.Load(context.Background(), server.URL, nil)
	if err != nil || bitmap == nil {
		t.Fatalf("restarted offline preview did not load from disk: bitmap=%v err=%v", bitmap, err)
	}
	if width, height := bitmap.Size(); width != 80 || height != 50 {
		t.Fatalf("cached original dimensions changed: %dx%d", width, height)
	}
	// A new downloader can reuse the persistent original without a preview
	// first loading it into that downloader's memory cache.
	downloadCache := files.NewOriginalCacheWithDisk(1<<20, originalDisk(t, directory))
	downloader := files.New(files.Options{Originals: downloadCache, HTTPClient: offline, StateDir: t.TempDir()})
	path, err := downloader.DownloadResumable(context.Background(), domain.Wallpaper{ID: "abc123", Path: server.URL}, t.TempDir(), "download:abc123", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("cached original was not saved intact: err=%v", err)
	}
	if requests.Load() != 1 || offlineRequests.Load() != 0 {
		t.Fatalf("disk reuse made network requests: initial=%d offline=%d", requests.Load(), offlineRequests.Load())
	}
}

func TestOriginalDiskInvalidImageFallsBackAndRepairsCache(t *testing.T) {
	body := detailPicture(t, "png", 32, 24)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	directory := t.TempDir()
	// This is a valid storage envelope containing invalid media. Image-level
	// validation must remove it and retry the original source in this load.
	originalDisk(t, directory).Put(server.URL, []byte("invalid image"))
	cache := files.NewOriginalCacheWithDisk(1<<20, originalDisk(t, directory))
	if _, err := NewDetailLoader(DetailOptions{Originals: cache}).Load(context.Background(), server.URL, nil); err != nil {
		t.Fatal(err)
	}
	fresh := files.NewOriginalCacheWithDisk(1<<20, originalDisk(t, directory))
	if data, ok := fresh.Get(server.URL); !ok || !bytes.Equal(data, body) || requests.Load() != 1 {
		t.Fatalf("fallback did not repair the persistent original: found=%v requests=%d", ok, requests.Load())
	}
}

func TestOriginalDiskUnavailableDoesNotBlockImageLoading(t *testing.T) {
	body := detailPicture(t, "png", 16, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(server.Close)
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := files.NewOriginalCacheWithDisk(1<<20, originalDisk(t, filepath.Join(blocked, "cache")))
	bitmap, err := NewDetailLoader(DetailOptions{Originals: cache}).Load(context.Background(), server.URL, nil)
	if err != nil || bitmap == nil {
		t.Fatalf("unavailable optional cache broke original loading: bitmap=%v err=%v", bitmap, err)
	}
}
