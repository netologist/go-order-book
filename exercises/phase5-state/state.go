// Package phase5 is your practice target for Phase 5: a market state
// machine that guards the order book. PlaceOrder/CancelOrder are
// state-aware; Transition enforces the lifecycle.
package phase5

import (
	"errors"
	"sync"

	"github.com/netologist/go-order-book/exercises/phase4-concurrency"
	"github.com/netologist/go-order-book/internal/orderbook"
)

// MarketState is the lifecycle stage of a market.
type MarketState int

const (
	Open MarketState = iota
	Live
	Paused
	Settled
)

// String implements fmt.Stringer.
func (s MarketState) String() string {
	switch s {
	case Open:
		return "open"
	case Live:
		return "live"
	case Paused:
		return "paused"
	case Settled:
		return "settled"
	}
	return "unknown"
}

// validTransitions is the data-driven transition table.
//
//	Open    → [Live]
//	Live    → [Paused, Settled]
//	Paused  → [Live, Settled]
//	Settled → []  (terminal)
var validTransitions = map[MarketState][]MarketState{
	Open:    {Live},
	Live:    {Paused, Settled},
	Paused:  {Live, Settled},
	Settled: {},
}

// ErrInvalidTransition is returned by Transition for any illegal move.
var ErrInvalidTransition = errors.New("invalid state transition")

// ErrMarketNotLive is returned by PlaceOrder/CancelOrder when the state
// doesn't allow the operation.
var ErrMarketNotLive = errors.New("market is not in live state")

// Market owns the order book and the lifecycle state.
type Market struct {
	mu    sync.RWMutex
	state MarketState
	book  *phase4.OrderBook
}

// NewMarket creates a market in the Open state with an empty book.
func NewMarket() *Market {
	return &Market{book: &phase4.OrderBook{}}
}

// Transition moves the market to `to` if the move is valid per
// validTransitions. Returns ErrInvalidTransition otherwise.
func (m *Market) Transition(to MarketState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, allowed := range validTransitions[m.state] {
		if allowed == to {
			m.state = to
			return nil
		}
	}
	return ErrInvalidTransition
}

// PlaceOrder delegates to the book only if state == Live. Otherwise returns
// ErrMarketNotLive without touching the book.
func (m *Market) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error) {
	m.mu.RLock()
	state := m.state
	m.mu.RUnlock()

	if state != Live {
		return nil, ErrMarketNotLive
	}
	return m.book.PlaceOrder(o)
}

// CancelOrder delegates to the book unless state == Settled. (Live, Paused
// and Open allow cancel — Paused keeps cancels open so users can exit.)
func (m *Market) CancelOrder(id string) error {
	m.mu.RLock()
	state := m.state
	m.mu.RUnlock()

	if state == Settled {
		return ErrMarketNotLive
	}
	return m.book.CancelOrder(id)
}

// State returns the current state. Safe for concurrent callers.
func (m *Market) State() MarketState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}
