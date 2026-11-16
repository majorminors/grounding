package backends

import (
	"sync"
	"time"
)

// responseCache remembers successful GET bodies for a short while. The
// same DOI, seed paper or health probe is often requested several times
// in one session (check_citation then citation_graph, or a model
// retrying a search with the same terms), and every repeat costs
// rate-limit budget or, for the metered backends, money. It is off by
// default so short-lived CLI runs and tests see every request; the MCP
// server turns it on at startup.
type responseCache struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[string]cachedResponse
}

type cachedResponse struct {
	body    []byte
	expires time.Time
}

var cache *responseCache

const (
	defaultCacheTTL        = 10 * time.Minute
	defaultCacheMaxEntries = 512
)

// EnableResponseCache turns on the in-process GET cache with the given
// TTL (zero means the default of ten minutes).
func EnableResponseCache(ttl time.Duration) {
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	cache = &responseCache{
		ttl:        ttl,
		maxEntries: defaultCacheMaxEntries,
		entries:    make(map[string]cachedResponse),
	}
}

// DisableResponseCache turns the cache off (tests).
func DisableResponseCache() {
	cache = nil
}

// cacheKey identifies a request by URL plus any headers that change the
// answer (an API key changes the tier of data some backends return).
func cacheKey(req Request) string {
	key := req.URL
	for name, value := range req.Headers {
		key += "\x00" + name + "=" + value
	}
	return key
}

func (c *responseCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return entry.body, true
}

func (c *responseCache) put(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxEntries {
		c.evictExpiredOrOldest()
	}
	c.entries[key] = cachedResponse{body: body, expires: time.Now().Add(c.ttl)}
}

// evictExpiredOrOldest drops every expired entry, and if nothing had
// expired, the entry closest to expiry. Caller holds the lock.
func (c *responseCache) evictExpiredOrOldest() {
	now := time.Now()
	var oldestKey string
	var oldestExpiry time.Time
	for key, entry := range c.entries {
		if now.After(entry.expires) {
			delete(c.entries, key)
			continue
		}
		if oldestKey == "" || entry.expires.Before(oldestExpiry) {
			oldestKey, oldestExpiry = key, entry.expires
		}
	}
	if len(c.entries) >= c.maxEntries && oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}
