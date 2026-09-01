// Package idempotency provides a TTL-backed cache for idempotency keys.
// It is safe for concurrent use.
package idempotency

import (
	"sync"
	"time"
)

type record struct {
	response  any
	expiresAt time.Time
}

// Guard caches responses by key with a TTL. Callers should Lookup before
// Store to avoid overwriting a fresh result.
type Guard struct {
	mu    sync.Mutex
	store map[string]record
}

// NewGuard returns an empty idempotency guard.
func NewGuard() *Guard {
	return &Guard{
		store: make(map[string]record),
	}
}

// Lookup returns (response, true) if the key exists and is not expired.
// Expired entries are lazily deleted as a side effect.
func (g *Guard) Lookup(key string) (any, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	r, ok := g.store[key]
	if !ok {
		return nil, false
	}

	if time.Now().After(r.expiresAt) {
		delete(g.store, key)
		return nil, false
	}

	return r.response, true
}

// Store caches response under key for ttl. Overwrites any prior value.
func (g *Guard) Store(key string, response any, ttl time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.store[key] = record{
		response:  response,
		expiresAt: time.Now().Add(ttl),
	}
}

// Sweep removes all expired entries and returns the count. Intended for
// use from a background ticker.
func (g *Guard) Sweep() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	var removed int
	for k, r := range g.store {
		if now.After(r.expiresAt) {
			delete(g.store, k)
			removed++
		}
	}
	return removed
}
