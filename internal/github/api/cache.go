package api

import (
	"container/list"
	"sync"
)

const (
	cacheMaxEntries   = 1000
	cacheMaxBytes     = 64 << 20
	cacheMaxEntryBody = 1 << 20
)

// cacheEntry is a response GitHub may confirm with a 304. It is immutable once stored.
type cacheEntry struct {
	key  string
	etag string
	body []byte
	link string
}

// etagCache is an LRU of conditional-request entries, bounded by count and by total body bytes.
type etagCache struct {
	mu      sync.Mutex
	order   *list.List // front is the most recently used
	entries map[string]*list.Element
	bytes   int
}

func newETagCache() *etagCache {
	return &etagCache{order: list.New(), entries: map[string]*list.Element{}}
}

func (c *etagCache) get(key string) *cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return nil
	}
	c.order.MoveToFront(el)
	return el.Value.(*cacheEntry)
}

func (c *etagCache) put(key string, e *cacheEntry) {
	if len(e.body) > cacheMaxEntryBody {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
	e.key = key
	c.entries[key] = c.order.PushFront(e)
	c.bytes += len(e.body)
	for len(c.entries) > cacheMaxEntries || c.bytes > cacheMaxBytes {
		c.removeLocked(c.order.Back().Value.(*cacheEntry).key)
	}
}

func (c *etagCache) remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
}

func (c *etagCache) removeLocked(key string) {
	el, ok := c.entries[key]
	if !ok {
		return
	}
	c.bytes -= len(el.Value.(*cacheEntry).body)
	c.order.Remove(el)
	delete(c.entries, key)
}
