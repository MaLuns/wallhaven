package files

import (
	"bytes"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/imagecache"
)

func TestOriginalMemoryEvictionStillHitsDiskAndDeleteClearsBoth(t *testing.T) {
	directory := t.TempDir()
	newDisk := func() *imagecache.Disk {
		return imagecache.New(imagecache.Options{Directory: directory, MaxBytes: 4096, MaxEntryBytes: 1024, MaxEntries: 16})
	}
	cache := NewOriginalCacheWithDisk(6, newDisk())
	input := []byte("one")
	cache.Put("https://images.test/one", input)
	input[0] = 'X'
	cache.Put("https://images.test/two", []byte("two"))
	cache.Put("https://images.test/new", []byte("new"))
	if data, ok := cache.Get("https://images.test/one"); !ok || !bytes.Equal(data, []byte("one")) {
		t.Fatal("memory eviction lost the immutable disk snapshot")
	}
	cache.mu.Lock()
	used := cache.used
	cache.mu.Unlock()
	if used > 6 {
		t.Fatalf("disk hit exceeded memory budget: %d", used)
	}
	cache.Delete("https://images.test/one")
	if _, ok := cache.Get("https://images.test/one"); ok {
		t.Fatal("Delete retained an in-memory copy")
	}
	if _, ok := NewOriginalCacheWithDisk(6, newDisk()).Get("https://images.test/one"); ok {
		t.Fatal("Delete retained the persistent copy")
	}
}
