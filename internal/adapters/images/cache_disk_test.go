package images

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/imagecache"
	kit "github.com/egoist/mygo/ui"
)

func thumbnailDisk(directory string) *imagecache.Disk {
	return imagecache.New(imagecache.Options{Directory: directory, MaxBytes: 32 << 20, MaxEntryBytes: 16 << 20, MaxEntries: 32})
}

func requireThumbnailSize(t *testing.T, source kit.ImageSource, width, height int) {
	t.Helper()
	bitmap, ok := source.(*kit.Bitmap)
	if !ok {
		t.Fatalf("thumbnail = %T, want decoded bitmap", source)
	}
	if w, h := bitmap.Size(); w != width || h != height {
		t.Fatalf("thumbnail dimensions = %dx%d, want %dx%d", w, h, width, height)
	}
}

func TestThumbnailDiskCacheSurvivesRestartOffline(t *testing.T) {
	data := picture(t, 32, 24)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	directory := t.TempDir()
	updates, notify := notifications()
	first := New(Options{Disk: thumbnailDisk(directory), OnChange: notify})
	t.Cleanup(first.Close)
	requireThumbnailSize(t, waitImage(t, first, server.URL, updates), 32, 24)
	first.Close()
	if requests.Load() != 1 {
		t.Fatalf("initial thumbnail fetched %d times", requests.Load())
	}
	server.Close()

	// New memory and disk cache objects share only their filesystem directory.
	// The original HTTP endpoint is now unavailable, so a memory-only cache
	// cannot satisfy this request.
	updates, notify = notifications()
	second := New(Options{Disk: thumbnailDisk(directory), OnChange: notify})
	t.Cleanup(second.Close)
	requireThumbnailSize(t, waitImage(t, second, server.URL, updates), 32, 24)
	if requests.Load() != 1 {
		t.Fatalf("restart repeated the thumbnail download: %d requests", requests.Load())
	}
}

func TestThumbnailDiskCacheRepairsInvalidImageAndReusesRepair(t *testing.T) {
	data := picture(t, 24, 16)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	directory := t.TempDir()
	disk := thumbnailDisk(directory)
	disk.Put(server.URL, []byte("a cache envelope containing an invalid image"))
	updates, notify := notifications()
	first := New(Options{Disk: disk, OnChange: notify})
	t.Cleanup(first.Close)
	requireThumbnailSize(t, waitImage(t, first, server.URL, updates), 24, 16)
	first.Close()
	if repaired, found := disk.Get(server.URL); !found || !bytes.Equal(repaired, data) {
		t.Fatal("thumbnail load did not replace its damaged disk image")
	}
	server.Close()
	updates, notify = notifications()
	second := New(Options{Disk: thumbnailDisk(directory), OnChange: notify})
	t.Cleanup(second.Close)
	requireThumbnailSize(t, waitImage(t, second, server.URL, updates), 24, 16)
	if requests.Load() != 1 {
		t.Fatalf("repaired thumbnail was downloaded %d times", requests.Load())
	}
}

func TestThumbnailDiskUnavailableKeepsNetworkLoading(t *testing.T) {
	for _, unavailableAfterOpen := range []bool{false, true} {
		name := "unavailable-on-open"
		if unavailableAfterOpen {
			name = "directory-replaced-after-open"
		}
		t.Run(name, func(t *testing.T) {
			data := picture(t, 16, 12)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = w.Write(data)
			}))
			t.Cleanup(server.Close)
			directory := filepath.Join(t.TempDir(), "cache")
			var disk *imagecache.Disk
			if unavailableAfterOpen {
				disk = thumbnailDisk(directory)
				disk.Put(server.URL, data)
				if _, found := disk.Get(server.URL); !found {
					t.Fatal("cache was not available before its directory was replaced")
				}
				if err := os.RemoveAll(directory); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(directory, []byte("not a directory"), 0600); err != nil {
				t.Fatal(err)
			}
			if !unavailableAfterOpen {
				disk = thumbnailDisk(directory)
			}
			for range 2 {
				updates, notify := notifications()
				cache := New(Options{Disk: disk, OnChange: notify})
				t.Cleanup(cache.Close)
				requireThumbnailSize(t, waitImage(t, cache, server.URL, updates), 16, 12)
				cache.Close()
			}
			if requests.Load() != 2 {
				t.Fatalf("unavailable disk changed normal loading: %d requests", requests.Load())
			}
		})
	}
}

func TestThumbnailDiskHitsKeepImageSafetyLimits(t *testing.T) {
	valid := picture(t, 8, 8)
	oversized := append(append([]byte(nil), valid...), make([]byte, maxBodyBytes+1-len(valid))...)
	for _, test := range []struct {
		name      string
		cached    []byte
		maxMemory int64
	}{
		{"encoded-size", oversized, 0},
		{"width", picture(t, 8193, 1), 0},
		{"pixels", picture(t, 2049, 2049), 0},
		{"memory-budget", picture(t, 32, 32), 1024},
		// A PNG header passes DecodeConfig but the missing image data must
		// still fail complete bitmap decoding and trigger repair.
		{"decode", valid[:33], 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = w.Write(valid)
			}))
			t.Cleanup(server.Close)
			disk := thumbnailDisk(t.TempDir())
			disk.Put(server.URL, test.cached)
			if _, found := disk.Get(server.URL); !found {
				t.Fatal("test payload was not stored; image limits would not be exercised")
			}
			updates, notify := notifications()
			cache := New(Options{Disk: disk, MaxBytes: test.maxMemory, OnChange: notify})
			t.Cleanup(cache.Close)
			requireThumbnailSize(t, waitImage(t, cache, server.URL, updates), 8, 8)
			if requests.Load() != 1 {
				t.Fatal("cached image bypassed thumbnail validation limits")
			}
			if repaired, found := disk.Get(server.URL); !found || !bytes.Equal(repaired, valid) {
				t.Fatal("invalid cached image was not repaired with the validated response")
			}
		})
	}
}

func TestThumbnailDiskDoesNotStoreInvalidNetworkImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not an image"))
	}))
	t.Cleanup(server.Close)
	disk := thumbnailDisk(t.TempDir())
	updates, notify := notifications()
	cache := New(Options{Disk: disk, OnChange: notify})
	t.Cleanup(cache.Close)
	if source := waitImage(t, cache, server.URL, updates); source != nil {
		t.Fatal("invalid network response produced a bitmap")
	}
	if _, found := disk.Get(server.URL); found {
		t.Fatal("invalid network image was written to the persistent cache")
	}
}
