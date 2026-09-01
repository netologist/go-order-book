// Package phase3 is your practice target for Phase 3: market orders, IOC,
// and self-match prevention, layered on top of the Phase 2 OrderBook.
package phase3

import (
	"errors"
	"time"

	"github.com/netologist/go-order-book/exercises/phase2-orderbook"
	"github.com/netologist/go-order-book/internal/orderbook"
)

// ErrMarketNoLiquidity is returned when a market order can't fill at all —
// either the book is empty or the only opposite orders are from the same
// user (self-match prevention).
var ErrMarketNoLiquidity = errors.New("market order found no liquidity")

// Matcher owns the strategy for placing orders. The book is a passive
// data structure (Phase 2); the matcher decides *what* to do for limit,
// market, and IOC orders.
type Matcher struct {
	book *phase2.OrderBook
}

// NewMatcher wires a matcher to a book. The matcher mutates the book; the
// book is the source of truth for resting orders.
func NewMatcher(book *phase2.OrderBook) *Matcher {
	return &Matcher{book: book}
}

// PlaceOrder dispatches by OrderType:
//   - Limit:  delegate to book.PlaceOrder (Phase 2 behavior), but check
//     self-match first — if the best opposite-side order belongs to the
//     same user and would cross, rest the order without matching.
//   - Market: sweep opposite side at any price, cancel remainder.
//     Returns ErrMarketNoLiquidity if the book is exhausted before filling.
//   - IOC:    fill what crosses within the price limit, cancel remainder.
//     Never returns an error on partial/no fill.
//
// Self-match: never fill against an order from the same UserID.
func (m *Matcher) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error) {
	switch o.Type {
	case orderbook.Limit:
		return m.placeLimit(o)
	case orderbook.Market:
		return m.placeMarket(o)
	case orderbook.IOC:
		return m.placeIOC(o)
	}
	return nil, nil
}

// placeLimit handles a limit order with self-match prevention.
func (m *Matcher) placeLimit(o orderbook.Order) ([]orderbook.Trade, error) {
	switch o.Side {
	case orderbook.Buy:
		best := m.book.GetBestAsk()
		if best != nil && best.UserID == o.UserID && best.Price <= o.Price {
			m.book.InsertBid(o)
			return nil, nil
		}
	case orderbook.Sell:
		best := m.book.GetBestBid()
		if best != nil && best.UserID == o.UserID && best.Price >= o.Price {
			m.book.InsertAsk(o)
			return nil, nil
		}
	}
	return m.book.PlaceOrder(o)
}

// placeMarket sweeps the entire opposite side at any price.
func (m *Matcher) placeMarket(o orderbook.Order) ([]orderbook.Trade, error) {
	var (
		trades    []orderbook.Trade
		remaining = o.Quantity
		skipped   []orderbook.Order // same-user orders re-inserted after sweep
	)

	for remaining > 0 {
		var best *orderbook.Order
		switch o.Side {
		case orderbook.Buy:
			best = m.book.PopBestAsk()
		case orderbook.Sell:
			best = m.book.PopBestBid()
		}
		if best == nil {
			break
		}

		// Self-match: skip same-user orders.
		if best.UserID == o.UserID {
			skipped = append(skipped, *best)
			continue
		}

		qty := remaining
		if best.Quantity < qty {
			qty = best.Quantity
		}

		trade := orderbook.Trade{
			Quantity:  qty,
			Price:     best.Price,
			Timestamp: time.Now(),
		}
		if o.Side == orderbook.Buy {
			trade.BuyOrderID = o.ID
			trade.SellOrderID = best.ID
		} else {
			trade.BuyOrderID = best.ID
			trade.SellOrderID = o.ID
		}
		trades = append(trades, trade)
		remaining -= qty

		// Rest any unfilled portion of the opposite order.
		if best.Quantity > qty {
			best.Quantity -= qty
			switch o.Side {
			case orderbook.Buy:
				m.book.InsertAsk(*best)
			case orderbook.Sell:
				m.book.InsertBid(*best)
			}
		}
	}

	// Re-insert any skipped same-user orders.
	for _, s := range skipped {
		switch o.Side {
		case orderbook.Buy:
			m.book.InsertAsk(s)
		case orderbook.Sell:
			m.book.InsertBid(s)
		}
	}

	if remaining > 0 {
		if len(trades) > 0 {
			return trades, ErrMarketNoLiquidity
		}
		return nil, ErrMarketNoLiquidity
	}
	return trades, nil
}

// placeIOC fills what crosses within the price limit, cancels the remainder,
// and never rests. Returns empty trades (no error) when nothing fills.
func (m *Matcher) placeIOC(o orderbook.Order) ([]orderbook.Trade, error) {
	var (
		trades    []orderbook.Trade
		remaining = o.Quantity
		skipped   []orderbook.Order
	)

	for remaining > 0 {
		var best *orderbook.Order
		canCross := false
		switch o.Side {
		case orderbook.Buy:
			best = m.book.PopBestAsk()
			if best == nil {
				return trades, nil
			}
			canCross = best.Price <= o.Price
		case orderbook.Sell:
			best = m.book.PopBestBid()
			if best == nil {
				return trades, nil
			}
			canCross = best.Price >= o.Price
		}

		if !canCross {
			// Prices don't cross — push back and stop.
			switch o.Side {
			case orderbook.Buy:
				m.book.InsertAsk(*best)
			case orderbook.Sell:
				m.book.InsertBid(*best)
			}
			return trades, nil
		}

		// Self-match: skip same-user orders.
		if best.UserID == o.UserID {
			skipped = append(skipped, *best)
			continue
		}

		qty := remaining
		if best.Quantity < qty {
			qty = best.Quantity
		}

		trade := orderbook.Trade{
			Quantity:  qty,
			Price:     best.Price,
			Timestamp: time.Now(),
		}
		if o.Side == orderbook.Buy {
			trade.BuyOrderID = o.ID
			trade.SellOrderID = best.ID
		} else {
			trade.BuyOrderID = best.ID
			trade.SellOrderID = o.ID
		}
		trades = append(trades, trade)
		remaining -= qty

		if best.Quantity > qty {
			best.Quantity -= qty
			switch o.Side {
			case orderbook.Buy:
				m.book.InsertAsk(*best)
			case orderbook.Sell:
				m.book.InsertBid(*best)
			}
		}
	}

	// Re-insert any skipped same-user orders.
	for _, s := range skipped {
		switch o.Side {
		case orderbook.Buy:
			m.book.InsertAsk(s)
		case orderbook.Sell:
			m.book.InsertBid(s)
		}
	}

	return trades, nil
}
