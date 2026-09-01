// Package phase2 is your practice target for Phase 2 of the order-book
// study guide: a single-threaded order book with limit-order matching using
// a sorted slice.
//
// The methods below are stubs — they compile but panic. Implement them so
// the tests in orderbook_test.go pass.
package phase2

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/netologist/go-order-book/internal/orderbook"
)

// ErrOrderNotFound is returned by CancelOrder when no resting order with
// the given ID exists.
var ErrOrderNotFound = errors.New("order not found")

// errUnsupportedOrderType is returned by PlaceOrder for non-limit orders,
// which are out of scope for Phase 2 (Market and IOC arrive in Phase 3).
var errUnsupportedOrderType = errors.New("phase2: only limit orders are supported")

// OrderBook holds resting limit orders on both sides of the book.
//
// Sort order (single-threaded invariant — must hold at all times):
//   - bids: price DESC, timestamp ASC, id ASC
//   - asks: price ASC,  timestamp ASC, id ASC
//
// The invariant is maintained by inserting new orders at the correct sorted
// position via sort.Search rather than appending and re-sorting.
type OrderBook struct {
	mu   sync.Mutex // reserved for Phase 4 — not used here
	bids []orderbook.Order
	asks []orderbook.Order
}

// PlaceOrder inserts o into the book, matching it against the opposite side
// if the prices cross. Returns the trades generated, in execution order.
//
// For Phase 2: only Limit orders are in scope. Market and IOC are Phase 3.
// Trust the caller — validation has already been done upstream.
func (ob *OrderBook) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error) {
	if o.Type != orderbook.Limit {
		return nil, errUnsupportedOrderType
	}

	switch o.Side {
	case orderbook.Buy:
		return ob.matchBuy(o), nil
	case orderbook.Sell:
		return ob.matchSell(o), nil
	}
	return nil, nil
}

// matchBuy matches an incoming BUY against the ask side, then rests any
// unfilled remainder on the bid side. Returns trades in execution order.
func (ob *OrderBook) matchBuy(o orderbook.Order) []orderbook.Trade {
	var trades []orderbook.Trade
	remaining := o.Quantity

	for remaining > 0 {
		trade, ok := ob.tryFillAsk(o, &remaining)
		if !ok {
			break
		}
		trades = append(trades, trade)
	}

	if remaining > 0 {
		rest := o
		rest.Quantity = remaining
		ob.bids = insertSorted(ob.bids, rest, bidLess)
	}
	return trades
}

// matchSell is the mirror of matchBuy: sweeps the bid side, then rests any
// unfilled remainder on the ask side.
func (ob *OrderBook) matchSell(o orderbook.Order) []orderbook.Trade {
	var trades []orderbook.Trade
	remaining := o.Quantity

	for remaining > 0 {
		trade, ok := ob.tryFillBid(o, &remaining)
		if !ok {
			break
		}
		trades = append(trades, trade)
	}

	if remaining > 0 {
		rest := o
		rest.Quantity = remaining
		ob.asks = insertSorted(ob.asks, rest, askLess)
	}
	return trades
}

// tryFillAsk attempts to fill incoming BUY against the best ask. On a
// successful cross, decrements *remaining by the matched qty and returns
// the resulting trade. Returns ok=false when no fill is possible (no
// opposite orders, or best ask is above the buy's limit price).
func (ob *OrderBook) tryFillAsk(o orderbook.Order, remaining *int64) (orderbook.Trade, bool) {
	if len(ob.asks) == 0 || ob.asks[0].Price > o.Price {
		return orderbook.Trade{}, false
	}

	resting := &ob.asks[0]
	qty := *remaining
	if resting.Quantity < qty {
		qty = resting.Quantity
	}

	trade := orderbook.Trade{
		BuyOrderID:  o.ID,
		SellOrderID: resting.ID,
		Price:       resting.Price, // resting order's price = price improvement
		Quantity:    qty,
		Timestamp:   time.Now(),
	}
	ob.consumeHead(&ob.asks, qty)
	*remaining -= qty
	return trade, true
}

// tryFillBid is the mirror of tryFillAsk for an incoming SELL.
func (ob *OrderBook) tryFillBid(o orderbook.Order, remaining *int64) (orderbook.Trade, bool) {
	if len(ob.bids) == 0 || ob.bids[0].Price < o.Price {
		return orderbook.Trade{}, false
	}

	resting := &ob.bids[0]
	qty := *remaining
	if resting.Quantity < qty {
		qty = resting.Quantity
	}

	trade := orderbook.Trade{
		BuyOrderID:  resting.ID,
		SellOrderID: o.ID,
		Price:       resting.Price,
		Quantity:    qty,
		Timestamp:   time.Now(),
	}
	ob.consumeHead(&ob.bids, qty)
	*remaining -= qty
	return trade, true
}

// consumeHead removes qty lots from the head of s. If the head is fully
// consumed, drops it; otherwise decrements its Quantity in place. Caller
// must guarantee len(s) > 0 and qty <= s[0].Quantity.
func (ob *OrderBook) consumeHead(s *[]orderbook.Order, qty int64) {
	if qty == (*s)[0].Quantity {
		*s = (*s)[1:]
		return
	}
	(*s)[0].Quantity -= qty
}

// CancelOrder removes the order with the given id from whichever side it's
// resting on. Returns ErrOrderNotFound if the order is not on the book.
func (ob *OrderBook) CancelOrder(id string) error {
	if removed, ok := removeByID(&ob.bids, id); ok {
		_ = removed
		return nil
	}
	if _, ok := removeByID(&ob.asks, id); ok {
		return nil
	}
	return ErrOrderNotFound
}

// removeByID scans s for an order with the given id and removes it from
// the slice on first match. Returns the removed order and ok=true, or
// ok=false if not found.
func removeByID(s *[]orderbook.Order, id string) (orderbook.Order, bool) {
	for i := range *s {
		if (*s)[i].ID == id {
			removed := (*s)[i]
			*s = append((*s)[:i], (*s)[i+1:]...)
			return removed, true
		}
	}
	return orderbook.Order{}, false
}

// GetBestBid returns a pointer to a *copy* of the best (highest-price) bid,
// or nil if the bid side is empty. Returning a copy prevents the caller
// from mutating the book by accident.
func (ob *OrderBook) GetBestBid() *orderbook.Order {
	return bestOf(&ob.bids)
}

// GetBestAsk returns a pointer to a *copy* of the best (lowest-price) ask,
// or nil if the ask side is empty.
func (ob *OrderBook) GetBestAsk() *orderbook.Order {
	return bestOf(&ob.asks)
}

// bestOf returns a pointer to a copy of s[0], or nil if s is empty.
func bestOf(s *[]orderbook.Order) *orderbook.Order {
	if len(*s) == 0 {
		return nil
	}
	o := (*s)[0] // value copy
	return &o
}

// PopBestBid removes and returns the best bid, or nil if the bid side is empty.
// The caller takes ownership of the returned copy.
func (ob *OrderBook) PopBestBid() *orderbook.Order {
	if len(ob.bids) == 0 {
		return nil
	}
	o := ob.bids[0]
	ob.bids = ob.bids[1:]
	return &o
}

// PopBestAsk removes and returns the best ask, or nil if the ask side is empty.
// The caller takes ownership of the returned copy.
func (ob *OrderBook) PopBestAsk() *orderbook.Order {
	if len(ob.asks) == 0 {
		return nil
	}
	o := ob.asks[0]
	ob.asks = ob.asks[1:]
	return &o
}

// InsertBid places o on the bid side while preserving sort order.
func (ob *OrderBook) InsertBid(o orderbook.Order) {
	ob.bids = insertSorted(ob.bids, o, bidLess)
}

// InsertAsk places o on the ask side while preserving sort order.
func (ob *OrderBook) InsertAsk(o orderbook.Order) {
	ob.asks = insertSorted(ob.asks, o, askLess)
}

// BidCount returns the number of resting bids.
func (ob *OrderBook) BidCount() int { return len(ob.bids) }

// AskCount returns the number of resting asks.
func (ob *OrderBook) AskCount() int { return len(ob.asks) }

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

// bidLess reports whether a should sort before b in the bid list.
// Bids are sorted: price DESC, timestamp ASC, id ASC.
func bidLess(a, b orderbook.Order) bool {
	if a.Price != b.Price {
		return a.Price > b.Price
	}
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	return a.ID < b.ID
}

// insertSorted returns s with o inserted at the position dictated by less,
// preserving the sort invariant. Caller passes the side-specific less func.
func insertSorted(s []orderbook.Order, o orderbook.Order, less func(a, b orderbook.Order) bool) []orderbook.Order {
	i := sort.Search(len(s), func(i int) bool { return less(o, s[i]) })
	s = append(s, orderbook.Order{})
	copy(s[i+1:], s[i:])
	s[i] = o
	return s
}
