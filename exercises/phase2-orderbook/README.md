# Phase 2 — Order Book (sorted slice, single-threaded)

## Goal

Build an in-memory `OrderBook` that holds resting limit orders and matches
incoming limit orders against the opposite side using **price-time priority**.

> **Scope of this phase**: LIMIT orders only. Market orders and IOC come in
> Phase 3. Validation is already done in `internal/orderbook/validate.go`
> — you don't need to re-validate inputs here. Trust what you get.
> Concurrency comes in Phase 4 — for now, single-threaded is fine.

## What to implement

In `orderbook.go` (the stub):

```go
type OrderBook struct {
    mu   sync.Mutex                  // unused in Phase 2, but reserve it
    bids []orderbook.Order           // sorted: price DESC, timestamp ASC, id ASC
    asks []orderbook.Order           // sorted: price ASC,  timestamp ASC, id ASC
}

func (ob *OrderBook) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error)
func (ob *OrderBook) CancelOrder(id string) error
func (ob *OrderBook) GetBestBid() *orderbook.Order
func (ob *OrderBook) GetBestAsk() *orderbook.Order
```

## Hints

### Sort key

Both sides need a strict total order — same price, same timestamp must still
have a deterministic order. Think about what your third tiebreaker is.

### Match loop

For an incoming BUY:

1. While the best ask exists **and** its price ≤ your price, fill.
2. Trade price = **passive (resting) order's price**, not yours. Price
   improvement goes to the aggressor.
3. `min(incoming.Remaining, resting.Remaining)` per trade.
4. If the resting order is fully consumed, remove it from the slice.

For an incoming SELL, mirror the logic.

### Insertion

After the match loop, if `o.Remaining > 0` (which for limit orders means
quantity > matched), insert into the correct side at the right position.

### Cancellation

Linear scan by ID. The book is small. If you find it, remove it from the
slice **and** return `nil`. If you don't, return a sentinel error
(you'll need to define one — see below).

### Errors you'll need to define

In the `phase2` package:

```go
var ErrOrderNotFound = errors.New("order not found")
```

This is a package-level sentinel. Tests will use `errors.Is`.

### What "remaining" means for a trade

A `Trade` records `Quantity` — the number of lots that crossed. Both
orders lose that much from their remaining. You don't need to mutate the
input `Order` value (it's a value type in Go). The book is the source of
truth for what each resting order still has.

## Tests to pass

Run:

```bash

    test ./exercises/phase2-orderbook/... -count=1 -v
```

Required cases (from study guide §11.1):

1. `TestPlaceOrder_LimitMatch` — basic cross
2. `TestPlaceOrder_PartialFill` — partial fill leaves remainder on book
3. `TestPlaceOrder_NoMatchWhenPricesDontCross` — both rest
4. `TestPlaceOrder_PriceTimePriority` — FIFO at same price
5. `TestCancelOrder`
6. `TestCancelOrder_NotFound`
7. `TestCancelOrder_DoubleCancelFails`
8. `TestEmptyOrderBook`
9. `TestGetBest_BasicSequence`

## Study guide reference

- §2 Order book anatomy
- §3 Matching engine algorithm
- §5.1 Sorted slice trade-offs
- §11.1 Test cases
- §5.3 Lazy deletion (NOT needed yet — Phase 2 is single-threaded)

## Extension (optional)

If you finish early, also handle: a resting order that gets partially
filled by *two* incoming orders in sequence. Verify the remaining qty is
correct after both fills. No new test required — just think about it.
