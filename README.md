# go-order-book

A production-quality **prediction-market order book and matching engine** built in idiomatic Go.

This project is a **design-patterns tutorial disguised as a real trading system** — every design decision is
documented in the source and this README walks you through the full implementation step by step, exactly as
the `exercises/` folder structures it: one phase at a time, test-driven, from the simplest slice to a
fully wired HTTP server.

---

## Table of Contents

1. [What is an Order Book?](#what-is-an-order-book)
2. [Domain: Prediction Markets](#domain-prediction-markets)
3. [Architecture at a Glance](#architecture-at-a-glance)
4. [Libraries and Dependencies](#libraries-and-dependencies)
5. [Phase 2 — The Order Book (sorted slice)](#phase-2--the-order-book-sorted-slice)
6. [Phase 3 — The Matching Engine (Market + IOC + Self-match)](#phase-3--the-matching-engine-market--ioc--self-match)
7. [Phase 4 — Concurrency (Mutex + Race-free)](#phase-4--concurrency-mutex--race-free)
8. [Phase 5 — Market State Machine](#phase-5--market-state-machine)
9. [Phase 6 — Ledger (Balances + Hold, TOCTOU-safe)](#phase-6--ledger-balances--hold-toctou-safe)
10. [Phase 7 — Idempotency Guard](#phase-7--idempotency-guard)
11. [Phase 8 — HTTP API Layer](#phase-8--http-api-layer)
12. [Phase 9 — Event Bus (Pub/Sub)](#phase-9--event-bus-pubsub)
13. [Phase 10 — Integration: Voltron](#phase-10--integration-voltron)
14. [Concurrency Model Summary](#concurrency-model-summary)
15. [Running the Project](#running-the-project)
16. [API Reference](#api-reference)
17. [Practice Exercises](#practice-exercises)

---

## What is an Order Book?

An **order book** is the core data structure of any exchange — stock, crypto, or prediction market.
It holds two sorted lists of resting orders:

```
BIDS (buyers)           ASKS (sellers)
─────────────────       ─────────────────
price 67¢  qty 5        price 64¢  qty 10   ← best ask (lowest)
price 65¢  qty 12       price 68¢  qty 3
price 63¢  qty 7        price 70¢  qty 20
```

When a new order arrives, the **matching engine** checks whether it *crosses* any resting order on the
opposite side. A buy at 65¢ crosses an ask at 64¢ — they trade at 64¢ (the **passive** price, giving
*price improvement* to the aggressive buyer).

**Three key rules:**

1. **Price-time priority** — lower asks fill first; at equal price, the older order fills first.
2. **No self-trade** — an order never matches against an order from the same user.
3. **Price improvement** — the aggressor (new order) gets the resting order's price, not its own.

---

## Domain: Prediction Markets

This order book is built for a prediction market (e.g., "Will team X win?"). Prices are **integer cents**
in **[1, 100]** — they represent the probability in percent. Buying at 64¢ means you believe the event
has a ≥64% chance of occurring. All arithmetic uses `int64` (no `float64` — floating point is wrong for
money).

A contract always settles at either **0¢** (NO) or **100¢** (YES), so prices live in a bounded range
and the book's validation layer enforces this strictly.

---

## Architecture at a Glance

```
HTTP request
     │
     ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         api.Handlers                                │
│  POST /orders  →  idempotency check  →  parse & validate            │
│  DELETE /orders/{id}                                                │
└──────────────────────────────┬──────────────────────────────────────┘
                               │
                               ▼
                     ┌──────────────────┐
                     │  market.Market   │  state gate (Open/Live/Paused/Settled)
                     └────────┬─────────┘
                              │ PlaceOrder only if Live
                              ▼
                     ┌──────────────────┐
                     │ orderbook.Order  │  validate → match → rest
                     │      Book        │
                     └────────┬─────────┘
                              │ []Trade
                              ▼
               ┌──────────────────────────────┐
               │        ledger.Ledger         │  reserve → settle
               └──────────────────────────────┘
                              │
                              ▼
               ┌──────────────────────────────┐
               │        events.EventBus       │  publish TradeExecuted
               └──────────────────────────────┘
```

Each layer is **independently testable and swappable** — the API layer only knows the `OrderPlacer`
interface; the matching engine only knows the `Order` type; the ledger is a pure state machine.

---

## Libraries and Dependencies

This project is **zero-dependency** for production code — the only `go.mod` entry is the Go stdlib.

| Area | Package | Why |
|---|---|---|
| Sorting | `sort` (stdlib) | `sort.Search` for binary insertion into sorted slices |
| Concurrency | `sync` (stdlib) | `sync.Mutex`, `sync.RWMutex` |
| HTTP routing | `net/http` (stdlib) | Go 1.22+ named path parameters (`/orders/{id}`) |
| Structured logging | `log/slog` (stdlib, Go 1.21+) | JSON or text handler, zero-alloc hot path |
| Time | `time` (stdlib) | Price-time priority tiebreaker |
| Testing | `testing` (stdlib) | Table-driven tests, `-race` flag |
| Race detector | `go test -race` (built-in tool) | Catches data races at test time |

**No third-party testing library** (no testify, no gomock) — the stdlib `testing` package is sufficient
for an interview-grade project of this scope, and avoids explaining dependency choices under time pressure.

---

## Phase 2 — The Order Book (sorted slice)

> **Goal:** build the simplest correct in-memory order book. Single-threaded, limit orders only.

### Data structure choice: sorted slice vs. other options

| Option | Insert | Lookup best | Delete by ID | Cache locality |
|---|---|---|---|---|
| **Sorted slice** (chosen) | O(n) shift | O(1) head | O(n) scan | Excellent |
| `heap.Interface` | O(log n) | O(1) Pop | O(n) rebuild | Good |
| B-Tree / `btree` pkg | O(log n) | O(log n) | O(log n) | Fair |
| `list.List` (linked list) | O(1) if you have a node ptr | O(n) scan | O(1) if you have a ptr | Poor |

**Why sorted slice wins for this scale:**

An order book in a prediction market for a single contract holds at most a few thousand resting orders.
At that scale, O(n) insertion with CPU cache locality beats O(log n) with pointer-chasing every time.
`sort.Search` (binary search) finds the insertion point in O(log n), then a single `append` + `copy`
shifts elements — all within one contiguous allocation.

### Sort order

```
bids: price DESC, timestamp ASC, id ASC   ← highest price first; FIFO at same price
asks: price ASC,  timestamp ASC, id ASC   ← lowest price first; FIFO at same price
```

The **three-key comparator** is critical. Without the `id ASC` tiebreaker, two orders at the same price
and nanosecond timestamp would have a non-deterministic order, making tests flaky and replays
non-deterministic.

```go
// From exercises/phase2-orderbook/orderbook.go

// askLess reports whether a should sort before b in the ask list.
// Asks are sorted: price ASC, timestamp ASC, id ASC.
func askLess(a, b orderbook.Order) bool {
    if a.Price != b.Price {
        return a.Price < b.Price
    }
    if !a.Timestamp.Equal(b.Timestamp) {
        return a.Timestamp.Before(b.Timestamp)
    }
    return a.ID < b.ID
}

// bidLess: same but price DESC.
func bidLess(a, b orderbook.Order) bool {
    if a.Price != b.Price {
        return a.Price > b.Price  // ← note: reversed
    }
    if !a.Timestamp.Equal(b.Timestamp) {
        return a.Timestamp.Before(b.Timestamp)
    }
    return a.ID < b.ID
}
```

### Binary insertion

```go
// insertSorted returns s with o inserted at the position dictated by less,
// preserving the sort invariant. Uses sort.Search for O(log n) index find,
// then O(n) shift — optimal for small n with great cache behaviour.
func insertSorted(s []orderbook.Order, o orderbook.Order, less func(a, b orderbook.Order) bool) []orderbook.Order {
    i := sort.Search(len(s), func(j int) bool { return !less(s[j], o) })
    s = append(s, orderbook.Order{})
    copy(s[i+1:], s[i:])
    s[i] = o
    return s
}
```

### The match loop (limit orders)

For an incoming **buy** at price P:

```
while best ask exists AND best_ask.Price <= P:
    qty = min(remaining_buy_qty, best_ask.Quantity)
    trade = Trade{Price: best_ask.Price, Quantity: qty}   // passive price!
    append trade
    reduce/remove best ask
    remaining_buy_qty -= qty
if remaining_buy_qty > 0:
    insert buy order into bids (sorted)
```

The sell side mirrors this exactly with reversed price direction.

```go
// From exercises/phase2-orderbook/orderbook.go

func (ob *OrderBook) matchBuy(o orderbook.Order) []orderbook.Trade {
    var trades []orderbook.Trade
    remaining := o.Quantity
    for remaining > 0 {
        t, ok := ob.tryFillAsk(o, &remaining)
        if !ok {
            break
        }
        trades = append(trades, t)
    }
    if remaining > 0 {
        rest := o
        rest.Quantity = remaining
        ob.InsertAsk(rest) // wait — this is a buy, inserts on bids:
        ob.bids = insertSorted(ob.bids, rest, bidLess)
    }
    return trades
}
```

### Key: price improvement

```go
// tryFillAsk — incoming buy against resting ask
func (ob *OrderBook) tryFillAsk(o orderbook.Order, remaining *int64) (orderbook.Trade, bool) {
    if len(ob.asks) == 0 || ob.asks[0].Price > o.Price {
        return orderbook.Trade{}, false   // no cross
    }
    best := ob.asks[0]
    qty := *remaining
    if best.Quantity < qty {
        qty = best.Quantity
    }
    trade := orderbook.Trade{
        BuyOrderID:  o.ID,
        SellOrderID: best.ID,
        Price:       best.Price,   // ← passive price, not o.Price
        Quantity:    qty,
        Timestamp:   time.Now(),
    }
    ob.consumeHead(&ob.asks, qty)
    *remaining -= qty
    return trade, true
}
```

### Test cases (Phase 2)

```bash
go test ./exercises/phase2-orderbook/... -count=1 -v
```

| Test | What it proves |
|---|---|
| `TestPlaceOrder_LimitMatch` | Basic cross, price improvement |
| `TestPlaceOrder_PartialFill` | Remainder rests on book |
| `TestPlaceOrder_NoMatchWhenPricesDontCross` | Both sides rest |
| `TestPlaceOrder_PriceTimePriority` | FIFO at same price |
| `TestCancelOrder` | Linear scan remove |
| `TestCancelOrder_NotFound` | Sentinel error |
| `TestCancelOrder_DoubleCancelFails` | Idempotency of cancel |
| `TestEmptyOrderBook` | GetBest on empty side = nil |
| `TestGetBest_BasicSequence` | Correct head after inserts |

---

## Phase 3 — The Matching Engine (Market + IOC + Self-match)

> **Goal:** extend the matching logic to handle all three order types and prevent self-matching.

### Three order types

| Type | Price required | Behaviour when nothing crosses |
|---|---|---|
| **Limit** | Yes | Rest on book |
| **Market** | No (`Price = 0`) | Cancel (return `ErrMarketNoLiquidity`) |
| **IOC** (Immediate-or-Cancel) | Yes | Cancel remainder |

```go
// Dispatch based on type — idiomatic Go (no interface needed at this scale)
func (m *Matcher) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error) {
    switch o.Type {
    case orderbook.Limit:
        return m.book.PlaceOrder(o)
    case orderbook.Market:
        return m.placeMarket(o)
    case orderbook.IOC:
        return m.placeIOC(o)
    default:
        return nil, ErrUnsupportedOrderType
    }
}
```

### Market order: sweep at any price

Market orders have no price limit — they consume the entire opposite side until filled or exhausted.

```go
// placeMarket sweeps best-to-worst, skipping self-matches.
func (ob *OrderBook) placeMarket(o Order) ([]Trade, error) {
    var trades []Trade
    var skipped []Order
    remaining := o.Quantity

    for remaining > 0 {
        best := popBest(&ob.asks) // or bids for a sell
        if best == nil {
            break
        }
        if best.UserID == o.UserID {
            skipped = append(skipped, *best) // self-match: skip but keep
            continue
        }
        trade, leftover := fillMarket(*best, &remaining, o.UserID, o.Side)
        trades = append(trades, trade)
        if leftover != nil {
            ob.asks = append([]Order{*leftover}, ob.asks...) // re-insert partial
        }
    }

    // Re-insert skipped same-user orders at front (preserve original position)
    for i := len(skipped) - 1; i >= 0; i-- {
        ob.asks = append([]Order{skipped[i]}, ob.asks...)
    }

    if remaining > 0 {
        return trades, ErrMarketNoLiquidity // partial fill with error
    }
    return trades, nil
}
```

### IOC order: fill then cancel remainder

IOC runs identical limit matching logic but **never inserts the remainder**:

```go
func (ob *OrderBook) placeIOC(o Order) []Trade {
    // Run limit matching, but don't rest remainder
    trades := ob.matchLimitSide(o) // same as limit match loop
    // remainder is discarded — that's the entire IOC contract
    return trades
}
```

### Self-match prevention

Skipping an order that belongs to the same user is done **inline** in the match loop — no separate index
needed at this scale. The skipped orders are collected and re-inserted at their original positions after
the loop to preserve book integrity.

This matters: if Alice has a resting ask at 64¢ and sends a buy at 65¢, allowing the match would let her
trade with herself — booking a profit with no real counterparty.

### Test cases (Phase 3)

```bash
go test ./exercises/phase3-matcher/... -count=1 -v
```

| Test | What it proves |
|---|---|
| `TestMarketOrder_FullFill_OneLevel` | Sweeps one resting order |
| `TestMarketOrder_SweepsMultipleLevels` | Sweeps multiple price levels |
| `TestMarketOrder_PartialFill_BookExhausted` | Partial fill + error |
| `TestMarketOrder_EmptyBook` | `ErrMarketNoLiquidity` immediately |
| `TestIOC_PartialMatch` | Partial fill, no remainder on book |
| `TestIOC_NoMatch` | Nothing crosses → no trades, no error |
| `TestSelfMatchPrevented` | Same-user orders never fill |

---

## Phase 4 — Concurrency (Mutex + Race-free)

> **Goal:** make the order book safe for concurrent goroutines without data races or invariant violations.

### Mutex choice

```go
type OrderBook struct {
    mu   sync.RWMutex  // readers share; writers are exclusive
    bids []Order
    asks []Order
}
```

- `PlaceOrder` and `CancelOrder` take the **write lock** (`Lock`/`Unlock`) — they mutate slices.
- `GetBestBid` and `GetBestAsk` take the **read lock** (`RLock`/`RUnlock`) — read-only.
- `BidCount`/`AskCount` also take the read lock.

**Why start with a full `sync.Mutex` everywhere?** It's simpler to reason about correctness. Upgrade to
`RWMutex` only after measuring that read contention is real (typically >10:1 read:write ratio).

### TOCTOU: the read-check-write trap

This is the most common concurrency bug in order books. Consider `CancelOrder`:

```go
// WRONG — TOCTOU race
func (ob *OrderBook) CancelOrder(id string) error {
    ob.mu.RLock()
    _, found := findByID(ob.bids, id) // read
    ob.mu.RUnlock()
    if !found { return ErrOrderNotFound }

    ob.mu.Lock()
    removeByID(&ob.bids, id) // write (order may be gone now!)
    ob.mu.Unlock()
    return nil
}

// CORRECT — single critical section
func (ob *OrderBook) CancelOrder(id string) error {
    ob.mu.Lock()
    defer ob.mu.Unlock()
    if _, ok := removeByID(&ob.bids, id); ok {
        return nil
    }
    if _, ok := removeByID(&ob.asks, id); ok {
        return nil
    }
    return ErrOrderNotFound
}
```

Between the `RUnlock` and `Lock` in the wrong version, another goroutine could cancel the same order —
resulting in `ErrOrderNotFound` being returned silently while the first cancel succeeded, or a double-
remove that corrupts slice state.

### Copy on read

`GetBestBid` must return a **copy**, not a pointer into the live slice:

```go
func (ob *OrderBook) GetBestBid() *Order {
    ob.mu.RLock()
    defer ob.mu.RUnlock()
    if len(ob.bids) == 0 {
        return nil
    }
    cp := ob.bids[0] // copy the value
    return &cp       // pointer to the copy, not into the slice
}
```

If you returned `&ob.bids[0]`, the caller could mutate the order through the pointer while the write lock
is not held — a silent data race.

### Test cases (Phase 4)

```bash
go test ./exercises/phase4-concurrency/... -race -count=10 -v
```

| Test | What it proves |
|---|---|
| `TestConcurrent_PlaceOrders_NoRace` | 100 goroutines, 1 order each, no race |
| `TestConcurrent_CancelAndPlace` | 50 place / 50 cancel goroutines |
| `TestConcurrent_ReadersAndWriters` | Many `GetBestBid` concurrent with `PlaceOrder` |
| `TestStress_HighVolume` | 1000 orders, 10 goroutines, 100 runs |

The `-race` flag instruments all memory accesses with shadow memory. Any unsynchronized access triggers
`WARNING: DATA RACE` and fails the test. Always run with `-race` during development.

---

## Phase 5 — Market State Machine

> **Goal:** add a lifecycle gate so orders can only be placed while the market is `Live`.

### States and transitions

```
Open ──────► Live ──────► Settled (terminal)
              │               ▲
              ▼               │
            Paused ───────────┘
```

| From \ To | Open | Live | Paused | Settled |
|---|---|---|---|---|
| **Open** | — | ✓ | — | — |
| **Live** | — | — | ✓ | ✓ |
| **Paused** | — | ✓ | — | ✓ |
| **Settled** | — | — | — | — (terminal) |

This is implemented as a **data-driven map** — no switch statement, no hardcoded if-else:

```go
var validTransitions = map[State][]State{
    Open:    {Live},
    Live:    {Paused, Settled},
    Paused:  {Live, Settled},
    Settled: {}, // terminal — nothing is valid
}

func (m *Market) Transition(to State) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    for _, allowed := range validTransitions[m.state] {
        if allowed == to {
            m.state = to
            return nil
        }
    }
    return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, m.state, to)
}
```

Adding a new state requires adding one map entry — no code path changes.

### State-guarded PlaceOrder

```go
func (m *Market) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error) {
    m.mu.RLock()
    state := m.state   // snapshot under read lock
    m.mu.RUnlock()     // release before the (potentially slow) book call
    if state != Live {
        return nil, ErrMarketNotLive
    }
    return m.book.PlaceOrder(o)
}
```

**Why read-lock-snapshot-unlock, then call the book?** Holding `RLock` while calling `book.PlaceOrder`
would be fine (no deadlock), but it would serialise *all* order placements behind the market state read
lock. By releasing the read lock first, state transitions (`Transition`) can acquire the write lock
without waiting for in-flight order placements to finish. State checks are cheap; matching is expensive.

### Test cases (Phase 5)

```bash
go test ./exercises/phase5-state/... -count=1 -v
```

| Test | What it proves |
|---|---|
| `TestTransition_Valid` | All 5 valid transitions succeed |
| `TestTransition_Invalid` | All illegal moves return `ErrInvalidTransition` |
| `TestPlaceOrder_WhenLive` | Order placed successfully |
| `TestPlaceOrder_WhenPaused` | `ErrMarketNotLive` |
| `TestPlaceOrder_WhenSettled` | `ErrMarketNotLive` |
| `TestCancelOrder_WhenSettled` | `ErrMarketNotLive` |
| `TestState_String` | `String()` returns lowercase name |

---

## Phase 6 — Ledger (Balances + Hold, TOCTOU-safe)

> **Goal:** track per-user funds with two-phase commit semantics: reserve on order, settle on trade.

### The two-phase commit pattern

A user places an order to buy 10 lots at 64¢ = 640¢ total cost. The funds must be **reserved** (held)
immediately so they can't be double-spent. When the trade executes, the held funds are **settled**
(transferred). If the order is cancelled, the hold is **released**.

```
PlaceOrder: held[user]     += amount        (reserve)
SettleTrade: held[buyer]   -= amount        (release reservation)
             avail[buyer]  -= amount        (debit buyer)
             avail[seller] += amount        (credit seller)
Refund:      held[user]    -= amount        (release reservation on cancel)
```

### The conservation invariant

```
Σ available + Σ held = constant (non-decreasing, no withdrawals)
```

This invariant must hold at all times, even under 100 concurrent goroutines. The `Total()` method checks
it in tests:

```go
func (l *Ledger) Total() int64 {
    l.mu.Lock()
    defer l.mu.Unlock()
    var total int64
    for _, v := range l.balances { total += v }
    for _, v := range l.held     { total += v }
    return total
}
```

### The TOCTOU trap (and how to avoid it)

```go
// WRONG: check and reserve in separate critical sections
func (l *Ledger) PlaceOrder(userID string, amount int64) error {
    l.mu.RLock()
    free := l.balances[userID] - l.held[userID]
    l.mu.RUnlock()
    if free < amount { return ErrInsufficientFunds } // ← another goroutine can sneak in here

    l.mu.Lock()
    l.held[userID] += amount  // ← now the balance might be gone
    l.mu.Unlock()
    return nil
}

// CORRECT: read-check-write in a single critical section
func (l *Ledger) PlaceOrder(userID string, amount int64) error {
    l.mu.Lock()
    defer l.mu.Unlock()
    free := l.balances[userID] - l.held[userID]
    if free < amount {
        return ErrInsufficientFunds
    }
    l.held[userID] += amount
    return nil
}
```

In the wrong version, two goroutines can both pass the check with `free = 100¢` and `amount = 80¢`,
then both increment `held` — resulting in `held = 160¢ > available = 100¢`, violating the invariant.

### Test cases (Phase 6)

```bash
go test ./exercises/phase6-ledger/... -count=1 -v
```

| Test | What it proves |
|---|---|
| `TestCredit_BalanceIncreases` | Basic credit |
| `TestPlaceOrder_HoldIncreases` | `available` unchanged, `held` up |
| `TestPlaceOrder_InsufficientFunds` | Atomic check + no state change on failure |
| `TestSettleTrade_TransfersCorrectly` | Buyer debited, seller credited |
| `TestRefund_ReleasesHold` | Hold back to zero |
| `TestConservationInvariant_After100Operations` | `Total()` unchanged |
| `TestConcurrent_NoDoubleSpend` | 100 goroutines, only correct count succeed |

---

## Phase 7 — Idempotency Guard

> **Goal:** ensure duplicate HTTP requests (retries) return the cached first response without side effects.

### The problem: network retries

A client sends `POST /orders` with header `Idempotency-Key: abc-123`. The network times out. The client
retries — with the same key. Without a guard, the order gets placed twice. With a guard, the second
request returns the **exact same response** as the first, instantly.

### Design

```go
type Guard struct {
    mu    sync.Mutex
    store map[string]record
}

type record struct {
    response  any
    expiresAt time.Time
}

// Lookup returns (response, true) if key exists and is not expired.
// Lazy expiration: expired entries are deleted on the first failed Lookup.
func (g *Guard) Lookup(key string) (any, bool) {
    g.mu.Lock()
    defer g.mu.Unlock()
    r, ok := g.store[key]
    if !ok { return nil, false }
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
    g.store[key] = record{response: response, expiresAt: time.Now().Add(ttl)}
}
```

**Why `any` (interface{}) for `response`?** The guard is a generic cache — it doesn't know whether the
response is a `PlaceOrderResponse`, a `CancelResponse`, or anything else. The HTTP layer marshals the
cached value to JSON when replaying.

**Why lazy expiration?** A background sweeper is optional. In the common case (few keys), lazy expiration
on `Lookup` keeps the guard simple. A `Sweep()` method is provided for background cleanup via
`time.Ticker`.

### Usage in the HTTP handler

```go
func (h *Handlers) PlaceOrder(w http.ResponseWriter, r *http.Request) {
    key := r.Header.Get("Idempotency-Key")
    if key != "" {
        if cached, ok := h.idempotency.Lookup(key); ok {
            writeJSON(w, http.StatusOK, cached)   // replay
            return
        }
    }

    // ... process the order ...

    resp := PlaceOrderResponse{...}
    if key != "" {
        h.idempotency.Store(key, resp, 5*time.Minute)
    }
    writeJSON(w, http.StatusOK, resp)
}
```

### Test cases (Phase 7)

```bash
go test ./exercises/phase7-idempotency/... -count=1 -v
```

| Test | What it proves |
|---|---|
| `TestLookup_UnseenKey` | `(nil, false)` for unknown key |
| `TestStore_Then_Lookup` | Returns `(response, true)` |
| `TestLookup_AfterExpiry` | Returns `(nil, false)` and deletes entry |
| `TestSweep_RemovesExpired` | Count matches expired entries |
| `TestConcurrent_SameKey_OnlyOneEffectiveWrite` | 100 goroutines, 1 winner |
| `TestDifferentKeys_Independent` | Key A and key B don't collide |

---

## Phase 8 — HTTP API Layer

> **Goal:** wire all components behind a clean HTTP interface using Go 1.22+ path patterns.

### Interface-driven design

The API layer depends on **interfaces**, not concrete types:

```go
// OrderPlacer abstracts order placement (satisfied by *market.Market).
type OrderPlacer interface {
    PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error)
    CancelOrder(id string) error
}

// MarketStater abstracts lifecycle transitions.
type MarketStater interface {
    Transition(to market.State) error
    State() market.State
}

// IdempotencyChecker abstracts the guard.
type IdempotencyChecker interface {
    Lookup(key string) (any, bool)
    Store(key string, response any, ttl time.Duration)
}
```

This means unit tests can inject a mock without spinning up a real `market.Market` or `orderbook.OrderBook`.

### Routes (Go 1.22+ `net/http`)

```go
func (h *Handlers) Routes(mux *http.ServeMux) {
    mux.HandleFunc("POST /orders",          h.PlaceOrder)
    mux.HandleFunc("DELETE /orders/{id}",   h.CancelOrder)
    mux.HandleFunc("GET /healthz",          h.Healthz)
}

// Named path parameters — Go 1.22+
func (h *Handlers) CancelOrder(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")   // extracts the {id} segment
    ...
}
```

Go 1.22 added method-prefixed patterns and `r.PathValue` — no third-party router needed.

### Validation error format

The `orderbook` package returns structured `*ValidationErrors` with per-field errors:

```go
{
  "errors": [
    {"field": "price",    "code": "out_of_range", "message": "price must be 1-100 for limit orders"},
    {"field": "quantity", "code": "must_be_positive", "message": "quantity must be > 0"}
  ]
}
```

This format is intentional: it lets the caller display field-level errors in a form UI without parsing
a human-readable string.

---

## Phase 9 — Event Bus (Pub/Sub)

> **Goal:** notify downstream consumers (analytics, risk systems) of trades without coupling the matching
> engine to them.

### Drop-on-full policy

The event bus uses buffered channels per subscriber. When a subscriber's buffer is full, the event is
**dropped** (non-blocking `select` with a `default` case). This is the correct policy for a trading
system: the hot path (matching) must never stall waiting for a slow subscriber.

```go
// Publish sends e to every subscriber of e.Type. Drops for slow subscribers.
func (eb *EventBus) Publish(e Event) {
    eb.mu.RLock()
    subs := eb.subscribers[e.Type]
    eb.mu.RUnlock()
    for _, ch := range subs {
        select {
        case ch <- e:    // delivered
        default:         // buffer full — drop (non-blocking)
        }
    }
}
```

**Why not block?** If the matching engine blocks on `Publish`, a slow analytics consumer could freeze
the entire order flow. For critical data (trade settlement, ledger updates), use a separate synchronous
path, not the event bus.

### Lifecycle: Close

```go
func (eb *EventBus) Close() {
    eb.mu.Lock()
    defer eb.mu.Unlock()
    for _, subs := range eb.subscribers {
        for _, ch := range subs {
            close(ch)   // signal consumers to exit their range loops
        }
    }
    eb.subscribers = nil
    eb.closed = true
}
```

Closing all subscriber channels lets consumer goroutines `range ch` exit cleanly — no leaked goroutines.

---

## Phase 10 — Integration: Voltron

> **Goal:** assemble all components into a single integrated pipeline and validate the full flow.

The integration test in `test/integration/voltron_test.go` wires all layers together:

```
HTTP (httptest.NewServer) → Idempotency → Validator → Market → OrderBook → Ledger → EventBus
```

```go
type voltron struct {
    market    *market.Market
    ledger    *ledger.Ledger
    eventBus  *events.EventBus
    idempotency *idempotency.Guard
    handlers  *api.Handlers
    server    *httptest.Server
}

func newVoltron() *voltron {
    mkt := market.New()
    mkt.Transition(market.Live)   // start in live state
    bus := events.NewEventBus(100)
    guard := idempotency.NewGuard()
    l := ledger.NewLedger()
    h := &api.Handlers{
        Market:      mkt,
        Ledger:      l,
        EventBus:    bus,
        Idempotency: guard,
    }
    mux := http.NewServeMux()
    h.Routes(mux)
    return &voltron{
        market: mkt, ledger: l, eventBus: bus,
        idempotency: guard, handlers: h,
        server: httptest.NewServer(mux),
    }
}
```

### Integration test cases

| Test | What it proves |
|---|---|
| `TestVoltron_PlaceOrder_FullFlow` | HTTP → matching → 200 response |
| `TestVoltron_IdempotentPlacement` | Same key → same `order_id` |
| `TestVoltron_CancelOrder` | Place then DELETE → 200 → second DELETE → 404 |
| `TestVoltron_MatchingFlow` | Two orders cross → trade in response |
| `TestVoltron_EventBusReceivesEvents` | Trade event delivered to subscriber |

---

## Concurrency Model Summary

| Component | Lock type | Reason |
|---|---|---|
| `OrderBook` | `sync.RWMutex` | Many concurrent reads (`GetBest*`), exclusive writes |
| `Market` | `sync.RWMutex` | State reads are cheap and frequent; transitions are rare |
| `Ledger` | `sync.Mutex` | All ops are read-check-write; `RWMutex` gains nothing |
| `IdempotencyGuard` | `sync.Mutex` | All ops are read-check-write |
| `EventBus` | `sync.RWMutex` | Subscribe/Unsubscribe write; Publish reads subscriber list |

**General rule:** start with `sync.Mutex` for correctness. Upgrade to `sync.RWMutex` only when you have
a measured read-dominated workload (>10:1 read:write). Premature `RWMutex` usage adds complexity without
benefit and can introduce subtle bugs (upgrading a read lock requires re-acquiring as write, which is not
atomic in Go's `sync.RWMutex`).

---

## Running the Project

### Prerequisites

- Go 1.22+ (uses `net/http` named path patterns and `log/slog`)
- `curl` and `python3` for smoke tests

### Build and test

```bash
make build       # compile all packages
make test        # unit + integration tests
make race        # -race -count=100 stress test
make vet         # go vet
make lint        # golangci-lint (requires golangci-lint installed)
make run         # start the server on :8080
make smoke       # build + start + API smoke test + stop
make clean       # remove build artefacts
```

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `READ_TIMEOUT` | `10s` | `http.Server` read timeout |
| `WRITE_TIMEOUT` | `10s` | `http.Server` write timeout |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown window |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `LOG_FORMAT` | `json` | `json` or `text` |

### Smoke test (curl-based)

```bash
# Terminal 1
make run

# Terminal 2
./hack/smoke.sh --test-only
```

Output:
```
-- Health --
  ✓ health / ok
-- Place & match --
  ✓ order_id present
  ✓ trade price / 64
  ✓ trade qty / 10
-- Market order --
  ✓ market / 60
-- IOC --
  ✓ no trades (empty list)
-- Validation --
  ✓ empty user_id / 400
  ✓ zero quantity / 400
  ✓ invalid side / 400
  ✓ market w/price / 400
-- Cancel --
  ✓ delete ok / 200
  ✓ not found / 404
-- Idempotency --
  ✓ same order_id replayed
========================================
  8 passed, 0 failed
========================================
```

---

## API Reference

Full OpenAPI 3.1 spec: [`api/openapi.yaml`](api/openapi.yaml)

### `POST /orders` — Place an order

**Header:** `Idempotency-Key: <uuid>` (optional — enables replay on retry)

**Request body:**

```json
{
  "user_id":  "alice",
  "side":     "buy",
  "type":     "limit",
  "price":    64,
  "quantity": 10
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `user_id` | string | always | Non-empty |
| `side` | `"buy"` or `"sell"` | always | |
| `type` | `"limit"`, `"market"`, or `"ioc"` | always | |
| `price` | int (cents, 1–100) | limit/ioc | 0 or omit for market |
| `quantity` | int (lots, ≥1) | always | |

**Response 200:**

```json
{
  "order_id": "160052-1",
  "trades": [
    {"buy_order_id": "160052-2", "sell_order_id": "160052-1", "price": 64, "quantity": 10}
  ],
  "remaining_qty": 0
}
```

**Response 400** (validation error):

```json
{"error": "price: out_of_range — price must be 1-100 for limit orders"}
```

### `DELETE /orders/{id}` — Cancel a resting order

**Response 200:** `{"status": "cancelled"}`
**Response 404:** `{"error": "order not found"}`

### `GET /healthz` — Health check

**Response 200:** `{"status": "ok"}`

---

## Practice Exercises

The [`exercises/`](exercises/) folder contains 6 TDD exercises — one per phase. Each exercise has:

- A **stub** file (compiles but panics — implement the methods)
- Pre-written **acceptance tests** (make them pass)
- A **README** explaining the goal, hints, and common pitfalls

```
exercises/
  phase2-orderbook/    # Sorted slice order book, limit matching
  phase3-matcher/      # Market + IOC + self-match prevention
  phase4-concurrency/  # Mutex, RWMutex, TOCTOU, race detector
  phase5-state/        # State machine (Open/Live/Paused/Settled)
  phase6-ledger/       # Two-phase commit, conservation invariant
  phase7-idempotency/  # TTL cache, lazy expiration, Sweep
```

### Workflow

```bash
# 1. Read the phase README
cat exercises/phase2-orderbook/README.md

# 2. Implement the stub
$EDITOR exercises/phase2-orderbook/orderbook.go

# 3. Run the acceptance tests
go test ./exercises/phase2-orderbook/... -count=1 -v

# 4. For Phase 4+, run with the race detector
go test ./exercises/phase4-concurrency/... -race -count=10 -v

# 5. When green, commit
git add exercises/phase2-orderbook
git commit -m "feat(exercises): phase 2 — order book"
```

### Phase map

| # | Phase | Key concept | Key pitfall |
|---|---|---|---|
| 2 | Order book (sorted slice) | `sort.Search` + binary insertion | Wrong sort key; missing ID tiebreaker |
| 3 | Matching engine | Market sweep; IOC no-rest; self-match skip | Re-inserting skipped orders |
| 4 | Concurrency | `sync.RWMutex`; read-lock snapshot | Pointer into slice; split lock sections |
| 5 | State machine | Data-driven transition map; terminal state | TOCTOU on state+book |
| 6 | Ledger | Two-phase commit; conservation invariant | Check-then-act in two separate locks |
| 7 | Idempotency | TTL cache; lazy deletion; `Sweep` | Expiry check in wrong direction |

---

## Project Layout

```
go-order-book/
├── api/
│   └── openapi.yaml              OpenAPI 3.1 spec
├── cmd/
│   └── server/
│       └── main.go               Entry point; wires all components
├── exercises/
│   ├── phase2-orderbook/         TDD exercise: sorted slice book
│   ├── phase3-matcher/           TDD exercise: market + IOC + self-match
│   ├── phase4-concurrency/       TDD exercise: mutex + race detector
│   ├── phase5-state/             TDD exercise: state machine
│   ├── phase6-ledger/            TDD exercise: balances + hold
│   └── phase7-idempotency/       TDD exercise: TTL cache
├── hack/
│   └── smoke.sh                  curl-based API smoke test
├── internal/
│   ├── api/                      HTTP handlers (Go 1.22+ path patterns)
│   ├── config/                   Env-driven config with defaults
│   ├── events/                   In-memory pub/sub event bus
│   ├── idempotency/              TTL-backed idempotency guard
│   ├── ledger/                   Per-user balances + two-phase hold
│   ├── market/                   State machine guarding the order book
│   └── orderbook/                Order, Trade, OrderBook, Matcher, Validator
├── test/
│   └── integration/
│       └── voltron_test.go       Full end-to-end integration test
├── Makefile
└── go.mod
```

---

## Key Design Decisions

### 1. Integer prices, never `float64`

All prices are `int64` cents. `float64` arithmetic is lossy: `0.1 + 0.2 != 0.3` in IEEE 754. For a
financial system, every cent must be exact. Using integers also makes equality checks, comparisons, and
the conservation invariant trivially correct.

### 2. Passive-price execution

Trades execute at the **resting** (passive) order's price, not the aggressor's. This gives the aggressor
*price improvement*: a buy at 65¢ that crosses a 64¢ ask executes at 64¢. This is the standard
convention for continuous order-driven markets.

### 3. Value types for `Order` and `Trade`

`Order` and `Trade` are `struct` values, not pointers. This means:
- No heap allocation per order in the fast path
- Mutations to `Order` inside the book don't alias with the caller's copy
- Slice elements are contiguous in memory (cache-friendly)

The book's slices hold value types, not pointers — this is the primary reason the sorted slice beats a
linked list in practice.

### 4. Interfaces at the API boundary only

Interfaces are defined at the HTTP handler boundary (`OrderPlacer`, `MarketStater`,
`IdempotencyChecker`) — not inside the core matching engine. The core uses concrete types for speed and
clarity. Interfaces at the boundary enable testing without spinning up the full stack.

### 5. `log/slog` for structured logging

Go 1.21+ `log/slog` provides levelled, structured (JSON or text) logging with zero-allocation hot paths.
It replaces `fmt.Printf` in production without any third-party dependency.
