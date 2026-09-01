// Package market implements the market lifecycle state machine and
// orchestrates the order flow: validate → state gate → OrderBook.
package market

import (
	"errors"
	"fmt"
	"sync"

	"github.com/netologist/go-order-book/internal/orderbook"
)

// State is the lifecycle stage of a market.
type State int

const (
	Open State = iota
	Live
	Paused
	Settled
)

// String implements fmt.Stringer.
func (s State) String() string {
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
	return fmt.Sprintf("unknown(%d)", int(s))
}

// validTransitions is the data-driven transition table.
//
//	Open    → [Live]
//	Live    → [Paused, Settled]
//	Paused  → [Live, Settled]
//	Settled → []  (terminal)
var validTransitions = map[State][]State{
	Open:    {Live},
	Live:    {Paused, Settled},
	Paused:  {Live, Settled},
	Settled: {},
}

// ErrInvalidTransition is returned by Transition for any illegal move.
var ErrInvalidTransition = errors.New("invalid state transition")

// ErrMarketNotLive is returned by PlaceOrder when the market isn't Live.
var ErrMarketNotLive = errors.New("market is not in live state")

// Market wraps an OrderBook with a lifecycle state machine.
// PlaceOrder and CancelOrder are gated by the current state.
type Market struct {
	mu    sync.RWMutex
	state State
	book  *orderbook.OrderBook
}

// New creates a market in the Open state with an empty book.
func New() *Market {
	return &Market{
		book: &orderbook.OrderBook{},
	}
}

// Transition moves the market to `to` if the move is valid per
// validTransitions. Returns ErrInvalidTransition otherwise.
func (m *Market) Transition(to State) error {
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

// PlaceOrder delegates to the book only if state == Live.
func (m *Market) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error) {
	m.mu.RLock()
	st := m.state
	m.mu.RUnlock()

	if st != Live {
		return nil, ErrMarketNotLive
	}
	return m.book.PlaceOrder(o)
}

// CancelOrder delegates to the book unless state == Settled.
// Open, Live, and Paused all allow cancel.
func (m *Market) CancelOrder(id string) error {
	m.mu.RLock()
	st := m.state
	m.mu.RUnlock()

	if st == Settled {
		return ErrMarketNotLive
	}
	return m.book.CancelOrder(id)
}

// State returns the current state. Safe for concurrent callers.
func (m *Market) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}
