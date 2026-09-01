package phase2

import (
	"errors"
	"testing"
	"time"

	"github.com/netologist/go-order-book/internal/orderbook"
)

// makeOrder is a test helper that builds a valid limit order with a
// deterministic timestamp so price-time priority tests are reproducible.
func makeOrder(id, user string, side orderbook.Side, price, qty int64, ts time.Time) orderbook.Order {
	return orderbook.Order{
		ID:        id,
		UserID:    user,
		Side:      side,
		Type:      orderbook.Limit,
		Price:     price,
		Quantity:  qty,
		Timestamp: ts,
	}
}

func TestPlaceOrder_LimitMatch(t *testing.T) {
	// BUY 10 @ 65¢ matches existing SELL 10 @ 64¢ at the resting 64¢.
	ob := &OrderBook{}
	if _, err := ob.PlaceOrder(makeOrder("s1", "alice", orderbook.Sell, 64, 10, time.Unix(1, 0))); err != nil {
		t.Fatal(err)
	}
	trades, err := ob.PlaceOrder(makeOrder("b1", "bob", orderbook.Buy, 65, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 {
		t.Fatalf("want 1 trade, got %d", len(trades))
	}
	tr := trades[0]
	if tr.Price != 64 {
		t.Errorf("trade price = %d, want 64 (resting order's price)", tr.Price)
	}
	if tr.Quantity != 10 {
		t.Errorf("trade qty = %d, want 10", tr.Quantity)
	}
	if tr.BuyOrderID != "b1" || tr.SellOrderID != "s1" {
		t.Errorf("trade ids = (%s, %s), want (b1, s1)", tr.BuyOrderID, tr.SellOrderID)
	}
	if ob.GetBestBid() != nil {
		t.Errorf("expected empty bid side, got %+v", ob.GetBestBid())
	}
	if ob.GetBestAsk() != nil {
		t.Errorf("expected empty ask side, got %+v", ob.GetBestAsk())
	}
}

func TestPlaceOrder_PartialFill(t *testing.T) {
	// BUY 10 @ 65¢ vs SELL 6 @ 64¢ → 6 fill, 4 lots rest on bid.
	ob := &OrderBook{}
	if _, err := ob.PlaceOrder(makeOrder("s1", "alice", orderbook.Sell, 64, 6, time.Unix(1, 0))); err != nil {
		t.Fatal(err)
	}
	trades, err := ob.PlaceOrder(makeOrder("b1", "bob", orderbook.Buy, 65, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].Quantity != 6 {
		t.Fatalf("want 1 trade of 6 lots, got %+v", trades)
	}
	best := ob.GetBestBid()
	if best == nil {
		t.Fatal("expected 4 lots resting on bid, got nil")
	}
	if best.Quantity != 4 {
		t.Errorf("resting bid qty = %d, want 4", best.Quantity)
	}
	if best.Price != 65 {
		t.Errorf("resting bid price = %d, want 65", best.Price)
	}
}

func TestPlaceOrder_NoMatchWhenPricesDontCross(t *testing.T) {
	// BUY 10 @ 60¢ vs SELL 10 @ 65¢ → no match, both rest.
	ob := &OrderBook{}
	if _, err := ob.PlaceOrder(makeOrder("s1", "alice", orderbook.Sell, 65, 10, time.Unix(1, 0))); err != nil {
		t.Fatal(err)
	}
	trades, err := ob.PlaceOrder(makeOrder("b1", "bob", orderbook.Buy, 60, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 0 {
		t.Errorf("want 0 trades, got %d", len(trades))
	}
	if best := ob.GetBestAsk(); best == nil || best.Price != 65 {
		t.Errorf("expected best ask @ 65, got %+v", best)
	}
	if best := ob.GetBestBid(); best == nil || best.Price != 60 {
		t.Errorf("expected best bid @ 60, got %+v", best)
	}
}

func TestPlaceOrder_PriceTimePriority(t *testing.T) {
	// Two SELLs at 65¢, alice comes first. The earlier one fills first.
	ob := &OrderBook{}
	if _, err := ob.PlaceOrder(makeOrder("s1", "alice", orderbook.Sell, 65, 5, time.Unix(10, 0))); err != nil {
		t.Fatal(err)
	}
	if _, err := ob.PlaceOrder(makeOrder("s2", "bob", orderbook.Sell, 65, 5, time.Unix(20, 0))); err != nil {
		t.Fatal(err)
	}
	trades, err := ob.PlaceOrder(makeOrder("b1", "carol", orderbook.Buy, 65, 5, time.Unix(30, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 {
		t.Fatalf("want 1 trade, got %d", len(trades))
	}
	if trades[0].SellOrderID != "s1" {
		t.Errorf("earlier order should fill first, got %s", trades[0].SellOrderID)
	}
}

func TestPlaceOrder_MultiLevelSweep(t *testing.T) {
	// BUY 10 @ 65¢ vs asks at 64¢ (3 lots) and 65¢ (7 lots) → two trades.
	ob := &OrderBook{}
	if _, err := ob.PlaceOrder(makeOrder("s1", "alice", orderbook.Sell, 64, 3, time.Unix(1, 0))); err != nil {
		t.Fatal(err)
	}
	if _, err := ob.PlaceOrder(makeOrder("s2", "bob", orderbook.Sell, 65, 7, time.Unix(2, 0))); err != nil {
		t.Fatal(err)
	}
	trades, err := ob.PlaceOrder(makeOrder("b1", "carol", orderbook.Buy, 65, 10, time.Unix(3, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 2 {
		t.Fatalf("want 2 trades, got %d", len(trades))
	}
	if trades[0].Price != 64 || trades[0].Quantity != 3 {
		t.Errorf("trade 1 = (@%d, %d), want (@64, 3)", trades[0].Price, trades[0].Quantity)
	}
	if trades[1].Price != 65 || trades[1].Quantity != 7 {
		t.Errorf("trade 2 = (@%d, %d), want (@65, 7)", trades[1].Price, trades[1].Quantity)
	}
}

func TestCancelOrder(t *testing.T) {
	ob := &OrderBook{}
	if _, err := ob.PlaceOrder(makeOrder("s1", "alice", orderbook.Sell, 64, 10, time.Unix(1, 0))); err != nil {
		t.Fatal(err)
	}
	if err := ob.CancelOrder("s1"); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	if ob.GetBestAsk() != nil {
		t.Errorf("expected empty book after cancel, got %+v", ob.GetBestAsk())
	}
}

func TestCancelOrder_NotFound(t *testing.T) {
	ob := &OrderBook{}
	err := ob.CancelOrder("ghost")
	if !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("want ErrOrderNotFound, got %v", err)
	}
}

func TestCancelOrder_DoubleCancelFails(t *testing.T) {
	ob := &OrderBook{}
	if _, err := ob.PlaceOrder(makeOrder("s1", "alice", orderbook.Sell, 64, 10, time.Unix(1, 0))); err != nil {
		t.Fatal(err)
	}
	if err := ob.CancelOrder("s1"); err != nil {
		t.Fatal(err)
	}
	if err := ob.CancelOrder("s1"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("second cancel should return ErrOrderNotFound, got %v", err)
	}
}

func TestEmptyOrderBook(t *testing.T) {
	ob := &OrderBook{}
	if ob.GetBestBid() != nil {
		t.Errorf("empty book should have nil best bid, got %+v", ob.GetBestBid())
	}
	if ob.GetBestAsk() != nil {
		t.Errorf("empty book should have nil best ask, got %+v", ob.GetBestAsk())
	}
}

func TestGetBest_BasicSequence(t *testing.T) {
	ob := &OrderBook{}
	// Stack: bid 60, ask 65, bid 61, ask 64. Best bid = 61, best ask = 64.
	_, _ = ob.PlaceOrder(makeOrder("b1", "u", orderbook.Buy, 60, 1, time.Unix(1, 0)))
	if best := ob.GetBestBid(); best == nil || best.Price != 60 {
		t.Fatalf("after first bid, best = %+v, want bid @ 60", best)
	}
	_, _ = ob.PlaceOrder(makeOrder("a1", "u", orderbook.Sell, 65, 1, time.Unix(2, 0)))
	if best := ob.GetBestAsk(); best == nil || best.Price != 65 {
		t.Fatalf("after first ask, best ask = %+v, want ask @ 65", best)
	}
	_, _ = ob.PlaceOrder(makeOrder("b2", "u", orderbook.Buy, 61, 1, time.Unix(3, 0)))
	if best := ob.GetBestBid(); best == nil || best.Price != 61 {
		t.Errorf("best bid = %+v, want bid @ 61", best)
	}
	_, _ = ob.PlaceOrder(makeOrder("a2", "u", orderbook.Sell, 64, 1, time.Unix(4, 0)))
	if best := ob.GetBestAsk(); best == nil || best.Price != 64 {
		t.Errorf("best ask = %+v, want ask @ 64", best)
	}
}
