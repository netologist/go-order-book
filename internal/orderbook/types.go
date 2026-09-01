// Package orderbook implements the core trading primitives: orders, trades,
// the in-memory book, and the matching engine.
//
// This file defines the value types. Validation lives in validate.go.
package orderbook

import "time"

// Side is the direction of an order. Stored as int for hot-path speed;
// the Stringer method keeps it readable in logs.
type Side int

const (
	Buy Side = iota
	Sell
)

// String implements fmt.Stringer for logs and JSON output.
func (s Side) String() string {
	switch s {
	case Buy:
		return "buy"
	case Sell:
		return "sell"
	default:
		return "unknown"
	}
}

// OrderType selects the matching strategy.
//
// Price is 0 for Market orders (sentinel — no price limit). Limit and IOC
// orders must have a positive price in [1, 100] cents.
type OrderType int

const (
	Limit OrderType = iota
	Market
	IOC // Immediate-or-Cancel
)

// String implements fmt.Stringer.
func (t OrderType) String() string {
	switch t {
	case Limit:
		return "limit"
	case Market:
		return "market"
	case IOC:
		return "ioc"
	default:
		return "unknown"
	}
}

// Order is a single resting or incoming instruction to buy/sell a number
// of lots of a contract at a given price (or better / any for market orders).
//
// Prices are integer cents in [0, 100] for prediction markets. Never float.
type Order struct {
	ID        string
	UserID    string
	Side      Side
	Type      OrderType
	Price     int64     // cents; 0 means "no limit" (market orders)
	Quantity  int64     // lots; must be > 0
	Timestamp time.Time // price-time priority tiebreaker
}

// Trade is a match between a resting order and an incoming order.
//
// Trade price is always the *passive* (resting) order's price — this is the
// "price improvement" convention: an incoming aggressive order at 65¢
// crossing a 64¢ resting ask executes at 64¢, not 65¢.
type Trade struct {
	BuyOrderID  string
	SellOrderID string
	Price       int64 // cents
	Quantity    int64 // lots
	Timestamp   time.Time
}
