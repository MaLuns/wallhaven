package files

import (
	"bytes"
	"container/list"
	"sync"

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
}

var _ ports.EncodedOriginals = (*OriginalCache)(nil)

func NewOriginalCache(maxBytes int64) *OriginalCache {
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	return &OriginalCache{limit: maxBytes, entries: make(map[string]*list.Element)}
}

func (c *OriginalCache) Get(url string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	position := c.entries[url]
	if position == nil {
		return nil, false
	}
	c.recent.MoveToFront(position)
	return position.Value.(originalEntry).data, true
}

func (c *OriginalCache) Put(url string, data []byte) {
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

func (c *OriginalCache) remove(position *list.Element) {
	entry := position.Value.(originalEntry)
	c.used -= int64(len(entry.data))
	delete(c.entries, entry.url)
	c.recent.Remove(position)
}
