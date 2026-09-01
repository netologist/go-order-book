// Package phase7 is your practice target for Phase 7: a thread-safe
// idempotency guard that maps a client-supplied key to the cached
// response of the first execution.
package phase7

import (
	"sync"
	"time"
)

// record is one cached response. expiresAt is the absolute deadline
// after which the record is no longer valid.
type record struct {
	response  any
	expiresAt time.Time
}

// IdempotencyGuard is a TTL-keyed cache. Single mutex is fine for
// Phase 7; the size of the store is bounded by the number of active
// orders so contention is low.
type IdempotencyGuard struct {
	mu    sync.Mutex
	store map[string]record
}

// NewIdempotencyGuard returns an empty guard.
func NewIdempotencyGuard() *IdempotencyGuard {
	return &IdempotencyGuard{
		store: make(map[string]record),
	}
}

// Lookup returns (response, true) if the key was stored and is not yet
// expired. Returns (nil, false) otherwise. Expired entries are deleted
// as a side effect (lazy GC).
func (g *IdempotencyGuard) Lookup(key string) (any, bool) {
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

// Store caches `response` under `key` for `ttl`. Overwrites any prior
// value (callers should Lookup first to avoid clobbering a fresh result).
func (g *IdempotencyGuard) Store(key string, response any, ttl time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.store[key] = record{
		response:  response,
		expiresAt: time.Now().Add(ttl),
	}
}

// Sweep removes all expired entries and returns the number removed.
// Use this from a background ticker.
func (g *IdempotencyGuard) Sweep() int {
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
