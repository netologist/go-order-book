# Phase 5 — Market State Machine

## Goal

Implement the **market lifecycle**: `Open → Live → Paused/Settled`. A
market in `Paused` state rejects new orders but accepts cancels. A
market in `Settled` state rejects everything (terminal state).

The state machine **guards** the order book. PlaceOrder checks state
*first*; if the state forbids it, the order book is never touched.

## What to implement

In `state.go`:

```go
type MarketState int
const (
    Open MarketState = iota
    Live
    Paused
    Settled
)

type Market struct {
    mu    sync.RWMutex
    state MarketState
    book  *phase4.OrderBook   // reuse the thread-safe book
}

func (m *Market) Transition(to MarketState) error
func (m *Market) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error)
func (m *Market) CancelOrder(id string) error
func (m *Market) State() MarketState
```

> Note: importing `phase4` for the book. Phase 5 is the first phase that
> uses the **thread-safe** book from Phase 4.

## Hints

### Valid transitions

Data-driven is the cleanest:

```go
var validTransitions = map[MarketState][]MarketState{
    Open:    {Live},
    Live:    {Paused, Settled},
    Paused:  {Live, Settled},
    Settled: {}, // terminal
}
```

`Transition` walks the slice, sets state if `to` is in it, returns an
error otherwise.

### State-guarded operations

```go
func (m *Market) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error) {
    m.mu.RLock()
    state := m.state
    m.mu.RUnlock()
    if state != Live {
        return nil, fmt.Errorf("market is %s, cannot place order", state)
    }
    return m.book.PlaceOrder(o)
}
```

The read is fast (RLock) and releases before the (potentially slow) book
call. This way a paused market doesn't block readers.

### State strings

`func (s MarketState) String() string` — same pattern as `Side.String()`.
Useful for `slog`, errors, and HTTP responses.

### Errors

```go
var ErrInvalidTransition = errors.New("invalid state transition")
var ErrMarketNotLive     = errors.New("market is not in live state")
```

## Tests to pass

```bash

    test ./exercises/phase5-state/... -count=1 -v
```

1. `TestTransition_Valid` — Open→Live, Live→Paused, Live→Settled, Paused→Live, Paused→Settled
2. `TestTransition_Invalid` — Open→Paused, Open→Settled, Paused→Open, Settled→* (all errors)
3. `TestPlaceOrder_WhenLive` — succeeds
4. `TestPlaceOrder_WhenPaused` — `ErrMarketNotLive`
5. `TestPlaceOrder_WhenSettled` — `ErrMarketNotLive`
6. `TestPlaceOrder_WhenOpen` — `ErrMarketNotLive` (only Live accepts orders)
7. `TestCancelOrder_WhenSettled` — `ErrMarketNotLive`
8. `TestState_String` — Open/Live/Paused/Settled → "open"/"live"/"paused"/"settled"

## Study guide reference

- §1.3 Market lifecycle
- §8 State machine: enum + valid transitions map
- §8.2 State-guarded operations
- §12.2 Sequence diagram (note the AssertState call)

## Common pitfalls

- ❌ Checking state and then calling the book without holding any lock
  → TOCTOU. Use the read-locked snapshot pattern above.
- ❌ Forgetting to make `Settled` a terminal state. After settling, the
  market is dead forever.
- ❌ Allowing `Open → Open` (no-op transitions). Be strict.
