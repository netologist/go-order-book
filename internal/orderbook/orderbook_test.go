package orderbook

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func mkOrder(id, user string, side Side, typ OrderType, price, qty int64, ts time.Time) Order {
	return Order{
		ID: id, UserID: user, Side: side, Type: typ,
		Price: price, Quantity: qty, Timestamp: ts,
	}
}

// ---------------------------------------------------------------------------
// Limit order tests
// ---------------------------------------------------------------------------

func TestPlaceOrder_LimitMatch(t *testing.T) {
	ob := &OrderBook{}
	if _, err := ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 64, 10, time.Unix(1, 0))); err != nil {
		t.Fatal(err)
	}
	trades, err := ob.PlaceOrder(mkOrder("b1", "bob", Buy, Limit, 65, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].Price != 64 || trades[0].Quantity != 10 {
		t.Fatalf("want 1 trade (@64, 10), got %+v", trades)
	}
}

func TestPlaceOrder_PartialFill(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 64, 6, time.Unix(1, 0)))
	trades, err := ob.PlaceOrder(mkOrder("b1", "bob", Buy, Limit, 65, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].Quantity != 6 {
		t.Fatalf("want 1 trade of 6, got %+v", trades)
	}
	best := ob.GetBestBid()
	if best == nil || best.Quantity != 4 || best.Price != 65 {
		t.Errorf("want resting bid (@65, 4), got %+v", best)
	}
}

func TestPlaceOrder_NoMatchWhenPricesDontCross(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 65, 10, time.Unix(1, 0)))
	trades, err := ob.PlaceOrder(mkOrder("b1", "bob", Buy, Limit, 60, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 0 {
		t.Errorf("want 0 trades, got %d", len(trades))
	}
}

func TestPlaceOrder_PriceTimePriority(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 65, 5, time.Unix(10, 0)))
	_, _ = ob.PlaceOrder(mkOrder("s2", "bob", Sell, Limit, 65, 5, time.Unix(20, 0)))
	trades, err := ob.PlaceOrder(mkOrder("b1", "carol", Buy, Limit, 65, 5, time.Unix(30, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].SellOrderID != "s1" {
		t.Fatalf("earlier order should fill first, got %+v", trades)
	}
}

func TestPlaceOrder_MultiLevelSweep(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 64, 3, time.Unix(1, 0)))
	_, _ = ob.PlaceOrder(mkOrder("s2", "bob", Sell, Limit, 65, 7, time.Unix(2, 0)))
	trades, err := ob.PlaceOrder(mkOrder("b1", "carol", Buy, Limit, 65, 10, time.Unix(3, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 2 {
		t.Fatalf("want 2 trades, got %d", len(trades))
	}
	if trades[0].Price != 64 || trades[0].Quantity != 3 {
		t.Errorf("trade 1: (@%d, %d)", trades[0].Price, trades[0].Quantity)
	}
	if trades[1].Price != 65 || trades[1].Quantity != 7 {
		t.Errorf("trade 2: (@%d, %d)", trades[1].Price, trades[1].Quantity)
	}
}

// ---------------------------------------------------------------------------
// Market order tests
// ---------------------------------------------------------------------------

func TestMarketOrder_FullFill_OneLevel(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 64, 10, time.Unix(1, 0)))
	trades, err := ob.PlaceOrder(mkOrder("b1", "bob", Buy, Market, 0, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].Quantity != 10 || trades[0].Price != 64 {
		t.Fatalf("want 1 trade (@64, 10), got %+v", trades)
	}
}

func TestMarketOrder_SweepsMultipleLevels(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 64, 3, time.Unix(1, 0)))
	_, _ = ob.PlaceOrder(mkOrder("s2", "alice", Sell, Limit, 65, 5, time.Unix(2, 0)))
	_, _ = ob.PlaceOrder(mkOrder("s3", "alice", Sell, Limit, 66, 5, time.Unix(3, 0)))
	trades, err := ob.PlaceOrder(mkOrder("b1", "bob", Buy, Market, 0, 10, time.Unix(4, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 3 {
		t.Fatalf("want 3 trades, got %d", len(trades))
	}
	want := []int64{3, 5, 2}
	for i, w := range want {
		if trades[i].Quantity != w {
			t.Errorf("trade %d qty = %d, want %d", i, trades[i].Quantity, w)
		}
	}
}

func TestMarketOrder_PartialFill_BookExhausted(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 64, 3, time.Unix(1, 0)))
	trades, err := ob.PlaceOrder(mkOrder("b1", "bob", Buy, Market, 0, 10, time.Unix(2, 0)))
	if !errors.Is(err, ErrMarketNoLiquidity) {
		t.Fatalf("want ErrMarketNoLiquidity, got %v", err)
	}
	if len(trades) != 1 || trades[0].Quantity != 3 {
		t.Fatalf("want 1 trade of 3, got %+v", trades)
	}
}

func TestMarketOrder_EmptyBook(t *testing.T) {
	ob := &OrderBook{}
	_, err := ob.PlaceOrder(mkOrder("b1", "bob", Buy, Market, 0, 10, time.Unix(1, 0)))
	if !errors.Is(err, ErrMarketNoLiquidity) {
		t.Fatalf("want ErrMarketNoLiquidity, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// IOC order tests
// ---------------------------------------------------------------------------

func TestIOC_PartialMatch(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 64, 6, time.Unix(1, 0)))
	trades, err := ob.PlaceOrder(mkOrder("b1", "bob", Buy, IOC, 65, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].Quantity != 6 {
		t.Fatalf("want 1 trade of 6, got %+v", trades)
	}
	if ob.GetBestBid() != nil {
		t.Errorf("IOC remainder must NOT rest, got %+v", ob.GetBestBid())
	}
}

func TestIOC_NoMatch(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 70, 5, time.Unix(1, 0)))
	trades, err := ob.PlaceOrder(mkOrder("b1", "bob", Buy, IOC, 65, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 0 {
		t.Errorf("want 0 trades, got %+v", trades)
	}
}

// ---------------------------------------------------------------------------
// Self-match prevention
// ---------------------------------------------------------------------------

func TestSelfMatchPrevented_Limit(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 64, 10, time.Unix(1, 0)))
	trades, err := ob.PlaceOrder(mkOrder("b1", "alice", Buy, Limit, 65, 5, time.Unix(2, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 0 {
		t.Errorf("want 0 trades (self-match), got %+v", trades)
	}
	if best := ob.GetBestAsk(); best == nil || best.Quantity != 10 {
		t.Errorf("alice's resting ask should be untouched, got %+v", best)
	}
	if best := ob.GetBestBid(); best == nil || best.Quantity != 5 || best.Price != 65 {
		t.Errorf("alice's buy should rest at @65 qty 5, got %+v", best)
	}
}

// ---------------------------------------------------------------------------
// Cancel / best-bid-ask
// ---------------------------------------------------------------------------

func TestCancelOrder(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 64, 10, time.Unix(1, 0)))
	if err := ob.CancelOrder("s1"); err != nil {
		t.Fatal(err)
	}
	if ob.GetBestAsk() != nil {
		t.Errorf("expected empty book after cancel")
	}
}

func TestCancelOrder_NotFound(t *testing.T) {
	ob := &OrderBook{}
	err := ob.CancelOrder("ghost")
	if !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("want ErrOrderNotFound, got %v", err)
	}
}

func TestCancelOrder_DoubleCancel(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("s1", "alice", Sell, Limit, 64, 10, time.Unix(1, 0)))
	_ = ob.CancelOrder("s1")
	if err := ob.CancelOrder("s1"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("second cancel should return ErrOrderNotFound, got %v", err)
	}
}

func TestEmptyBook(t *testing.T) {
	ob := &OrderBook{}
	if ob.GetBestBid() != nil || ob.GetBestAsk() != nil {
		t.Error("empty book should have nil best bid and ask")
	}
}

func TestGetBest_Sequence(t *testing.T) {
	ob := &OrderBook{}
	_, _ = ob.PlaceOrder(mkOrder("b1", "u", Buy, Limit, 60, 1, time.Unix(1, 0)))
	if best := ob.GetBestBid(); best == nil || best.Price != 60 {
		t.Fatalf("after first bid, best = %+v, want @60", best)
	}
	_, _ = ob.PlaceOrder(mkOrder("a1", "u", Sell, Limit, 65, 1, time.Unix(2, 0)))
	if best := ob.GetBestAsk(); best == nil || best.Price != 65 {
		t.Fatalf("after first ask, best = %+v, want @65", best)
	}
	_, _ = ob.PlaceOrder(mkOrder("b2", "u", Buy, Limit, 61, 1, time.Unix(3, 0)))
	if best := ob.GetBestBid(); best == nil || best.Price != 61 {
		t.Errorf("best bid = %+v, want @61", best)
	}
	_, _ = ob.PlaceOrder(mkOrder("a2", "u", Sell, Limit, 64, 1, time.Unix(4, 0)))
	if best := ob.GetBestAsk(); best == nil || best.Price != 64 {
		t.Errorf("best ask = %+v, want @64", best)
	}
}

// ---------------------------------------------------------------------------
// Validation integration
// ---------------------------------------------------------------------------

func TestPlaceOrder_ValidationCalled(t *testing.T) {
	ob := &OrderBook{}
	_, err := ob.PlaceOrder(Order{}) // empty order — should fail validation
	if err == nil {
		t.Fatal("expected validation error for empty order")
	}
	var ve *ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationErrors, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestConcurrent_NoRace(t *testing.T) {
	ob := &OrderBook{}
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, _ = ob.PlaceOrder(mkOrder("b-"+itoa(i), "u", Buy, Limit, 50, 1, time.Now()))
			} else {
				_, _ = ob.PlaceOrder(mkOrder("s-"+itoa(i), "u", Sell, Limit, 80, 1, time.Now()))
			}
		}(i)
	}
	wg.Wait()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
