package files

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func TestOriginalCacheCopiesInputsRetainsSnapshotsAndBoundsLRU(t *testing.T) {
	cache := NewOriginalCache(6)
	input := []byte("one")
	cache.Put("one", input)
	input[0] = 'X'
	first, found := cache.Get("one")
	if !found || string(first) != "one" {
		t.Fatal("Put retained mutable caller data")
	}
	cache.Put("two", []byte("two"))
	cache.Get("one")
	cache.Put("new", []byte("new"))
	if _, found := cache.Get("two"); found {
		t.Fatal("least recently used image was not evicted")
	}
	cache.Put("one", []byte("ONE"))
	if string(first) != "one" {
		t.Fatal("replacing an entry mutated an earlier snapshot")
	}
	if next, ok := cache.Get("one"); !ok || string(next) != "ONE" {
		t.Fatal("replacement was lost")
	}
	cache.Put("oversize", []byte("1234567"))
	if _, ok := cache.Get("oversize"); ok {
		t.Fatal("oversized original was cached")
	}
	cache.mu.Lock()
	used, limit := cache.used, cache.limit
	cache.mu.Unlock()
	if used > limit {
		t.Fatalf("used=%d limit=%d", used, limit)
	}
}

func TestOriginalCacheConcurrentAccessAndEntryBound(t *testing.T) {
	cache := NewOriginalCache(64 << 10)
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			for n := range 500 {
				key := fmt.Sprintf("%d-%d", worker, n)
				data := bytes.Repeat([]byte{byte(n)}, 32)
				cache.Put(key, data)
				if got, found := cache.Get(key); found && !bytes.Equal(got, data) {
					t.Error("cache returned mutated bytes")
				}
			}
		})
	}
	workers.Wait()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.used > cache.limit || len(cache.entries) > maxOriginalEntries {
		t.Fatalf("cache exceeded bounds: bytes=%d entries=%d", cache.used, len(cache.entries))
	}
}
