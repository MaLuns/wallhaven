package images

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kit "github.com/egoist/mygo/ui"
)

func picture(t *testing.T, width, height int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, width, height))
	im.Set(0, 0, color.RGBA{R: 200, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, im); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func waitImage(t *testing.T, cache *Cache, url string, updates <-chan struct{}) kit.ImageSource {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		image, loading := cache.Get(url)
		if !loading {
			return image
		}
		select {
		case <-updates:
		case <-deadline.C:
			t.Fatal("image load did not finish")
		}
	}
}

func notifications() (chan struct{}, func()) {
	updates := make(chan struct{}, 100)
	return updates, func() { updates <- struct{}{} }
}

func TestCacheDeduplicatesRebuildsAndSharesBitmap(t *testing.T) {
	data := picture(t, 32, 24)
	var requests atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-release
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	updates, notify := notifications()
	cache := New(Options{OnChange: notify})
	t.Cleanup(cache.Close)
	if image, loading := cache.Get(server.URL); image != nil || !loading {
		t.Fatal("first render must queue image")
	}
	<-started
	var rebuilds sync.WaitGroup
	for range 100 {
		rebuilds.Add(1)
		go func() {
			defer rebuilds.Done()
			cache.Get(server.URL)
		}()
	}
	rebuilds.Wait()
	close(release)
	loaded := waitImage(t, cache, server.URL, updates)
	bitmap, ok := loaded.(*kit.Bitmap)
	if !ok {
		t.Fatalf("image type = %T, want bitmap", loaded)
	}
	if w, h := bitmap.Size(); w != 32 || h != 24 {
		t.Fatalf("decoded size = %dx%d", w, h)
	}
	hit, pending := cache.Get(server.URL)
	if pending || hit != loaded || requests.Load() != 1 {
		t.Fatalf("cache hit changed bitmap or refetched: pending=%t requests=%d", pending, requests.Load())
	}
}

func TestCacheRejectsFailuresAndCoolsDown(t *testing.T) {
	tooWide := picture(t, 8193, 1)
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
		large  bool
	}{
		{"http", http.StatusForbidden, []byte("denied"), false},
		{"invalid-image", http.StatusOK, []byte("not an image"), false},
		{"dimensions", http.StatusOK, tooWide, false},
		{"response-size", http.StatusOK, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if tc.large {
					w.Header().Set("Content-Length", strconv.Itoa(maxBodyBytes+1))
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			}))
			t.Cleanup(server.Close)
			updates, notify := notifications()
			cache := New(Options{OnChange: notify})
			t.Cleanup(cache.Close)
			cache.Get(server.URL)
			if waitImage(t, cache, server.URL, updates) != nil {
				t.Fatal("invalid response produced image")
			}
			for range 20 {
				if value, loading := cache.Get(server.URL); value != nil || loading {
					t.Fatal("failed image must retain cooldown")
				}
			}
			if requests.Load() != 1 {
				t.Fatalf("failed image fetched %d times", requests.Load())
			}
		})
	}
}

func TestCacheEvictsLeastRecentlyUsedAndHonorsBudget(t *testing.T) {
	data := picture(t, 32, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	updates, notify := notifications()
	cache := New(Options{OnChange: notify, MaxBytes: 8 << 10})
	t.Cleanup(cache.Close)
	for _, path := range []string{"/a", "/b", "/c"} {
		if waitImage(t, cache, server.URL+path, updates) == nil {
			t.Fatal("valid thumbnail was rejected")
		}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.used > cache.limit || len(cache.entries) != 1 || cache.entries[server.URL+"/c"] == nil {
		t.Fatalf("cache budget / LRU: bytes=%d entries=%d", cache.used, len(cache.entries))
	}
}

func TestCacheBoundsWorkersAndCancelsOnClose(t *testing.T) {
	var active, peak atomic.Int32
	started := make(chan struct{}, workerCount)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); n > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, n) {
				break
			}
		}
		started <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	cache := New(Options{})
	t.Cleanup(cache.Close)
	for i := range 100 {
		cache.Get(server.URL + "/" + strconv.Itoa(i))
	}
	for range workerCount {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	closed := make(chan struct{})
	go func() { cache.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("close did not cancel network requests")
	}
	if peak.Load() != workerCount {
		t.Fatalf("network concurrency = %d, want %d", peak.Load(), workerCount)
	}
	if image, loading := cache.Get(server.URL); image != nil || loading {
		t.Fatal("closed cache accepted work")
	}
}

func TestCacheRejectsNonHTTPAndCredentialURLs(t *testing.T) {
	cache := New(Options{})
	defer cache.Close()
	for _, url := range []string{"", "file:///tmp/picture.png", "ftp://example.com/image", "https://user:secret@example.com/image", "not a URL"} {
		if value, loading := cache.Get(url); value != nil || loading {
			t.Fatalf("accepted unsupported URL %q", url)
		}
	}
}
