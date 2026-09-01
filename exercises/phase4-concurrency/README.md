# Phase 4 — Concurrency (Mutex + Race-free)

## Goal

Make `OrderBook` from Phase 2 **thread-safe**. Many goroutines must be able
to call `PlaceOrder`, `CancelOrder`, and `GetBestBid/Ask` concurrently
without data races and without violating book invariants.

> **Scope**: same `OrderBook` API as Phase 2 — `PlaceOrder`, `CancelOrder`,
> `GetBestBid`, `GetBestAsk`. You're adding locking, not new behavior.

## What to implement

In `concurrency.go` (which is a re-implementation of the Phase 2 OrderBook
with locking — you can either modify Phase 2 or build a sibling here):

```go
type OrderBook struct {
    mu   sync.RWMutex  // readers share, writer is exclusive
    bids []orderbook.Order
    asks []orderbook.Order
}

func (ob *OrderBook) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error)
func (ob *OrderBook) CancelOrder(id string) error
func (ob *OrderBook) GetBestBid() *orderbook.Order
func (ob *OrderBook) GetBestAsk() *orderbook.Order
```

> **Decision**: are you going to (a) modify `phase2.OrderBook` to use a real
> mutex, or (b) build a fresh `phase4.OrderBook` that copies the slice
> ops? Option (a) is the natural answer if Phase 2 already had `mu
> sync.Mutex` reserved. Option (b) lets you compare implementations.
> Either is fine.

## Hints

### Mutex choice

- `sync.Mutex` for writers (`PlaceOrder`, `CancelOrder`).
- `sync.RWMutex` if you want many concurrent readers (`GetBestBid/Ask`)
  to not block each other.

For Phase 4, **start with `sync.Mutex`** everywhere. The optimization to
`RWMutex` is a micro-benchmark away.

### Critical section discipline

The lock must wrap **read-check-write** for any state that two goroutines
could touch. For example, "is the order on the book? if so, remove it"
must be a single critical section — otherwise two concurrent cancels of
the same order could double-process it.

### Copy on read

`GetBestBid` should return a pointer to a **copy** of the order, not a
pointer into the live slice. Otherwise the caller could mutate the
book via the returned pointer. Phase 2 already requires this; re-confirm
under the lock.

### Race detector

Run every test with `-race` and a high `-count` to flush out intermittent
races:

```bash

    test ./exercises/phase4-concurrency/... -race -count=100
```

If you see a `WARNING: DATA RACE`, your critical section is too small.

## Tests to pass

```bash

    test ./exercises/phase4-concurrency/... -race -count=10 -v
```

1. `TestConcurrent_PlaceOrders_NoRace` — 100 goroutines, each places 1 order
2. `TestConcurrent_CancelAndPlace` — 50 place / 50 cancel goroutines
3. `TestConcurrent_ReadersAndWriters` — many GetBestBid concurrent with PlaceOrder
4. `TestStress_HighVolume` — 1000 orders placed across 10 goroutines, run 100 times

## Study guide reference

- §6 Go concurrency models
- §6.1 Single mutex
- §6.5 RWMutex
- §7.2 TOCTOU prevention

## Extension (optional)

Build a tiny **benchmark** in `concurrency_bench_test.go` that compares
`sync.Mutex` vs `sync.RWMutex` for a 90% read / 10% write workload.
You don't need to win, just observe.

## Common pitfalls

- ❌ Forgetting `defer mu.Unlock()` (panic leaves the lock held forever).
- ❌ Calling `o.GetBestBid()` *while holding the write lock* — that's fine,
  but be careful not to call other methods that re-lock (would deadlock
  with `sync.Mutex`, would be fine with re-entrant locks but Go doesn't
  have those by default).
- ❌ Returning a pointer into the slice — caller can mutate the book.
- ❌ Splitting read and write across two separate `RLock`/`Lock` calls
  (TOCTOU).
