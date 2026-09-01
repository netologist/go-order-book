# Phase 3 — Matching Engine (Market + IOC + Self-match)

## Goal

Refactor the matcher to handle **three order types** and prevent **self-match**.

- **Limit** — already done in Phase 2.
- **Market** — fill at the best available price(s) until filled or book empty.
- **IOC** — like limit, but **don't rest** the unfilled portion.

A new incoming order is **never** matched against another order from the same
user. If the only opposite orders are from the same user, the order must rest
(or be cancelled, in the IOC case).

## What to implement

In `matcher.go`:

```go
type Matcher struct {
    book *phase2.OrderBook
}

func NewMatcher(book *phase2.OrderBook) *Matcher
func (m *Matcher) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error)
```

You are **decoupling the matching strategy from the book**. The book is
now a passive data structure (Phase 2 done). The matcher decides what to
do for each order type.

> Note: `phase2.OrderBook` is the same struct you built in Phase 2. Import
> `exercises/phase2-orderbook` and reuse it.

## Hints

### Market order

No price limit. Walk the opposite side from best to worst, fill as much as
you can. If the book is empty, the order is **cancelled** (with no error —
market orders against empty books are normal in illiquid markets, but you
may also return a sentinel `ErrMarketNoLiquidity` if you prefer; the tests
below expect an error in that case).

### IOC order

Run the limit match logic but **don't insert** the remainder. Just return
the trades you got.

### Self-match prevention

When the incoming order's `UserID` matches the resting order's `UserID`,
**skip that resting order** during matching. The resting order stays on
the book. If after skipping all self-orders there's nothing left, the
incoming limit order rests, and the incoming IOC/market order is cancelled
with no trades.

A reasonable implementation: maintain a `userID` index, or just compare
inline during the match loop. Inline is fine for Phase 3.

### Strategy pattern

You don't need a fancy interface — a `switch o.Type` inside `PlaceOrder`
is the idiomatic Go answer for "three behaviors, one entry point." Save
the strategy-pattern interface for Phase 11 if you want to play with it.

### Errors you'll need to define

```go
var ErrMarketNoLiquidity = errors.New("market order found no liquidity")
```

(Market order against an empty book, or after skipping all self-matches.)

## Tests to pass

```bash

    test ./exercises/phase3-matcher/... -count=1 -v
```

1. `TestMarketOrder_FullFill_OneLevel`
2. `TestMarketOrder_SweepsMultipleLevels`
3. `TestMarketOrder_PartialFill_BookExhausted` — partial fill, error returned
4. `TestMarketOrder_EmptyBook` — `ErrMarketNoLiquidity`
5. `TestIOC_PartialMatch` — partial fill, no remainder on book
6. `TestIOC_NoMatch` — order cancelled, no error
7. `TestSelfMatchPrevented` — both orders rest, no trade

## Study guide reference

- §3 Matching algorithm
- §4.2 Market order (sweep, weighted average)
- §4.3 IOC (immediate-or-cancel)
- §3.4 Edge case: self-match prevention

## Extension (optional)

If you finish early: handle the "weighted average fill price" of a market
order sweep and return it as `trades[len-1].Price` of a synthetic final
trade (or attach it to a `MatchSummary` returned alongside trades). Not
required — just a thought experiment on how an exchange reports fills.
