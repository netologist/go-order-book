package phase5

import (
	"errors"
	"testing"
	"time"

	"github.com/netologist/go-order-book/exercises/phase4-concurrency"
	"github.com/netologist/go-order-book/internal/orderbook"
)

func newMarket() *Market {
	return &Market{book: &phase4.OrderBook{}}
}

func mkOrder(id, user string, side orderbook.Side, price, qty int64) orderbook.Order {
	return orderbook.Order{
		ID: id, UserID: user, Side: side, Type: orderbook.Limit,
		Price: price, Quantity: qty, Timestamp: time.Now(),
	}
}

func TestTransition_Valid(t *testing.T) {
	m := newMarket()
	cases := []struct{ from, to MarketState }{
		{Open, Live},
		{Live, Paused},
		{Paused, Live},
		{Live, Settled},
		{Paused, Settled},
	}
	for _, c := range cases {
		m.state = c.from
		if err := m.Transition(c.to); err != nil {
			t.Errorf("%s → %s: want ok, got %v", c.from, c.to, err)
		}
		if m.State() != c.to {
			t.Errorf("after transition, state = %s, want %s", m.State(), c.to)
		}
	}
}

func TestTransition_Invalid(t *testing.T) {
	cases := []struct{ from, to MarketState }{
		{Open, Paused},
		{Open, Settled},
		{Paused, Open},
		{Settled, Live},
		{Settled, Open},
		{Settled, Paused},
	}
	for _, c := range cases {
		m := newMarket()
		m.state = c.from
		if err := m.Transition(c.to); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("%s → %s: want ErrInvalidTransition, got %v", c.from, c.to, err)
		}
		if m.State() != c.from {
			t.Errorf("state changed despite invalid transition: now %s", m.State())
		}
	}
}

func TestPlaceOrder_WhenLive(t *testing.T) {
	m := newMarket()
	if err := m.Transition(Live); err != nil {
		t.Fatal(err)
	}
	trades, err := m.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, 64, 10))
	if err != nil {
		t.Fatalf("Live market should accept orders, got %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("no opposite side, want 0 trades, got %+v", trades)
	}
}

func TestPlaceOrder_WhenPaused(t *testing.T) {
	m := newMarket()
	_ = m.Transition(Live)
	_ = m.Transition(Paused)
	_, err := m.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, 64, 10))
	if !errors.Is(err, ErrMarketNotLive) {
		t.Fatalf("want ErrMarketNotLive, got %v", err)
	}
}

func TestPlaceOrder_WhenSettled(t *testing.T) {
	m := newMarket()
	_ = m.Transition(Live)
	_ = m.Transition(Settled)
	_, err := m.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, 64, 10))
	if !errors.Is(err, ErrMarketNotLive) {
		t.Fatalf("want ErrMarketNotLive, got %v", err)
	}
}

func TestPlaceOrder_WhenOpen(t *testing.T) {
	m := newMarket()
	_, err := m.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, 64, 10))
	if !errors.Is(err, ErrMarketNotLive) {
		t.Fatalf("Open market should reject orders, got %v", err)
	}
}

func TestCancelOrder_WhenSettled(t *testing.T) {
	m := newMarket()
	_ = m.Transition(Live)
	if _, err := m.PlaceOrder(mkOrder("s1", "alice", orderbook.Sell, 64, 10)); err != nil {
		t.Fatal(err)
	}
	_ = m.Transition(Paused)
	if err := m.CancelOrder("s1"); err != nil {
		t.Fatalf("Paused should still allow cancel, got %v", err)
	}
	_ = m.Transition(Live)
	if _, err := m.PlaceOrder(mkOrder("s2", "alice", orderbook.Sell, 64, 10)); err != nil {
		t.Fatal(err)
	}
	_ = m.Transition(Settled)
	if err := m.CancelOrder("s2"); !errors.Is(err, ErrMarketNotLive) {
		t.Fatalf("Settled must reject cancel, got %v", err)
	}
}

func TestState_String(t *testing.T) {
	cases := map[MarketState]string{
		Open:    "open",
		Live:    "live",
		Paused:  "paused",
		Settled: "settled",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", s, got, want)
		}
	}
}
