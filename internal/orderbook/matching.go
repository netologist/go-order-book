package orderbook

import "time"

// ---------------------------------------------------------------------------
// market-order matching (sweep at any price)
// ---------------------------------------------------------------------------

func (ob *OrderBook) placeMarket(o Order) ([]Trade, error) {
	var (
		trades    []Trade
		remaining = o.Quantity
		skipped   []Order // same-user orders re-inserted after sweep
	)

	for remaining > 0 {
		var best *Order
		switch o.Side {
		case Buy:
			best = popBest(&ob.asks)
		case Sell:
			best = popBest(&ob.bids)
		}
		if best == nil {
			break
		}

		if best.UserID == o.UserID {
			skipped = append(skipped, *best)
			continue
		}

		trade, rest := fillMarket(*best, &remaining, o.ID, o.Side)
		trades = append(trades, trade)

		if rest != nil {
			switch o.Side {
			case Buy:
				ob.asks = insertSorted(ob.asks, *rest, askLess)
			case Sell:
				ob.bids = insertSorted(ob.bids, *rest, bidLess)
			}
		}
	}

	// Re-insert any skipped same-user orders.
	for _, s := range skipped {
		switch o.Side {
		case Buy:
			ob.asks = insertSorted(ob.asks, s, askLess)
		case Sell:
			ob.bids = insertSorted(ob.bids, s, bidLess)
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

// fillMarket creates a trade from best, decrements remaining.
// Returns the trade and any unfilled portion of best to rest.
func fillMarket(best Order, remaining *int64, aggressorID string, side Side) (Trade, *Order) {
	qty := *remaining
	if best.Quantity < qty {
		qty = best.Quantity
	}

	var trade Trade
	if side == Buy {
		trade = Trade{
			BuyOrderID:  aggressorID,
			SellOrderID: best.ID,
			Price:       best.Price,
			Quantity:    qty,
			Timestamp:   time.Now(),
		}
	} else {
		trade = Trade{
			BuyOrderID:  best.ID,
			SellOrderID: aggressorID,
			Price:       best.Price,
			Quantity:    qty,
			Timestamp:   time.Now(),
		}
	}

	*remaining -= qty

	if best.Quantity > qty {
		best.Quantity -= qty
		return trade, &best
	}
	return trade, nil
}

// ---------------------------------------------------------------------------
// IOC matching (fill what crosses, don't rest remainder)
// ---------------------------------------------------------------------------

func (ob *OrderBook) placeIOC(o Order) []Trade {
	var (
		trades    []Trade
		remaining = o.Quantity
		skipped   []Order
	)

	for remaining > 0 {
		var best *Order
		canCross := false
		switch o.Side {
		case Buy:
			best = popBest(&ob.asks)
			if best == nil {
				return trades
			}
			canCross = best.Price <= o.Price
		case Sell:
			best = popBest(&ob.bids)
			if best == nil {
				return trades
			}
			canCross = best.Price >= o.Price
		}

		if !canCross {
			switch o.Side {
			case Buy:
				ob.asks = insertSorted(ob.asks, *best, askLess)
			case Sell:
				ob.bids = insertSorted(ob.bids, *best, bidLess)
			}
			return trades
		}

		if best.UserID == o.UserID {
			skipped = append(skipped, *best)
			continue
		}

		trade, rest := fillMarket(*best, &remaining, o.ID, o.Side)
		trades = append(trades, trade)

		if rest != nil {
			switch o.Side {
			case Buy:
				ob.asks = insertSorted(ob.asks, *rest, askLess)
			case Sell:
				ob.bids = insertSorted(ob.bids, *rest, bidLess)
			}
		}
	}

	for _, s := range skipped {
		switch o.Side {
		case Buy:
			ob.asks = insertSorted(ob.asks, s, askLess)
		case Sell:
			ob.bids = insertSorted(ob.bids, s, bidLess)
		}
	}

	return trades
}
