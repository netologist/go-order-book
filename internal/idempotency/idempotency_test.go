package idempotency

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLookup_UnseenKey(t *testing.T) {
	g := NewGuard()
	v, ok := g.Lookup("nope")
	if ok || v != nil {
		t.Errorf("want (nil, false), got (%v, %v)", v, ok)
	}
}

func TestStore_Then_Lookup(t *testing.T) {
	g := NewGuard()
	g.Store("K1", "response-1", time.Minute)
	v, ok := g.Lookup("K1")
	if !ok || v != "response-1" {
		t.Errorf("want (\"response-1\", true), got (%v, %v)", v, ok)
	}
}

func TestLookup_AfterExpiry(t *testing.T) {
	g := NewGuard()
	g.Store("K1", "x", 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	v, ok := g.Lookup("K1")
	if ok || v != nil {
		t.Errorf("want (nil, false) after expiry, got (%v, %v)", v, ok)
	}
}

func TestSweep_RemovesExpired(t *testing.T) {
	g := NewGuard()
	g.Store("a", 1, 1*time.Millisecond)
	g.Store("b", 2, 1*time.Millisecond)
	g.Store("c", 3, time.Hour)
	time.Sleep(10 * time.Millisecond)

	n := g.Sweep()
	if n != 2 {
		t.Errorf("Sweep removed %d, want 2", n)
	}
}

func TestDifferentKeys_Independent(t *testing.T) {
	g := NewGuard()
	g.Store("A", "alpha", time.Minute)
	g.Store("B", "beta", time.Minute)
	if v, _ := g.Lookup("A"); v != "alpha" {
		t.Errorf("A = %v, want alpha", v)
	}
	if v, _ := g.Lookup("B"); v != "beta" {
		t.Errorf("B = %v, want beta", v)
	}
}

func TestConcurrent_StoreLookup(t *testing.T) {
	g := NewGuard()
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	var stored int64

	for range N {
		go func() {
			defer wg.Done()
			if _, ok := g.Lookup("K1"); !ok {
				time.Sleep(time.Millisecond)
				g.Store("K1", "result", time.Minute)
				atomic.AddInt64(&stored, 1)
			}
		}()
	}
	wg.Wait()

	v, ok := g.Lookup("K1")
	if !ok {
		t.Fatal("K1 should be present after concurrent stores")
	}
	if v != "result" {
		t.Errorf("K1 value = %v, want \"result\"", v)
	}
}
