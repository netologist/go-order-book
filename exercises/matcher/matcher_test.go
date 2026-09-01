package matcher

import (
	"sync"
	"testing"
)

// ═══════════════════════════════════════════════════════════════════════════
// INTERVIEW SIMULATION: Test Suite
//
// Bunları SIRAYLA yazacaksın. Her testi yaz, çalıştır, FAIL gör, fix'le.
//
// Çalıştır:
//
//	go test -race -v ./exercises/matcher/...
//
// ═══════════════════════════════════════════════════════════════════════════

// Söyleyeceğin cümle:
// "Before I start fixing anything, let me write a few tests to lock in the
//  expected behavior. That way I know I'm not breaking anything."

// ---------------------------------------------------------------------------
// Test 1: Happy Path — Çalışıyor mu? (BROKEN: geçer veya seller balance yanlış)
// ---------------------------------------------------------------------------

func TestPlaceOrder_BasicMatch(t *testing.T) {
	m := NewMatcher()
	m.SetBalance("alice", 1000)
	m.SetBalance("bob", 1000)

	// Bob 5 lot satıyor @ 10
	m.PlaceOrder(Order{ID: "s1", TraderID: "bob", Side: Sell, Price: 10, Quantity: 5})

	// Alice 5 lot alıyor @ 10 → tam eşleşmeli
	trades, err := m.PlaceOrder(Order{ID: "b1", TraderID: "alice", Side: Buy, Price: 10, Quantity: 5})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	if trades[0].Quantity != 5 {
		t.Errorf("trade qty = %d, want 5", trades[0].Quantity)
	}
}

// ---------------------------------------------------------------------------
// Test 2: Partial Fill — Kısmi eşleşme
// ---------------------------------------------------------------------------

func TestPlaceOrder_PartialFill(t *testing.T) {
	m := NewMatcher()
	m.SetBalance("alice", 1000)
	m.SetBalance("bob", 1000)

	m.PlaceOrder(Order{ID: "s1", TraderID: "bob", Side: Sell, Price: 10, Quantity: 3})

	trades, err := m.PlaceOrder(Order{ID: "b1", TraderID: "alice", Side: Buy, Price: 10, Quantity: 5})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if trades[0].Quantity != 3 {
		t.Errorf("trade qty = %d, want 3", trades[0].Quantity)
	}

	orders := m.GetOrders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 resting order, got %d", len(orders))
	}
	if orders[0].Quantity != 2 {
		t.Errorf("resting qty = %d, want 2", orders[0].Quantity)
	}
}

// ---------------------------------------------------------------------------
// Test 3: Price Priority — En iyi fiyat önce eşleşsin
// ---------------------------------------------------------------------------

func TestPlaceOrder_PricePriority(t *testing.T) {
	m := NewMatcher()
	m.SetBalance("alice", 1000)
	m.SetBalance("bob", 1000)
	m.SetBalance("charlie", 1000)

	// Charlie @ 9 (ucuz), Bob @ 10
	m.PlaceOrder(Order{ID: "c1", TraderID: "charlie", Side: Sell, Price: 9, Quantity: 5})
	m.PlaceOrder(Order{ID: "s1", TraderID: "bob", Side: Sell, Price: 10, Quantity: 5})

	// Alice alıyor @ 10 → Charlie'nin 9'luk satışıyla eşleşmeli
	trades, err := m.PlaceOrder(Order{ID: "b1", TraderID: "alice", Side: Buy, Price: 10, Quantity: 5})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if trades[0].Price != 9 {
		t.Errorf("trade price = %.0f, want 9 (cheapest first)", trades[0].Price)
	}
}

// ---------------------------------------------------------------------------
// Test 4: 🔴 CONCURRENCY — Data race
//
// BROKEN: go test -race → FATAL: concurrent map writes
// Interview'da: "Let me test this with the race detector..."
// ---------------------------------------------------------------------------

func TestPlaceOrder_Concurrent(t *testing.T) {
	m := NewMatcher()
	m.SetBalance("alice", 10000)
	m.SetBalance("bob", 10000)

	var wg sync.WaitGroup
	n := 10

	m.PlaceOrder(Order{ID: "s0", TraderID: "bob", Side: Sell, Price: 10, Quantity: int64(n)})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			o := Order{
				ID:       "buy" + string(rune('a'+id)),
				TraderID: "alice",
				Side:     Buy,
				Price:    10,
				Quantity: 1,
			}
			_, _ = m.PlaceOrder(o)
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Test 5: 🟡 IDEMPOTENCY — Aynı order ID iki kere
//
// BROKEN: double-execution → trader çift işlem yapar.
// Fix sonrası: second call returns same result, no double debit.
// ---------------------------------------------------------------------------

func TestPlaceOrder_Duplicate(t *testing.T) {
	m := NewMatcher()
	m.SetBalance("alice", 1000)
	m.SetBalance("bob", 1000)

	m.PlaceOrder(Order{ID: "s1", TraderID: "bob", Side: Sell, Price: 10, Quantity: 5})

	_, err := m.PlaceOrder(Order{ID: "b1", TraderID: "alice", Side: Buy, Price: 10, Quantity: 3})
	if err != nil {
		t.Fatalf("first PlaceOrder: %v", err)
	}

	// AYNI order ID → idempotent olmalı.
	_, err = m.PlaceOrder(Order{ID: "b1", TraderID: "alice", Side: Buy, Price: 10, Quantity: 3})
	if err != nil {
		t.Fatalf("duplicate PlaceOrder: %v", err)
	}

	// Alice'in bakiyesi sadece 30 azalmalı (double debit → 60 olurdu).
	bal := m.GetBalance("alice")
	if bal != 1000-30 {
		t.Errorf("alice balance = %d, want %d (idempotency failed — double debit?)", bal, 1000-30)
	}
}

// ---------------------------------------------------------------------------
// Test 6: 🟡 SELF-MATCH — Kendi kendine trade
//
// BROKEN: Self-match yapar → 1 trade döner.
// Fix: 0 trade dönmeli.
// ---------------------------------------------------------------------------

func TestPlaceOrder_SelfMatch(t *testing.T) {
	m := NewMatcher()
	m.SetBalance("alice", 1000)

	m.PlaceOrder(Order{ID: "s1", TraderID: "alice", Side: Sell, Price: 10, Quantity: 5})

	trades, err := m.PlaceOrder(Order{ID: "b1", TraderID: "alice", Side: Buy, Price: 10, Quantity: 5})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("got %d trades, want 0 (should not self-match)", len(trades))
	}
}

// ---------------------------------------------------------------------------
// Test 7: 🔴 Insufficient Balance — Yetersiz bakiye
//
// BROKEN: State bozulur (order'lar çoktan eşleşmiş).
// Fix: Hata dönülür, state bozulmamış olur.
// ---------------------------------------------------------------------------

func TestPlaceOrder_InsufficientBalance(t *testing.T) {
	m := NewMatcher()
	m.SetBalance("alice", 10)
	m.SetBalance("bob", 1000)

	m.PlaceOrder(Order{ID: "s1", TraderID: "bob", Side: Sell, Price: 10, Quantity: 5})

	_, err := m.PlaceOrder(Order{ID: "b1", TraderID: "alice", Side: Buy, Price: 10, Quantity: 5})

	if err == nil {
		t.Error("expected error for insufficient balance")
	}

	// State bozulmamış olmalı — Bob'un order'ı hala book'ta.
	orders := m.GetOrders()
	if len(orders) != 1 {
		t.Errorf("expected 1 resting order (state corrupted?), got %d", len(orders))
	}
}

// ---------------------------------------------------------------------------
// Test 8: Edge Cases — Boş ID, sıfır/negatif quantity
// ---------------------------------------------------------------------------

func TestPlaceOrder_EdgeCases(t *testing.T) {
	m := NewMatcher()

	_, err := m.PlaceOrder(Order{ID: "", TraderID: "alice", Side: Buy, Price: 10, Quantity: 1})
	if err == nil {
		t.Error("expected error for empty ID")
	}

	_, err = m.PlaceOrder(Order{ID: "b1", TraderID: "alice", Side: Buy, Price: 10, Quantity: 0})
	if err == nil {
		t.Error("expected error for zero quantity")
	}

	_, err = m.PlaceOrder(Order{ID: "b2", TraderID: "alice", Side: Buy, Price: 10, Quantity: -5})
	if err == nil {
		t.Error("expected error for negative quantity")
	}
}

// ---------------------------------------------------------------------------
// Test 9: Empty Book — Eşleşmesiz order
// ---------------------------------------------------------------------------

func TestPlaceOrder_NoMatch(t *testing.T) {
	m := NewMatcher()
	m.SetBalance("alice", 1000)

	trades, err := m.PlaceOrder(Order{ID: "b1", TraderID: "alice", Side: Buy, Price: 10, Quantity: 5})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("expected 0 trades (book was empty), got %d", len(trades))
	}

	orders := m.GetOrders()
	if len(orders) != 1 {
		t.Errorf("expected 1 resting order, got %d", len(orders))
	}
}

// ---------------------------------------------------------------------------
// Test 10: Multi-Match — Birden fazla eşleşme
// ---------------------------------------------------------------------------

func TestPlaceOrder_MultiMatch(t *testing.T) {
	m := NewMatcher()
	m.SetBalance("alice", 1000)
	m.SetBalance("bob", 1000)
	m.SetBalance("charlie", 1000)

	// İki satıcı: Bob 3 lot @ 10, Charlie 3 lot @ 10
	m.PlaceOrder(Order{ID: "s1", TraderID: "bob", Side: Sell, Price: 10, Quantity: 3})
	m.PlaceOrder(Order{ID: "s2", TraderID: "charlie", Side: Sell, Price: 10, Quantity: 3})

	// Alice 5 lot alıyor @ 10 → 2 trade (3+2)
	trades, err := m.PlaceOrder(Order{ID: "b1", TraderID: "alice", Side: Buy, Price: 10, Quantity: 5})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}

	totalQty := int64(0)
	for _, t := range trades {
		totalQty += t.Quantity
	}
	if totalQty != 5 {
		t.Errorf("total matched = %d, want 5", totalQty)
	}
}
