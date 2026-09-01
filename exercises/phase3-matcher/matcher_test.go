package phase3

import (
	"errors"
	"testing"
	"time"

	"github.com/netologist/go-order-book/exercises/phase2-orderbook"
	"github.com/netologist/go-order-book/internal/orderbook"
)

func mkOrder(id, user string, side orderbook.Side, t orderbook.OrderType, price, qty int64, ts time.Time) orderbook.Order {
	return orderbook.Order{
		ID: id, UserID: user, Side: side, Type: t,
		Price: price, Quantity: qty, Timestamp: ts,
	}
}

func newMatcher() (*Matcher, *phase2.OrderBook) {
	book := &phase2.OrderBook{}
	return NewMatcher(book), book
}

func TestMarketOrder_FullFill_OneLevel(t *testing.T) {
	m, book := newMatcher()
	// Resting: 10 lots @ 64¢ (alice)
	_, _ = book.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, orderbook.Limit, 64, 10, time.Unix(1, 0)))
	// Aggressor: market buy 10 lots from bob
	trades, err := m.PlaceOrder(mkOrder("b1", "bob", orderbook.Buy, orderbook.Market, 0, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(trades) != 1 || trades[0].Quantity != 10 || trades[0].Price != 64 {
		t.Fatalf("want 1 trade (@64, 10), got %+v", trades)
	}
}

func TestMarketOrder_SweepsMultipleLevels(t *testing.T) {
	m, book := newMatcher()
	_, _ = book.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, orderbook.Limit, 64, 3, time.Unix(1, 0)))
	_, _ = book.PlaceOrder(mkOrder("s2", "alice", orderbook.Sell, orderbook.Limit, 65, 5, time.Unix(2, 0)))
	_, _ = book.PlaceOrder(mkOrder("s3", "alice", orderbook.Sell, orderbook.Limit, 66, 5, time.Unix(3, 0)))
	trades, err := m.PlaceOrder(mkOrder("b1", "bob", orderbook.Buy, orderbook.Market, 0, 10, time.Unix(4, 0)))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(trades) != 3 {
		t.Fatalf("want 3 trades (3@64, 5@65, 2@66), got %d", len(trades))
	}
	// Verify quantities across the sweep
	want := []int64{3, 5, 2}
	for i, w := range want {
		if trades[i].Quantity != w {
			t.Errorf("trade %d qty = %d, want %d", i, trades[i].Quantity, w)
		}
	}
}

func TestMarketOrder_PartialFill_BookExhausted(t *testing.T) {
	m, book := newMatcher()
	_, _ = book.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, orderbook.Limit, 64, 3, time.Unix(1, 0)))
	// Bob wants 10, only 3 available → 3 fill, 7 unfulfilled.
	trades, err := m.PlaceOrder(mkOrder("b1", "bob", orderbook.Buy, orderbook.Market, 0, 10, time.Unix(2, 0)))
	if !errors.Is(err, ErrMarketNoLiquidity) {
		t.Fatalf("want ErrMarketNoLiquidity, got %v", err)
	}
	if len(trades) != 1 || trades[0].Quantity != 3 {
		t.Fatalf("want 1 trade of 3, got %+v", trades)
	}
}

func TestMarketOrder_EmptyBook(t *testing.T) {
	m, _ := newMatcher()
	_, err := m.PlaceOrder(mkOrder("b1", "bob", orderbook.Buy, orderbook.Market, 0, 10, time.Unix(1, 0)))
	if !errors.Is(err, ErrMarketNoLiquidity) {
		t.Fatalf("want ErrMarketNoLiquidity, got %v", err)
	}
}

func TestIOC_PartialMatch(t *testing.T) {
	m, book := newMatcher()
	_, _ = book.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, orderbook.Limit, 64, 6, time.Unix(1, 0)))
	// IOC BUY 10 @ 65¢ → 6 fill, 4 cancelled (not rested).
	trades, err := m.PlaceOrder(mkOrder("b1", "bob", orderbook.Buy, orderbook.IOC, 65, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(trades) != 1 || trades[0].Quantity != 6 {
		t.Fatalf("want 1 trade of 6, got %+v", trades)
	}
	if book.GetBestBid() != nil {
		t.Errorf("IOC remainder must NOT rest on book, got %+v", book.GetBestBid())
	}
}

func TestIOC_NoMatch(t *testing.T) {
	m, book := newMatcher()
	_, _ = book.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, orderbook.Limit, 70, 5, time.Unix(1, 0)))
	// IOC BUY 10 @ 65¢ → 0 fill, 0 trades, no error.
	trades, err := m.PlaceOrder(mkOrder("b1", "bob", orderbook.Buy, orderbook.IOC, 65, 10, time.Unix(2, 0)))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("want 0 trades, got %+v", trades)
	}
}

func TestSelfMatchPrevented(t *testing.T) {
	m, book := newMatcher()
	// Alice has an ask resting at 64.
	_, _ = book.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, orderbook.Limit, 64, 10, time.Unix(1, 0)))
	// Alice now sends a buy that would cross her own ask.
	// Expected: no trade, the BUY rests on the bid side at 65.
	trades, err := m.PlaceOrder(mkOrder("b1", "alice", orderbook.Buy, orderbook.Limit, 65, 5, time.Unix(2, 0)))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("want 0 trades (self-match), got %+v", trades)
	}
	if best := book.GetBestAsk(); best == nil || best.Quantity != 10 {
		t.Errorf("alice's resting ask should be untouched, got %+v", best)
	}
	if best := book.GetBestBid(); best == nil || best.Quantity != 5 || best.Price != 65 {
		t.Errorf("alice's buy should rest at @65 qty 5, got %+v", best)
	}
}
