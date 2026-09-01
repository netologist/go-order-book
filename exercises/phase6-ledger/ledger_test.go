package phase6

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCredit_BalanceIncreases(t *testing.T) {
	l := NewLedger()
	l.Credit("alice", 1000)
	avail, held, ok := l.Balance("alice")
	if !ok || avail != 1000 || held != 0 {
		t.Fatalf("got (%d, %d, %v), want (1000, 0, true)", avail, held, ok)
	}
}

func TestPlaceOrder_HoldIncreases(t *testing.T) {
	l := NewLedger()
	l.Credit("alice", 1000)
	if err := l.PlaceOrder("alice", 300); err != nil {
		t.Fatal(err)
	}
	avail, held, _ := l.Balance("alice")
	if avail != 1000 || held != 300 {
		t.Errorf("after hold 300, got (avail=%d, held=%d), want (1000, 300)", avail, held)
	}
}

func TestPlaceOrder_InsufficientFunds(t *testing.T) {
	l := NewLedger()
	l.Credit("alice", 1000)
	if err := l.PlaceOrder("alice", 700); err != nil {
		t.Fatal(err)
	}
	// Now alice has 300 free. Try to reserve 500.
	if err := l.PlaceOrder("alice", 500); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}
	// State should be unchanged.
	avail, held, _ := l.Balance("alice")
	if avail != 1000 || held != 700 {
		t.Errorf("state changed on rejection: got (avail=%d, held=%d), want (1000, 700)", avail, held)
	}
}

func TestSettleTrade_TransfersCorrectly(t *testing.T) {
	l := NewLedger()
	l.Credit("alice", 1000)
	l.Credit("bob", 500)
	if err := l.PlaceOrder("alice", 600); err != nil {
		t.Fatal(err)
	}
	// Trade: alice buys 200 from bob at some price (cost = price*qty, but
	// the ledger just sees a total amount). Settle 200.
	l.SettleTrade("alice", "bob", 200)
	availA, heldA, _ := l.Balance("alice")
	availB, heldB, _ := l.Balance("bob")
	// alice: held -200, available -200  →  (800, 400)
	// bob:   available +200              →  (700, 0)
	if availA != 800 || heldA != 400 {
		t.Errorf("alice: got (%d, %d), want (800, 400)", availA, heldA)
	}
	if availB != 700 || heldB != 0 {
		t.Errorf("bob: got (%d, %d), want (700, 0)", availB, heldB)
	}
}

func TestRefund_ReleasesHold(t *testing.T) {
	l := NewLedger()
	l.Credit("alice", 1000)
	if err := l.PlaceOrder("alice", 300); err != nil {
		t.Fatal(err)
	}
	l.Refund("alice", 300)
	avail, held, _ := l.Balance("alice")
	if avail != 1000 || held != 0 {
		t.Errorf("after full refund: got (%d, %d), want (1000, 0)", avail, held)
	}
}

func TestConservationInvariant_After100Operations(t *testing.T) {
	l := NewLedger()
	l.Credit("alice", 1000)
	l.Credit("bob", 1000)
	initial := l.Total() // 2000

	// 100 mixed operations
	for i := 0; i < 100; i++ {
		_ = l.PlaceOrder("alice", 1)
	}
	for i := 0; i < 50; i++ {
		l.SettleTrade("alice", "bob", 1)
	}
	for i := 0; i < 50; i++ {
		l.Refund("alice", 1)
	}
	if got := l.Total(); got != initial {
		t.Errorf("Total = %d, want %d (conservation violated)", got, initial)
	}
}

func TestConcurrent_NoDoubleSpend(t *testing.T) {
	l := NewLedger()
	l.Credit("alice", 1000)
	const N = 100
	const per = 50 // each goroutine tries to reserve 50; only 20 should succeed.
	var wg sync.WaitGroup
	wg.Add(N)
	var ok, fail int64
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if err := l.PlaceOrder("alice", per); err == nil {
				atomic.AddInt64(&ok, 1)
			} else {
				atomic.AddInt64(&fail, 1)
			}
		}()
	}
	wg.Wait()
	// 1000 / 50 = 20 reservations max.
	if ok != 20 {
		t.Errorf("ok = %d, want 20", ok)
	}
	if fail != 80 {
		t.Errorf("fail = %d, want 80", fail)
	}
	avail, held, _ := l.Balance("alice")
	if avail != 1000 || held != 1000 {
		t.Errorf("final state: (avail=%d, held=%d), want (1000, 1000)", avail, held)
	}
	if l.Total() != 2000 {
		t.Errorf("Total = %d, want 2000", l.Total())
	}
}
