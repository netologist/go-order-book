// Package phase4 is your practice target for Phase 4: thread-safe OrderBook
// with sync.Mutex / sync.RWMutex. Same API as Phase 2, but every method
// must hold the appropriate lock.
package phase4

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/netologist/go-order-book/internal/orderbook"
)

// ErrOrderNotFound mirrors the Phase 2 sentinel.
var ErrOrderNotFound = errors.New("order not found")

// errUnsupportedOrderType mirrors the Phase 2 sentinel.
var errUnsupportedOrderType = errors.New("phase4: only limit orders are supported")

// OrderBook is a thread-safe order book. The shape mirrors Phase 2 but
// mu is now actively used.
//
// Sort order (lock held at all times):
//   - bids: price DESC, timestamp ASC, id ASC
//   - asks: price ASC,  timestamp ASC, id ASC
type OrderBook struct {
	mu   sync.RWMutex
	bids []orderbook.Order
	asks []orderbook.Order
}

// PlaceOrder inserts o into the book, matching it against the opposite side
// if the prices cross. Returns the trades generated, in execution order.
func (ob *OrderBook) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error) {
	if o.Type != orderbook.Limit {
		return nil, errUnsupportedOrderType
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()

	switch o.Side {
	case orderbook.Buy:
		return ob.matchBuy(o), nil
	case orderbook.Sell:
		return ob.matchSell(o), nil
	}
	return nil, nil
}

// CancelOrder removes the order with the given id from whichever side it's
// resting on. Returns ErrOrderNotFound if the order is not on the book.
func (ob *OrderBook) CancelOrder(id string) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	if removed, ok := removeByID(&ob.bids, id); ok {
		_ = removed
		return nil
	}
	if _, ok := removeByID(&ob.asks, id); ok {
		return nil
	}
	return ErrOrderNotFound
}

// GetBestBid returns a pointer to a *copy* of the best (highest-price) bid,
// or nil if the bid side is empty.
func (ob *OrderBook) GetBestBid() *orderbook.Order {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	return bestOf(&ob.bids)
}

// GetBestAsk returns a pointer to a *copy* of the best (lowest-price) ask,
// or nil if the ask side is empty.
func (ob *OrderBook) GetBestAsk() *orderbook.Order {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	return bestOf(&ob.asks)
}

// ---------------------------------------------------------------------------
// internal helpers — identical logic to Phase 2
// ---------------------------------------------------------------------------

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
		Price:       resting.Price,
		Quantity:    qty,
		Timestamp:   time.Now(),
	}
	consumeHead(&ob.asks, qty)
	*remaining -= qty
	return trade, true
}

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
	consumeHead(&ob.bids, qty)
	*remaining -= qty
	return trade, true
}

func consumeHead(s *[]orderbook.Order, qty int64) {
	if qty == (*s)[0].Quantity {
		*s = (*s)[1:]
		return
	}
	(*s)[0].Quantity -= qty
}

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

func bestOf(s *[]orderbook.Order) *orderbook.Order {
	if len(*s) == 0 {
		return nil
	}
	o := (*s)[0] // value copy
	return &o
}

func askLess(a, b orderbook.Order) bool {
	if a.Price != b.Price {
		return a.Price < b.Price
	}
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	return a.ID < b.ID
}

func bidLess(a, b orderbook.Order) bool {
	if a.Price != b.Price {
		return a.Price > b.Price
	}
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	return a.ID < b.ID
}

func insertSorted(s []orderbook.Order, o orderbook.Order, less func(a, b orderbook.Order) bool) []orderbook.Order {
	i := sort.Search(len(s), func(i int) bool { return less(o, s[i]) })
	s = append(s, orderbook.Order{})
	copy(s[i+1:], s[i:])
	s[i] = o
	return s
}
