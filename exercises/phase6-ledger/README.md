# Phase 6 — Ledger (Balances + Hold, TOCTOU-safe)

## Goal

Track user funds. Each user has an `available` balance and a `held` amount
(funds reserved for an open order). The contract:

```
available >= 0  AND  held >= 0
Σ available + Σ held = constant   (the conservation invariant)
```

Three operations:

| Op | What it does |
|---|---|
| `PlaceOrder` (reserve) | `available` unchanged, `held += amount`, fail if `available - held < amount` |
| `SettleTrade` | `held[buyer] -= amount; available[buyer] -= amount; available[seller] += amount` |
| `Refund` (cancel) | `held -= amount` |

The **TOCTOU trap**: read `available`, check, write — must be inside one
critical section. Two concurrent `PlaceOrder` calls each passing the check
must not both succeed if their sum exceeds the balance.

## What to implement

In `ledger.go`:

```go
type Ledger struct {
    mu       sync.Mutex
    balances map[string]int64  // available
    held     map[string]int64  // reserved
}

func (l *Ledger) Credit(userID string, amount int64)        // deposit
func (l *Ledger) PlaceOrder(userID string, amount int64) error  // reserve
func (l *Ledger) SettleTrade(buyer, seller string, amount int64)
func (l *Ledger) Refund(userID string, amount int64)         // cancel
func (l *Ledger) Balance(userID string) (available, held int64, ok bool)
func (l *Ledger) Total() (total int64)                      // Σ available + Σ held
```

## Hints

### TOCTOU is the entire point of this phase

```go
func (l *Ledger) PlaceOrder(userID string, amount int64) error {
    l.mu.Lock()
    defer l.mu.Unlock()
    avail := l.balances[userID]
    held := l.held[userID]
    if avail - held < amount {
        return ErrInsufficientFunds
    }
    l.held[userID] += amount
    return nil
}
```

The read (`avail - held`) and the write (`held += amount`) are inside the
same `Lock`/`Unlock` pair. That is the whole defense.

### The conservation invariant

```go
func (l *Ledger) Total() int64 {
    l.mu.Lock()
    defer l.mu.Unlock()
    var total int64
    for _, v := range l.balances { total += v }
    for _, v := range l.held { total += v }
    return total
}
```

`Total()` must equal the sum of all `Credit` calls minus any
"withdraw-style" operations (we don't have a withdraw here, so Total is
non-decreasing). After 100 goroutines all `PlaceOrder`ing 1 lot at 50¢,
Total must still equal the initial seed.

### Settle order matters

`SettleTrade(buyer, seller, amount)` should:

1. `held[buyer] -= amount`
2. `available[buyer] -= amount`
3. `available[seller] += amount`

If the buyer doesn't have enough `held`, the call would underflow. For
Phase 6, trust the caller and document the precondition. Production
should `panic` or return an error if `held[buyer] < amount`.

### Auto-create users

In `Credit`, if the user doesn't exist, create them with `0` balance, then
add. In `Balance`, return `ok=false` for unknown users (or auto-create
with 0 — your call, but the tests will tell you).

## Tests to pass

```bash

    test ./exercises/phase6-ledger/... -count=1 -v
```

1. `TestCredit_BalanceIncreases`
2. `TestPlaceOrder_HoldIncreases` — `available` unchanged, `held` up
3. `TestPlaceOrder_InsufficientFunds` — `ErrInsufficientFunds`, no state change
4. `TestSettleTrade_TransfersCorrectly` — buyer paid, seller credited
5. `TestRefund_ReleasesHold` — `held` back down, `available` untouched
6. `TestConservationInvariant_After100Operations` — Total unchanged
7. `TestConcurrent_NoDoubleSpend` — 100 goroutines, only the right number succeed

## Study guide reference

- §7.1 Double-spend prevention
- §7.2 TOCTOU
- §12.1 Schema (`balances`, `ledger_entries`)
- §12.2 Critical queries (Q1, Q4 — note the `FOR UPDATE` pattern)

## Extension (optional)

Add a `Withdraw(userID, amount)` that debits `available` and reduces
Total. Verify the conservation invariant still holds when you mix credits,
holds, settles, refunds, and withdrawals.
