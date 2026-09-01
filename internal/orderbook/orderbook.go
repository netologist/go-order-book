package orderbook

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrUnsupportedOrderType is returned when an order type isn't supported.
var ErrUnsupportedOrderType = errors.New("unsupported order type")

// ErrMarketNoLiquidity is returned when a market order can't fill at all
// or the book is exhausted before the full quantity is matched.
var ErrMarketNoLiquidity = errors.New("market order found no liquidity")

// OrderBook is a thread-safe limit-order book with price-time priority.
//
// Sort order (lock held at all times):
//   - bids: price DESC, timestamp ASC, id ASC
//   - asks: price ASC,  timestamp ASC, id ASC
//
// Methods: PlaceOrder handles Limit, Market, and IOC with self-match
// prevention. CancelOrder, GetBestBid, GetBestAsk are also exposed.
type OrderBook struct {
	mu   sync.RWMutex
	bids []Order
	asks []Order
}

// PlaceOrder validates and processes o against the book.
//   - Limit:  match at or better than price, rest remainder
//   - Market: sweep entire opposite side at any price
//   - IOC:    fill what crosses within the price limit, cancel remainder
//
// Self-match: never fill against an order from the same UserID.
// Returns trades in execution order.
func (ob *OrderBook) PlaceOrder(o Order) ([]Trade, error) {
	if err := ValidateOrder(o); err != nil {
		return nil, err
	}

	ob.mu.Lock()
	defer ob.mu.Unlock()

	switch o.Type {
	case Limit:
		return ob.placeLimit(o), nil
	case Market:
		return ob.placeMarket(o)
	case IOC:
		return ob.placeIOC(o), nil
	}
	return nil, ErrUnsupportedOrderType
}

// CancelOrder removes the order with the given id. Returns ErrOrderNotFound
// if the order isn't resting on either side.
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

// GetBestBid returns a copy of the best (highest-price) bid, or nil.
func (ob *OrderBook) GetBestBid() *Order {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return bestOf(&ob.bids)
}

// GetBestAsk returns a copy of the best (lowest-price) ask, or nil.
func (ob *OrderBook) GetBestAsk() *Order {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return bestOf(&ob.asks)
}

// BidCount returns the number of resting bids.
func (ob *OrderBook) BidCount() int {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return len(ob.bids)
}

// AskCount returns the number of resting asks.
func (ob *OrderBook) AskCount() int {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return len(ob.asks)
}

// ---------------------------------------------------------------------------
// limit-order matching
// ---------------------------------------------------------------------------

// placeLimit handles self-match prevention + limit-order matching.
func (ob *OrderBook) placeLimit(o Order) []Trade {
	switch o.Side {
	case Buy:
		best := bestOf(&ob.asks)
		if best != nil && best.UserID == o.UserID && best.Price <= o.Price {
			ob.bids = insertSorted(ob.bids, o, bidLess)
			return nil
		}
		return ob.matchBuy(o)
	case Sell:
		best := bestOf(&ob.bids)
		if best != nil && best.UserID == o.UserID && best.Price >= o.Price {
			ob.asks = insertSorted(ob.asks, o, askLess)
			return nil
		}
		return ob.matchSell(o)
	}
	return nil
}

func (ob *OrderBook) matchBuy(o Order) []Trade {
	var trades []Trade
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

func (ob *OrderBook) matchSell(o Order) []Trade {
	var trades []Trade
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

func (ob *OrderBook) tryFillAsk(o Order, remaining *int64) (Trade, bool) {
	if len(ob.asks) == 0 || ob.asks[0].Price > o.Price {
		return Trade{}, false
	}
	return ob.fillFrom(&ob.asks, remaining, o.ID, o.Side), true
}

func (ob *OrderBook) tryFillBid(o Order, remaining *int64) (Trade, bool) {
	if len(ob.bids) == 0 || ob.bids[0].Price < o.Price {
		return Trade{}, false
	}
	return ob.fillFrom(&ob.bids, remaining, o.ID, o.Side), true
}

// fillFrom creates a trade from the head of s, decrements *remaining, and
// reduces or removes the head. The caller guarantees the head crosses.
func (ob *OrderBook) fillFrom(s *[]Order, remaining *int64, aggressorID string, side Side) Trade {
	resting := &(*s)[0]
	qty := *remaining
	if resting.Quantity < qty {
		qty = resting.Quantity
	}

	var trade Trade
	if side == Buy {
		trade = Trade{
			BuyOrderID:  aggressorID,
			SellOrderID: resting.ID,
			Price:       resting.Price,
			Quantity:    qty,
			Timestamp:   time.Now(),
		}
	} else {
		trade = Trade{
			BuyOrderID:  resting.ID,
			SellOrderID: aggressorID,
			Price:       resting.Price,
			Quantity:    qty,
			Timestamp:   time.Now(),
		}
	}

	consumeHead(s, qty)
	*remaining -= qty
	return trade
}

// NB: Market (sweep) and IOC matching live in matching.go

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

func consumeHead(s *[]Order, qty int64) {
	if qty == (*s)[0].Quantity {
		*s = (*s)[1:]
		return
	}
	(*s)[0].Quantity -= qty
}

func popBest(s *[]Order) *Order {
	if len(*s) == 0 {
		return nil
	}
	o := (*s)[0]
	*s = (*s)[1:]
	return &o
}

func removeByID(s *[]Order, id string) (Order, bool) {
	for i := range *s {
		if (*s)[i].ID == id {
			removed := (*s)[i]
			*s = append((*s)[:i], (*s)[i+1:]...)
			return removed, true
		}
	}
	return Order{}, false
}

func bestOf(s *[]Order) *Order {
	if len(*s) == 0 {
		return nil
	}
	o := (*s)[0]
	return &o
}

func askLess(a, b Order) bool {
	if a.Price != b.Price {
		return a.Price < b.Price
	}
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	return a.ID < b.ID
}

func bidLess(a, b Order) bool {
	if a.Price != b.Price {
		return a.Price > b.Price
	}
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	return a.ID < b.ID
}

func insertSorted(s []Order, o Order, less func(a, b Order) bool) []Order {
	i := sort.Search(len(s), func(i int) bool { return less(o, s[i]) })
	s = append(s, Order{})
	copy(s[i+1:], s[i:])
	s[i] = o
	return s
}
