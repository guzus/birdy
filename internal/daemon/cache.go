package daemon

import (
	"encoding/json"
	"sync"
	"time"
)

// cacheEntry stores a cached runner result with an expiry time.
type cacheEntry struct {
	exitCode  int
	stdout    string
	stderr    string
	expiresAt time.Time
}

// ttlCache is a small TTL cache keyed by a JSON-serialized args slice.
// It is safe for concurrent use. A zero TTL disables caching at the call
// sites; this struct itself is only constructed when ttl > 0.
type ttlCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time // injectable clock for tests
	entries map[string]cacheEntry
}

// newTTLCache constructs a cache with the given TTL. If ttl <= 0 the
// caller should not construct one (server.go gates on this).
func newTTLCache(ttl time.Duration) *ttlCache {
	return &ttlCache{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]cacheEntry),
	}
}

// keyForArgs returns a stable cache key for the given args slice.
// Args order is preserved because it is significant for bird.
func keyForArgs(args []string) string {
	b, err := json.Marshal(args)
	if err != nil {
		// Marshal of []string never fails; fall back to a string join.
		// (Belt-and-suspenders — keeps the key stable even on bizarre input.)
		out := ""
		for i, a := range args {
			if i > 0 {
				out += "\x00"
			}
			out += a
		}
		return out
	}
	return string(b)
}

// get returns a cached entry if present and unexpired.
// The second return value reports whether a hit occurred.
func (c *ttlCache) get(key string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		return cacheEntry{}, false
	}
	if !c.now().Before(e.expiresAt) {
		delete(c.entries, key)
		return cacheEntry{}, false
	}
	return e, true
}

// set stores an entry under key with TTL counted from now().
func (c *ttlCache) set(key string, exitCode int, stdout, stderr string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = cacheEntry{
		exitCode:  exitCode,
		stdout:    stdout,
		stderr:    stderr,
		expiresAt: c.now().Add(c.ttl),
	}
}

// size returns the current number of stored entries (including any that
// may have expired but not yet been pruned by a get).
func (c *ttlCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
