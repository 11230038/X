package video

import (
	"strconv"
	"testing"
	"time"
)

func TestSearchCacheAnswersUntilTheTTLRunsOut(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	cache := newSearchCache(time.Minute, func() time.Time { return now })
	cache.put("linear algebra|1", SearchResult{Query: "linear algebra", Page: 1})

	if hit, ok := cache.get("linear algebra|1"); !ok || hit.Page != 1 {
		t.Fatalf("get() = %+v, %v; want the page just stored", hit, ok)
	}
	now = now.Add(time.Minute)
	if _, ok := cache.get("linear algebra|1"); ok {
		t.Error("get() returned a page past its TTL")
	}
}

func TestSearchCacheKeyFoldsCaseAndSpacing(t *testing.T) {
	// A person typing the same question with different spacing means the same
	// question, and spending the upstream's quota on it twice would be spending
	// it on nothing.
	if first, second := cacheKey("  Linear   Algebra ", 2), cacheKey("linear algebra", 2); first != second {
		t.Errorf("cacheKey() = %q and %q, want one key", first, second)
	}
	if cacheKey("linear algebra", 1) == cacheKey("linear algebra", 2) {
		t.Error("cacheKey() put two pages on one key")
	}
}

func TestSearchCacheEvictsTheOldestPageAtCapacity(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	cache := newSearchCache(time.Hour, func() time.Time { return now })

	for index := 0; index <= searchCacheEntries; index++ {
		cache.put("query"+strconv.Itoa(index)+"|1", SearchResult{Page: 1})
	}

	if _, ok := cache.get("query0|1"); ok {
		t.Error("the oldest page survived one entry past the capacity")
	}
	newest := "query" + strconv.Itoa(searchCacheEntries) + "|1"
	if _, ok := cache.get(newest); !ok {
		t.Error("the newest page was evicted instead of the oldest")
	}
	if got := len(cache.entries); got != searchCacheEntries {
		t.Errorf("cache holds %d entries, want %d", got, searchCacheEntries)
	}
}
