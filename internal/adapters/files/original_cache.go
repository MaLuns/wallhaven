package files

import (
	"bytes"
	"container/list"
	"context"
	"sync"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/imagecache"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

const maxOriginalEntries = 128

type originalEntry struct {
	url  string
	data []byte
}

// OriginalCache keeps bounded immutable snapshots of encoded originals. Put
// copies the input; callers of Get must honor EncodedOriginals' read-only rule.
type OriginalCache struct {
	mu      sync.Mutex
	limit   int64
	used    int64
	entries map[string]*list.Element
	recent  list.List
	disk    *imagecache.Disk
}

var _ ports.EncodedOriginals = (*OriginalCache)(nil)

func NewOriginalCache(maxBytes int64) *OriginalCache {
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	return &OriginalCache{limit: maxBytes, entries: make(map[string]*list.Element)}
}

// NewOriginalCacheWithDisk retains encoded originals across launches. Call Get
// and Put on loading/download workers because a memory miss can access disk.
func NewOriginalCacheWithDisk(maxBytes int64, disk *imagecache.Disk) *OriginalCache {
	cache := NewOriginalCache(maxBytes)
	cache.disk = disk
	return cache
}

func (c *OriginalCache) Get(url string) ([]byte, bool) {
	c.mu.Lock()
	position := c.entries[url]
	if position != nil {
		c.recent.MoveToFront(position)
		data := position.Value.(originalEntry).data
		c.mu.Unlock()
		return data, true
	}
	c.mu.Unlock()
	if c.disk != nil {
		if data, found := c.disk.Get(url); found {
			c.putMemory(url, data)
			return data, true
		}
	}
	return nil, false
}

func (c *OriginalCache) Put(url string, data []byte) {
	c.PutIfCurrent(url, data, c.Generation())
}

// Generation is a nonblocking token for requests that start before disk clear.
func (c *OriginalCache) Generation() uint64 { return c.disk.Generation() }

func (c *OriginalCache) PutIfCurrent(url string, data []byte, generation uint64) {
	c.putMemory(url, data)
	if c.disk != nil {
		c.disk.PutIfCurrent(url, data, generation)
	}
}

type originalGenerationKey struct{}

func originalCacheContext(ctx context.Context, cache ports.EncodedOriginals) context.Context {
	if snapshots, ok := cache.(ports.VersionedEncodedOriginals); ok {
		return context.WithValue(ctx, originalGenerationKey{}, snapshots.Generation())
	}
	return ctx
}

func putOriginal(ctx context.Context, cache ports.EncodedOriginals, url string, data []byte) {
	if generation, ok := ctx.Value(originalGenerationKey{}).(uint64); ok {
		if snapshots, ok := cache.(ports.VersionedEncodedOriginals); ok {
			snapshots.PutIfCurrent(url, data, generation)
			return
		}
	}
	cache.Put(url, data)
}

func (c *OriginalCache) putMemory(url string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous := c.entries[url]; previous != nil {
		c.remove(previous)
	}
	if url == "" || len(url) > 8192 || len(data) == 0 || int64(len(data)) > c.limit {
		return
	}
	for c.used+int64(len(data)) > c.limit || len(c.entries) >= maxOriginalEntries {
		c.remove(c.recent.Back())
	}
	copy := bytes.Clone(data)
	c.entries[url] = c.recent.PushFront(originalEntry{url, copy})
	c.used += int64(len(copy))
}

// Delete invalidates both copies when cached bytes fail image validation.
func (c *OriginalCache) Delete(url string) {
	c.mu.Lock()
	if position := c.entries[url]; position != nil {
		c.remove(position)
	}
	c.mu.Unlock()
	if c.disk != nil {
		c.disk.Delete(url)
	}
}

func (c *OriginalCache) remove(position *list.Element) {
	entry := position.Value.(originalEntry)
	c.used -= int64(len(entry.data))
	delete(c.entries, entry.url)
	c.recent.Remove(position)
}
