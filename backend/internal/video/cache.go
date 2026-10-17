package video

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// searchCacheEntries bounds how many keyword pages are remembered. One page is
// at most twenty results and a few dozen kilobytes, so the ceiling is a few
// megabytes; it is here so that a client that walks pages cannot grow the map
// without end.
const searchCacheEntries = 256

type searchCacheEntry struct {
	result  SearchResult
	expires time.Time
}

// searchCache is an in-process, per-replica cache. Two replicas therefore do
// not share it, which is the same honesty internal/limit carries: the bound is
// per process, not per deployment.
type searchCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[string]searchCacheEntry
	// order is insertion order, oldest first. It is what makes eviction
	// predictable without counting hits: this cache exists to spare the upstream
	// a repeated search, not to be a good LRU.
	order []string
}

func newSearchCache(ttl time.Duration, now func() time.Time) *searchCache {
	return &searchCache{
		ttl:     ttl,
		now:     now,
		entries: make(map[string]searchCacheEntry),
	}
}

func (c *searchCache) get(key string) (SearchResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return SearchResult{}, false
	}
	if !c.now().Before(entry.expires) {
		delete(c.entries, key)
		c.forgetLocked(key)
		return SearchResult{}, false
	}
	return entry.result, true
}

func (c *searchCache) put(key string, result SearchResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.evictExpiredLocked(now)

	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = searchCacheEntry{result: result, expires: now.Add(c.ttl)}

	for len(c.order) > searchCacheEntries {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

func (c *searchCache) evictExpiredLocked(now time.Time) {
	kept := c.order[:0]
	for _, key := range c.order {
		entry, ok := c.entries[key]
		if !ok {
			continue
		}
		if !now.Before(entry.expires) {
			delete(c.entries, key)
			continue
		}
		kept = append(kept, key)
	}
	c.order = kept
}

func (c *searchCache) forgetLocked(key string) {
	for index, existing := range c.order {
		if existing == key {
			c.order = append(c.order[:index], c.order[index+1:]...)
			return
		}
	}
}

// cacheKey normalises a keyword so that "  Linear  Algebra " and "linear
// algebra" are one entry. Case and spacing are the two differences a person
// does not mean, and caching them separately would spend the upstream's quota on
// the same question.
func cacheKey(text string, page int) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " ")) + "|" + strconv.Itoa(page)
}
