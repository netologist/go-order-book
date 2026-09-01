package ledger

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
	if err := l.PlaceOrder("alice", 500); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}
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

	l.SettleTrade("alice", "bob", 200)

	availA, heldA, _ := l.Balance("alice")
	availB, heldB, _ := l.Balance("bob")

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

func TestConservationInvariant(t *testing.T) {
	l := NewLedger()
	l.Credit("alice", 1000)
	l.Credit("bob", 1000)
	initial := l.Total()

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
	const per int64 = 50
	var wg sync.WaitGroup
	wg.Add(N)
	var ok, fail int64

	for range N {
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

func TestBalance_UnknownUser(t *testing.T) {
	l := NewLedger()
	_, _, ok := l.Balance("ghost")
	if ok {
		t.Error("expected ok=false for unknown user")
	}
}
