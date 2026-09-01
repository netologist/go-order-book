# Phase 7 — Idempotency Guard

## Goal

Network retries happen. A client sends `POST /orders` with header
`Idempotency-Key: K1`, gets a timeout, retries with the same key — and
the system must not place the order twice. The first response is
**replayed** verbatim.

The guard has two operations:

- `Lookup(key)` — return `(response, true)` if seen and not expired; `(nil, false)` otherwise.
- `Store(key, response, ttl)` — cache the response for `ttl`.

## What to implement

In `idempotency.go`:

```go
type IdempotencyGuard struct {
    mu    sync.Mutex
    store map[string]record
}

type record struct {
    response  any
    expiresAt time.Time
}

func NewIdempotencyGuard() *IdempotencyGuard
func (g *IdempotencyGuard) Lookup(key string) (any, bool)
func (g *IdempotencyGuard) Store(key string, response any, ttl time.Duration)
func (g *IdempotencyGuard) Sweep() int   // remove expired, return count
```

## Hints

### TTL

Use `time.Now().After(record.expiresAt)` to detect expiry. The `Sweep`
helper walks the map and deletes expired entries — useful for a
background goroutine (`time.Ticker`) in production.

### Concurrency

Two operations, both under one `mu`. Same TOCTOU discipline as Phase 6.

### Don't be clever with time

`time.Now()` is fine. Don't reach for `time.Since` inside the lock unless
you have a reason. Readable code wins.

### Generic-typed response

`response any` lets callers store anything (the HTTP layer will marshal
it to JSON later). Go 1.18+ syntax.

## Tests to pass

```bash

    test ./exercises/phase7-idempotency/... -count=1 -v
```

1. `TestLookup_UnseenKey` — returns `(nil, false)`
2. `TestStore_Then_Lookup` — returns `(response, true)`
3. `TestLookup_AfterExpiry` — returns `(nil, false)` and the entry is gone
4. `TestSweep_RemovesExpired` — sweep count = N, store size = 0
5. `TestConcurrent_SameKey_OnlyOneEffectiveWrite` — 100 goroutines, 1 wins
6. `TestDifferentKeys_Independent` — key A and key B don't collide

## Study guide reference

- §7.3 Idempotency key pattern
- §7.4 Optimistic vs pessimistic locking
- §12.2 Q1 (DB-level UNIQUE constraint on idempotency_key)

## Extension (optional)

Add a `Start(ctx)` method that runs a background `time.Ticker` and
periodically calls `Sweep`. Wire it into the voltron main in Phase 12.
The test for it is: kick off the background sweeper with a 10ms
interval, store a key with 5ms TTL, wait 50ms, assert the key is gone.
