package phase4

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netologist/go-order-book/internal/orderbook"
)

func mkOrder(id, user string, side orderbook.Side, price, qty int64) orderbook.Order {
	return orderbook.Order{
		ID: id, UserID: user, Side: side, Type: orderbook.Limit,
		Price: price, Quantity: qty, Timestamp: time.Now(),
	}
}

func TestConcurrent_PlaceOrders_NoRace(t *testing.T) {
	ob := &OrderBook{}
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			// Half buys, half sells, with non-crossing prices so they all rest.
			if i%2 == 0 {
				_, _ = ob.PlaceOrder(mkOrder("b-"+itoa(i), "u", orderbook.Buy, 50, 1))
			} else {
				_, _ = ob.PlaceOrder(mkOrder("s-"+itoa(i), "u", orderbook.Sell, 80, 1))
			}
		}(i)
	}
	wg.Wait()
}

func TestConcurrent_CancelAndPlace(t *testing.T) {
	ob := &OrderBook{}
	// Seed 50 orders to cancel.
	for i := 0; i < 50; i++ {
		_, _ = ob.PlaceOrder(mkOrder("s-"+itoa(i), "u", orderbook.Sell, 70, 1))
	}
	const N = 50
	var wg sync.WaitGroup
	wg.Add(2 * N)
	var placed, cancelled int64
	for i := 0; i < N; i++ {
		// Canceller
		go func(i int) {
			defer wg.Done()
			if err := ob.CancelOrder("s-" + itoa(i)); err == nil {
				atomic.AddInt64(&cancelled, 1)
			}
		}(i)
		// Placer (non-crossing so it rests)
		go func(i int) {
			defer wg.Done()
			if _, err := ob.PlaceOrder(mkOrder("b-"+itoa(i), "u", orderbook.Buy, 60, 1)); err == nil {
				atomic.AddInt64(&placed, 1)
			}
		}(i)
	}
	wg.Wait()
	if placed != N {
		t.Errorf("placed = %d, want %d", placed, N)
	}
	if cancelled != N {
		t.Errorf("cancelled = %d, want %d", cancelled, N)
	}
}

func TestConcurrent_ReadersAndWriters(t *testing.T) {
	ob := &OrderBook{}
	// Seed some orders so GetBestBid/Ask are non-trivial.
	for i := 0; i < 20; i++ {
		_, _ = ob.PlaceOrder(mkOrder("s-"+itoa(i), "u", orderbook.Sell, int64(60+i), 1))
	}
	const R = 100
	const W = 20
	var wg sync.WaitGroup
	wg.Add(R + W)
	for i := 0; i < R; i++ {
		go func() {
			defer wg.Done()
			_ = ob.GetBestBid()
			_ = ob.GetBestAsk()
		}()
	}
	for i := 0; i < W; i++ {
		go func(i int) {
			defer wg.Done()
			_, _ = ob.PlaceOrder(mkOrder("s-w-"+itoa(i), "u", orderbook.Sell, int64(60+i%5), 1))
		}(i)
	}
	wg.Wait()
}

func TestStress_HighVolume(t *testing.T) {
	ob := &OrderBook{}
	const G = 10
	const PerG = 100
	var wg sync.WaitGroup
	wg.Add(G)
	for g := 0; g < G; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < PerG; i++ {
				if i%2 == 0 {
					_, _ = ob.PlaceOrder(mkOrder("b", "u", orderbook.Buy, 50, 1))
				} else {
					_, _ = ob.PlaceOrder(mkOrder("s", "u", orderbook.Sell, 80, 1))
				}
			}
		}(g)
	}
	wg.Wait()
}

// itoa is a tiny helper to avoid pulling in strconv just for test logs.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
