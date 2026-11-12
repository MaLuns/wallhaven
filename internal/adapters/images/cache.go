// Package images loads bounded, reusable thumbnail bitmaps outside the UI thread.
package images

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"image"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	kit "github.com/egoist/mygo/ui"
)

const (
	workerCount     = 8
	prefetchWorkers = 2
	queueCapacity   = 64
	maxEntries      = 512
	maxBodyBytes    = 8 << 20
	maxPixels       = 4 << 20
	retryDelay      = 30 * time.Second
)

type Options struct {
	HTTPClient *http.Client
	// OnChange requests a redraw, such as Window.Invalidate. It must not mutate
	// UI state: workers call it after publishing an immutable bitmap.
	OnChange func()
	MaxBytes int64
}

type entry struct {
	url     string
	bitmap  *kit.Bitmap
	loading bool
	visible bool
	queued  *list.Element
	// deferred marks unused lookahead discarded for memory pressure, rather
	// than a network failure. Get can retry it immediately when it is visible.
	deferred bool
	retryAt  time.Time
	size     int64
	position *list.Element
}

type Cache struct {
	mu             sync.Mutex
	entries        map[string]*entry
	recent         list.List
	used           int64
	limit          int64
	closed         bool
	client         *http.Client
	onChange       func()
	ctx            context.Context
	cancel         context.CancelFunc
	visibleJobs    list.List
	prefetchJobs   list.List
	activePrefetch int
	ready          *sync.Cond
	workers        sync.WaitGroup
	close          sync.Once
}

func New(options Options) *Cache {
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = 64 << 20
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Cache{
		entries: make(map[string]*entry), limit: options.MaxBytes,
		client: options.HTTPClient, onChange: options.OnChange,
		ctx: ctx, cancel: cancel,
	}
	c.ready = sync.NewCond(&c.mu)
	for range workerCount {
		c.workers.Add(1)
		go c.worker()
	}
	return c
}

// Get returns the cached immutable image, or nil and whether it is loading.
// Repeated calls during UI rebuilds share the same queued request. Only callers
// rendering visible cards should request images, to keep work proportional to
// the visible content. A failed load may be retried after a short cooldown.
func (c *Cache) Get(rawURL string) (kit.ImageSource, bool) {
	return c.request(rawURL, true)
}

// Prefetch prepares a nearby thumbnail without competing with queued visible
// images. Get promotes the same entry when it reaches the viewport; both calls
// share its request and decoded bitmap. Pending work remains bounded.
func (c *Cache) Prefetch(rawURL string) {
	c.request(rawURL, false)
}

func (c *Cache) request(rawURL string, visible bool) (kit.ImageSource, bool) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, false
	}
	e := c.entries[rawURL]
	if e != nil {
		if visible {
			c.recent.MoveToFront(e.position)
		}
		if e.bitmap != nil {
			return e.bitmap, false
		}
		if e.loading {
			if visible && !e.visible {
				e.visible = true
				if e.queued != nil {
					c.prefetchJobs.Remove(e.queued)
					e.queued = c.visibleJobs.PushFront(e)
					c.ready.Signal()
				}
			}
			return nil, true
		}
		if time.Now().Before(e.retryAt) && (!visible || !e.deferred) {
			return nil, false
		}
	} else {
		e = &entry{url: rawURL}
	}
	if c.visibleJobs.Len()+c.prefetchJobs.Len() >= queueCapacity {
		if !visible {
			return nil, true
		}
		// A fast scroll must not leave newly visible cards behind a full old
		// backlog. Drop pending lookahead first, then the oldest visible job.
		victim := c.prefetchJobs.Back()
		queue := &c.prefetchJobs
		if victim == nil {
			victim, queue = c.visibleJobs.Back(), &c.visibleJobs
		}
		dropped := victim.Value.(*entry)
		queue.Remove(victim)
		dropped.queued, dropped.loading = nil, false
		c.removeEntry(dropped)
	}
	e.loading, e.visible, e.deferred = true, visible, false
	if visible {
		e.queued = c.visibleJobs.PushFront(e)
	} else {
		e.queued = c.prefetchJobs.PushBack(e)
	}
	if e.position == nil {
		for len(c.entries) >= maxEntries && c.evictOne(nil) {
		}
		if visible {
			e.position = c.recent.PushFront(e)
		} else {
			e.position = c.recent.PushBack(e)
		}
		c.entries[rawURL] = e
	}
	c.ready.Signal()
	return nil, true
}

func (c *Cache) next() (*entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for !c.closed && c.visibleJobs.Len() == 0 && (c.prefetchJobs.Len() == 0 || c.activePrefetch >= prefetchWorkers) {
		c.ready.Wait()
	}
	if c.closed {
		return nil, false
	}
	queue := &c.visibleJobs
	prefetch := false
	if queue.Len() == 0 {
		queue = &c.prefetchJobs
		prefetch = true
		c.activePrefetch++
	}
	first := queue.Front()
	e := first.Value.(*entry)
	queue.Remove(first)
	e.queued = nil
	return e, prefetch
}

func (c *Cache) removeEntry(e *entry) {
	c.used -= e.size
	delete(c.entries, e.url)
	if e.position != nil {
		c.recent.Remove(e.position)
		e.position = nil
	}
}

func (c *Cache) worker() {
	defer c.workers.Done()
	for {
		e, prefetch := c.next()
		if e == nil {
			return
		}
		bitmap, size, err := c.fetch(e.url)
		c.mu.Lock()
		if prefetch {
			c.activePrefetch--
			c.ready.Broadcast()
		}
		if c.closed {
			c.mu.Unlock()
			return
		}
		e.loading = false
		if err != nil {
			e.retryAt = time.Now().Add(retryDelay)
		} else if !e.visible && c.used+size > c.limit {
			// Do not evict visible content for unused lookahead or immediately
			// fetch the same discarded image again on the completion redraw.
			e.deferred = true
			e.retryAt = time.Now().Add(retryDelay)
		} else {
			e.bitmap, e.size = bitmap, size
			c.used += size
			for c.used > c.limit && c.evictOne(e) {
			}
		}
		c.mu.Unlock()
		if c.onChange != nil {
			c.onChange()
		}
	}
}

// evictOne is called with mu held. Pending work retains identity for dedup.
func (c *Cache) evictOne(protect *entry) bool {
	for position := c.recent.Back(); position != nil; position = position.Prev() {
		e := position.Value.(*entry)
		if e == protect || e.loading {
			continue
		}
		c.removeEntry(e)
		return true
	}
	return false
}

func (c *Cache) fetch(rawURL string) (*kit.Bitmap, int64, error) {
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "image/*")
	response, err := c.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, 0, errors.New("thumbnail request failed")
	}
	if response.ContentLength > maxBodyBytes {
		return nil, 0, errors.New("thumbnail response too large")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if len(data) > maxBodyBytes {
		return nil, 0, errors.New("thumbnail response too large")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	pixels := int64(config.Width) * int64(config.Height)
	if config.Width <= 0 || config.Height <= 0 || config.Width > 8192 || config.Height > 8192 || pixels > maxPixels {
		return nil, 0, errors.New("thumbnail dimensions too large")
	}
	// Account for RGBA pixels, retained encoded bytes and generated mip levels.
	size := pixels*6 + int64(len(data))
	if size > c.limit {
		return nil, 0, errors.New("thumbnail exceeds image memory budget")
	}
	bitmap, err := kit.DecodeBitmap(data)
	return bitmap, size, err
}

// Close cancels in-flight requests and waits for workers. No callbacks occur
// after it returns. The bitmap objects themselves are never mutated here.
func (c *Cache) Close() {
	c.close.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		c.entries = make(map[string]*entry)
		c.recent.Init()
		c.visibleJobs.Init()
		c.prefetchJobs.Init()
		c.used = 0
		c.ready.Broadcast()
		c.mu.Unlock()
		c.workers.Wait()
	})
}
