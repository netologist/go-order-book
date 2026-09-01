// Package matcher is an INTERVIEW SIMULATION.
//
// This is the BROKEN code the interviewer might present.
//
// Run the tests with -race to see failures:
//
//	go test -race -v ./exercises/matcher/...
//
// Your task: find and fix all bugs, one at a time, explaining each in English.
//
// Bugs present:
//  1. No mutex on orders/balances → data race (CRITICAL)
//  2. Slice mutation during iteration → index skips elements
//  3. Self-matching → same trader buys from themselves
//  4. No idempotency → same order ID double-executes
//  5. Float64 for money → rounding errors
//  6. Balance check AFTER matching → corrupted state on insufficient funds
//  7. Seller balance update broken → searches removed order
package matcher

import (
	"errors"
	"sort"
)

// Side represents buy or sell.
type Side int

const (
	Buy Side = iota
	Sell
)

// Order represents a trader's intent to buy or sell.
type Order struct {
	ID       string
	TraderID string
	Side     Side
	Price    float64
	Quantity int64
}

// Trade records a match between two orders.
type Trade struct {
	BuyOrderID  string
	SellOrderID string
	Price       float64
	Quantity    int64
}

// Matcher holds the orders and trader balances.
// It matches buy orders against sell orders at the same or better price.
type Matcher struct {
	orders   []Order
	balances map[string]int64
}

// NewMatcher creates an empty matcher.
func NewMatcher() *Matcher {
	return &Matcher{
		balances: make(map[string]int64),
	}
}

// SetBalance initialises a trader's balance.
func (m *Matcher) SetBalance(traderID string, amount int64) {
	m.balances[traderID] = amount
}

// GetBalance returns the trader's current balance.
func (m *Matcher) GetBalance(traderID string) int64 {
	return m.balances[traderID]
}

// PlaceOrder attempts to match an incoming order against the book.
func (m *Matcher) PlaceOrder(o Order) ([]Trade, error) {
	if o.ID == "" {
		return nil, errors.New("order ID is required")
	}
	if o.Quantity <= 0 {
		return nil, errors.New("quantity must be positive")
	}

	var trades []Trade

	switch o.Side {
	case Buy:
		for i := range m.orders {
			resting := &m.orders[i]
			if resting.Side != Sell {
				continue
			}
			if resting.Price > o.Price {
				continue
			}
			qty := o.Quantity
			if resting.Quantity < qty {
				qty = resting.Quantity
			}
			t := Trade{
				BuyOrderID:  o.ID,
				SellOrderID: resting.ID,
				Price:       resting.Price,
				Quantity:    qty,
			}
			trades = append(trades, t)
			o.Quantity -= qty
			resting.Quantity -= qty
			if resting.Quantity == 0 {
				m.orders = append(m.orders[:i], m.orders[i+1:]...)
			}
			if o.Quantity == 0 {
				break
			}
		}

		// Balance check AFTER matching — corrupted state on fail!
		cost := int64(0)
		for _, t := range trades {
			cost += int64(t.Price * float64(t.Quantity))
		}
		if m.balances[o.TraderID] < cost {
			return nil, errors.New("insufficient balance")
		}
		for _, t := range trades {
			m.balances[o.TraderID] -= int64(t.Price * float64(t.Quantity))
			// Broken: searches m.orders for seller — order already removed!
			for _, resting := range m.orders {
				if resting.ID == t.SellOrderID {
					m.balances[resting.TraderID] += int64(t.Price * float64(t.Quantity))
					break
				}
			}
		}

	case Sell:
		for i := range m.orders {
			resting := &m.orders[i]
			if resting.Side != Buy {
				continue
			}
			if resting.Price < o.Price {
				continue
			}
			qty := o.Quantity
			if resting.Quantity < qty {
				qty = resting.Quantity
			}
			t := Trade{
				BuyOrderID:  resting.ID,
				SellOrderID: o.ID,
				Price:       resting.Price,
				Quantity:    qty,
			}
			trades = append(trades, t)
			o.Quantity -= qty
			resting.Quantity -= qty
			if resting.Quantity == 0 {
				m.orders = append(m.orders[:i], m.orders[i+1:]...)
			}
			if o.Quantity == 0 {
				break
			}
		}
	}

	if o.Quantity > 0 {
		m.orders = append(m.orders, o)
	}

	return trades, nil
}

// GetOrders returns all resting orders (unsorted).
func (m *Matcher) GetOrders() []Order {
	return m.orders
}

// SortOrders sorts by side and price.
func (m *Matcher) SortOrders() {
	sort.Slice(m.orders, func(i, j int) bool {
		return m.orders[i].Price > m.orders[j].Price
	})
}
