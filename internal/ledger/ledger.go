// Package ledger tracks per-user available balance and held (reserved) funds
// for the order-book system. All operations are thread-safe.
package ledger

import (
	"errors"
	"sync"
)

// ErrInsufficientFunds is returned when a user tries to reserve more than
// their free (available - held) balance.
var ErrInsufficientFunds = errors.New("insufficient funds")

// Ledger tracks per-user available balance and held (reserved) funds.
//
// Invariants (held at all times under the lock):
//   - balances[user] >= 0
//   - held[user]     >= 0
//   - balances[user] - held[user] >= 0  (free balance)
//   - Σ balances + Σ held is non-decreasing (no external withdrawals)
type Ledger struct {
	mu       sync.Mutex
	balances map[string]int64
	held     map[string]int64
}

// NewLedger returns a ready-to-use ledger.
func NewLedger() *Ledger {
	return &Ledger{
		balances: make(map[string]int64),
		held:     make(map[string]int64),
	}
}

// Credit adds amount to the user's available balance, creating the user
// if absent. Amount must be > 0.
func (l *Ledger) Credit(userID string, amount int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balances[userID] += amount
}

// PlaceOrder reserves amount from the user's free balance (available - held).
// The check-and-reserve happens atomically inside a single critical section
// to prevent TOCTOU races.
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

// SettleTrade transfers amount from buyer to seller. Decrements held[buyer]
// and available[buyer] by amount, increments available[seller].
// Caller must ensure held[buyer] >= amount.
func (l *Ledger) SettleTrade(buyer, seller string, amount int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.held[buyer] -= amount
	l.balances[buyer] -= amount
	l.balances[seller] += amount
}

// Refund releases amount from the user's held balance (used when an order
// is cancelled before filling). held[user] -= amount.
func (l *Ledger) Refund(userID string, amount int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held[userID] -= amount
}

// Balance returns the user's available and held amounts. ok is false if
// the user has no entry in either map.
func (l *Ledger) Balance(userID string) (available, held int64, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	avail, hasBal := l.balances[userID]
	h, hasHeld := l.held[userID]
	if !hasBal && !hasHeld {
		return 0, 0, false
	}
	return avail, h, true
}

// Total returns the sum of all available + all held. Used for conservation
// invariant checks in tests.
func (l *Ledger) Total() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	var total int64
	for _, v := range l.balances {
		total += v
	}
	for _, v := range l.held {
		total += v
	}
	return total
}
