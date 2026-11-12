package images

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitThumbnailRequest(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case path := <-started:
		return path
	case <-time.After(3 * time.Second):
		t.Fatal("thumbnail request did not start")
		return ""
	}
}

// Each request occupies a worker until the test releases a permit or cancels
// the cache. This observes network ordering rather than scheduler internals.
func blockedThumbnailServer(t *testing.T) (*httptest.Server, chan string, chan struct{}) {
	t.Helper()
	data := picture(t, 8, 8)
	started := make(chan string, 100)
	permits := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.URL.Path
		select {
		case <-permits:
			_, _ = w.Write(data)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	return server, started, permits
}

func occupyThumbnailWorkers(t *testing.T, cache *Cache, base string, started <-chan string) {
	t.Helper()
	for i := range workerCount {
		cache.Get(fmt.Sprintf("%s/block-%d", base, i))
	}
	for range workerCount {
		if path := waitThumbnailRequest(t, started); !strings.HasPrefix(path, "/block-") {
			t.Fatalf("unexpected initial request %s", path)
		}
	}
}

func TestEightVisibleThumbnailsStartWithoutWaitingForSlowResponses(t *testing.T) {
	server, started, _ := blockedThumbnailServer(t)
	cache := New(Options{})
	t.Cleanup(cache.Close)
	for i := range 12 {
		cache.Get(fmt.Sprintf("%s/visible-%d", server.URL, i))
	}
	// No response is released: eight visible requests must start without
	// waiting for any earlier thumbnail to finish downloading or decoding.
	for range 8 {
		if path := waitThumbnailRequest(t, started); !strings.HasPrefix(path, "/visible-") {
			t.Fatalf("unexpected thumbnail request %s", path)
		}
	}
	cache.mu.Lock()
	queued := cache.visibleJobs.Len()
	cache.mu.Unlock()
	if queued != 4 {
		t.Fatalf("pending visible requests = %d, want four after eight start", queued)
	}
}

func TestVisibleThumbnailsBypassQueuedLookaheadAndOldBacklog(t *testing.T) {
	for _, pendingKind := range []string{"prefetch", "old-visible"} {
		t.Run(pendingKind, func(t *testing.T) {
			server, started, permits := blockedThumbnailServer(t)
			cache := New(Options{})
			t.Cleanup(cache.Close)
			occupyThumbnailWorkers(t, cache, server.URL, started)
			for i := range queueCapacity {
				url := fmt.Sprintf("%s/old-%d", server.URL, i)
				if pendingKind == "prefetch" {
					cache.Prefetch(url)
				} else {
					cache.Get(url)
				}
			}
			if image, loading := cache.Get(server.URL + "/new-visible"); image != nil || !loading {
				t.Fatal("newly visible image was not queued")
			}
			cache.mu.Lock()
			queued := cache.visibleJobs.Len() + cache.prefetchJobs.Len()
			cache.mu.Unlock()
			if queued != queueCapacity {
				t.Fatalf("pending queue grew past its bound: %d", queued)
			}
			permits <- struct{}{}
			if next := waitThumbnailRequest(t, started); next != "/new-visible" {
				t.Fatalf("newly visible thumbnail waited behind %s", next)
			}
		})
	}
}

func TestLookaheadReservesWorkersForNewVisibleImages(t *testing.T) {
	server, started, _ := blockedThumbnailServer(t)
	cache := New(Options{})
	t.Cleanup(cache.Close)
	for i := 0; i < 10; i++ {
		cache.Prefetch(fmt.Sprintf("%s/prefetch-%d", server.URL, i))
	}
	for range prefetchWorkers {
		if path := waitThumbnailRequest(t, started); !strings.HasPrefix(path, "/prefetch-") {
			t.Fatalf("unexpected lookahead request %s", path)
		}
	}
	for i := range 6 {
		cache.Get(fmt.Sprintf("%s/visible-now-%d", server.URL, i))
	}
	for range 6 {
		if next := waitThumbnailRequest(t, started); !strings.HasPrefix(next, "/visible-now-") {
			t.Fatalf("visible image waited for lookahead request %s", next)
		}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.activePrefetch != 2 || cache.prefetchJobs.Len() != 8 {
		t.Fatalf("lookahead active=%d queued=%d, want two active and eight queued", cache.activePrefetch, cache.prefetchJobs.Len())
	}
}

func TestGetPromotesPrefetchAndDeduplicatesRebuilds(t *testing.T) {
	data := picture(t, 8, 8)
	started := make(chan string, 100)
	permits := make(chan struct{})
	var promotedRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/promoted" {
			promotedRequests.Add(1)
		}
		started <- r.URL.Path
		select {
		case <-permits:
			_, _ = w.Write(data)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	updates, notify := notifications()
	cache := New(Options{OnChange: notify})
	t.Cleanup(cache.Close)
	occupyThumbnailWorkers(t, cache, server.URL, started)
	cache.Prefetch(server.URL + "/low")
	for range 100 {
		cache.Prefetch(server.URL + "/promoted")
	}
	for range 100 {
		cache.Get(server.URL + "/promoted")
	}
	cache.mu.Lock()
	visible, prefetch := cache.visibleJobs.Len(), cache.prefetchJobs.Len()
	cache.mu.Unlock()
	if visible != 1 || prefetch != 1 {
		t.Fatalf("rebuilds duplicated queued work: visible=%d prefetch=%d", visible, prefetch)
	}
	permits <- struct{}{}
	if next := waitThumbnailRequest(t, started); next != "/promoted" {
		t.Fatalf("Get did not promote its prefetched image: %s", next)
	}
	close(permits)
	if waitImage(t, cache, server.URL+"/promoted", updates) == nil {
		t.Fatal("promoted thumbnail did not finish")
	}
	if got := promotedRequests.Load(); got != 1 {
		t.Fatalf("prefetch/Get downloaded the same image %d times", got)
	}
}

func TestUnusedPrefetchDoesNotEvictVisibleImageToExceedBudget(t *testing.T) {
	data := picture(t, 32, 32)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	updates, notify := notifications()
	cache := New(Options{OnChange: notify, MaxBytes: 8 << 10})
	t.Cleanup(cache.Close)
	cache.Get(server.URL + "/visible")
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("visible thumbnail did not finish")
	}
	if image, pending := cache.Get(server.URL + "/visible"); image == nil || pending {
		t.Fatal("visible thumbnail is not cached")
	}
	cache.Prefetch(server.URL + "/lookahead")
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("lookahead thumbnail did not finish")
	}
	cache.mu.Lock()
	deferred := cache.entries[server.URL+"/lookahead"]
	valid := cache.used <= cache.limit && cache.entries[server.URL+"/visible"] != nil && deferred != nil && deferred.bitmap == nil && deferred.deferred
	cache.mu.Unlock()
	if !valid {
		t.Fatal("lookahead displaced the visible image or exceeded the memory bound")
	}
	for range 20 {
		cache.Prefetch(server.URL + "/lookahead")
	}
	if requests.Load() != 2 {
		t.Fatal("discarded lookahead was fetched repeatedly after redraw")
	}
	if image := waitImage(t, cache, server.URL+"/lookahead", updates); image == nil {
		t.Fatal("discarded lookahead could not load when it became visible")
	}
}
