package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/netologist/go-order-book/internal/events"
	"github.com/netologist/go-order-book/internal/orderbook"
)

// ---------------------------------------------------------------------------
// fakes for testing
// ---------------------------------------------------------------------------

type fakeMarket struct {
	mu      sync.Mutex
	orders  []orderbook.Order
	cancels []string
	err     error
}

func (m *fakeMarket) PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	m.orders = append(m.orders, o)
	return nil, nil
}

func (m *fakeMarket) CancelOrder(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.cancels = append(m.cancels, id)
	return nil
}

type fakeState struct {
	state int
}

func (s *fakeState) State() int              { return s.state }
func (s *fakeState) Transition(to int) error { return nil }

type fakeIdempot struct {
	mu   sync.Mutex
	data map[string]any
}

func (g *fakeIdempot) Lookup(key string) (any, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.data[key]
	return v, ok
}

func (g *fakeIdempot) Store(key string, response any, ttl time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.data == nil {
		g.data = make(map[string]any)
	}
	g.data[key] = response
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

func TestPlaceOrder_Success(t *testing.T) {
	h := &Handlers{
		Market:  &fakeMarket{},
		Events:  events.NewEventBus(10),
		Idempot: &fakeIdempot{},
	}

	body := `{"user_id":"alice","side":"buy","type":"limit","price":50,"quantity":10}`
	req := httptest.NewRequest("POST", "/orders", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()

	h.PlaceOrder(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp PlaceOrderResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OrderID == "" {
		t.Error("expected non-empty order_id")
	}
}

func TestPlaceOrder_InvalidJSON(t *testing.T) {
	h := &Handlers{
		Market:  &fakeMarket{err: errors.New("bad request")},
		Events:  events.NewEventBus(10),
		Idempot: &fakeIdempot{},
	}

	req := httptest.NewRequest("POST", "/orders", bytes.NewReader([]byte("{bad json")))
	rec := httptest.NewRecorder()

	h.PlaceOrder(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

func TestPlaceOrder_InvalidSide(t *testing.T) {
	h := &Handlers{
		Market:  &fakeMarket{},
		Events:  events.NewEventBus(10),
		Idempot: &fakeIdempot{},
	}

	body := `{"user_id":"alice","side":"invalid","type":"limit","price":50,"quantity":10}`
	req := httptest.NewRequest("POST", "/orders", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()

	h.PlaceOrder(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPlaceOrder_Idempotency(t *testing.T) {
	idempot := &fakeIdempot{}
	h := &Handlers{
		Market:  &fakeMarket{},
		Events:  events.NewEventBus(10),
		Idempot: idempot,
	}

	body := `{"user_id":"alice","side":"buy","type":"limit","price":50,"quantity":10}`

	// First request.
	req1 := httptest.NewRequest("POST", "/orders", bytes.NewReader([]byte(body)))
	req1.Header.Set("Idempotency-Key", "key-1")
	rec1 := httptest.NewRecorder()
	h.PlaceOrder(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: want 200, got %d", rec1.Code)
	}

	// Second request with same key — should be cached.
	req2 := httptest.NewRequest("POST", "/orders", bytes.NewReader([]byte(body)))
	req2.Header.Set("Idempotency-Key", "key-1")
	rec2 := httptest.NewRecorder()
	h.PlaceOrder(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second request: want 200, got %d", rec2.Code)
	}

	// The response order ID should be identical (cached).
	var r1, r2 PlaceOrderResponse
	json.NewDecoder(rec1.Body).Decode(&r1)
	json.NewDecoder(rec2.Body).Decode(&r2)
	if r1.OrderID != r2.OrderID {
		t.Errorf("idempotency: order IDs differ: %q vs %q", r1.OrderID, r2.OrderID)
	}
}

func TestCancelOrder_Success(t *testing.T) {
	m := &fakeMarket{}
	h := &Handlers{
		Market:  m,
		Events:  events.NewEventBus(10),
		Idempot: &fakeIdempot{},
	}

	req := httptest.NewRequest("DELETE", "/orders/o-1", nil)
	req.SetPathValue("id", "o-1")
	rec := httptest.NewRecorder()

	h.CancelOrder(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(m.cancels) != 1 || m.cancels[0] != "o-1" {
		t.Errorf("cancels = %v, want [o-1]", m.cancels)
	}
}

func TestCancelOrder_NotFound(t *testing.T) {
	h := &Handlers{
		Market:  &fakeMarket{err: orderbook.ErrOrderNotFound},
		Events:  events.NewEventBus(10),
		Idempot: &fakeIdempot{},
	}

	req := httptest.NewRequest("DELETE", "/orders/ghost", nil)
	req.SetPathValue("id", "ghost")
	rec := httptest.NewRecorder()

	h.CancelOrder(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	h := &Handlers{}

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	h.Healthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}
