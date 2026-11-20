package upstream

import (
	"sync"
	"time"
)

// respCache holds public documents check_stack reads: the project roster, a
// project's release list and single releases, keyed by their public path. Every
// key is something anyone can fetch, and none of them is derived from a
// caller's running version — check_stack reads a project's releases whatever
// version it was asked about (see fetchSet in internal/tools), so the cache
// cannot hold one either. It exists because that design reads more than one call strictly
// needs, and agents re-run check_stack within minutes: the second run should
// cost nothing upstream. /v2/upgrade and /v2/search, which carry what a
// caller typed, never go through it.
//
// It is bounded in bytes as well as entries. An entry count alone let one
// stack-wide call with detail=full fill it with hundreds of release documents,
// and the hosted container (64 MiB) died at two such calls in flight; the
// byte budget keeps the cache a small, fixed share of the process whatever the
// callers ask for.
type respCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	max      int
	maxBytes int
	size     int
	data     map[string]cacheEntry
}

type cacheEntry struct {
	body []byte
	at   time.Time
}

func newRespCache(ttl time.Duration, max, maxBytes int) *respCache {
	return &respCache{ttl: ttl, max: max, maxBytes: maxBytes, data: map[string]cacheEntry{}}
}

func (c *respCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[key]
	if !ok || time.Since(e.at) > c.ttl {
		return nil, false
	}
	return e.body, true
}

func (c *respCache) put(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(body) > c.maxBytes/8 {
		return // one document that large would push out everything else; read it fresh next time
	}
	if old, ok := c.data[key]; ok {
		c.size -= len(old.body)
		delete(c.data, key)
	}
	if len(c.data) >= c.max || c.size+len(body) > c.maxBytes {
		for k, e := range c.data {
			if time.Since(e.at) > c.ttl {
				c.size -= len(e.body)
				delete(c.data, k)
			}
		}
		if len(c.data) >= c.max || c.size+len(body) > c.maxBytes {
			c.data = map[string]cacheEntry{} // still full of live entries: start over rather than grow
			c.size = 0
		}
	}
	c.data[key] = cacheEntry{body: body, at: time.Now()}
	c.size += len(body)
}
