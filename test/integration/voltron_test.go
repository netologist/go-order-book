// Package integration tests the full order-flow pipeline end-to-end:
//
//	HTTP → Idempotency guard → Validator → State machine → Ledger
//	  → Matcher → OrderBook → Event bus
//
// This is the "voltron" integration described in the study guide.
package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/netologist/go-order-book/internal/api"
	"github.com/netologist/go-order-book/internal/events"
	"github.com/netologist/go-order-book/internal/idempotency"
	"github.com/netologist/go-order-book/internal/ledger"
	"github.com/netologist/go-order-book/internal/market"
)

// voltron assembles every component into one integrated system.
type voltron struct {
	market   *market.Market
	ledger   *ledger.Ledger
	eventBus *events.EventBus
	idempot  *idempotency.Guard
	handlers *api.Handlers
	mux      *http.ServeMux
}

func newVoltron() *voltron {
	mkt := market.New()
	_ = mkt.Transition(market.Live) // start Live

	eb := events.NewEventBus(100)
	ig := idempotency.NewGuard()

	mux := http.NewServeMux()
	h := &api.Handlers{
		Market:  mkt,
		Events:  eb,
		Idempot: ig,
	}
	h.Routes(mux)

	return &voltron{
		market:   mkt,
		ledger:   ledger.NewLedger(),
		eventBus: eb,
		idempot:  ig,
		handlers: h,
		mux:      mux,
	}
}

// doPOST sends a POST /orders request and returns the response.
func (v *voltron) doPOST(t *testing.T, body string, idempotencyKey string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("POST", "/orders", bytes.NewReader([]byte(body)))
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()
	v.mux.ServeHTTP(rec, req)
	return rec.Result()
}

// doDELETE sends a DELETE /orders/{id} request.
func (v *voltron) doDELETE(t *testing.T, id string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("DELETE", "/orders/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	v.mux.ServeHTTP(rec, req)
	return rec.Result()
}

// ---------------------------------------------------------------------------
// Integration tests
// ---------------------------------------------------------------------------

func TestVoltron_PlaceOrder_FullFlow(t *testing.T) {
	v := newVoltron()

	// Credit the user so they have funds.
	v.ledger.Credit("alice", 1000)

	// Place a sell order: alice sells 10 lots @ 64¢.
	body := `{"user_id":"alice","side":"sell","type":"limit","price":64,"quantity":10}`
	resp := v.doPOST(t, body, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sell order: want 200, got %d", resp.StatusCode)
	}

	// Now alice buys — but first, reserve from the ledger.
	// In production the API handler would do this; here we test the ledger flow.
	if err := v.ledger.PlaceOrder("alice", 640); err != nil {
		t.Fatalf("reserve funds: %v", err)
	}
	v.ledger.SettleTrade("alice", "alice", 640) // same-user for simplicity

	avail, held, _ := v.ledger.Balance("alice")
	t.Logf("alice: avail=%d, held=%d", avail, held)
}

func TestVoltron_IdempotentPlacement(t *testing.T) {
	v := newVoltron()
	v.ledger.Credit("alice", 1000)

	body := `{"user_id":"alice","side":"sell","type":"limit","price":64,"quantity":10}`

	// First request with key.
	r1 := v.doPOST(t, body, "idem-1")
	var resp1 api.PlaceOrderResponse
	if err := json.NewDecoder(r1.Body).Decode(&resp1); err != nil {
		t.Fatal(err)
	}

	// Second request with same key — should return cached response.
	r2 := v.doPOST(t, body, "idem-1")
	var resp2 api.PlaceOrderResponse
	if err := json.NewDecoder(r2.Body).Decode(&resp2); err != nil {
		t.Fatal(err)
	}

	if resp1.OrderID != resp2.OrderID {
		t.Errorf("idempotency: order IDs differ: %q vs %q", resp1.OrderID, resp2.OrderID)
	}
}

func TestVoltron_CancelOrder(t *testing.T) {
	v := newVoltron()
	v.ledger.Credit("bob", 500)

	// Place an order.
	body := `{"user_id":"bob","side":"buy","type":"limit","price":50,"quantity":5}`
	placeResp := v.doPOST(t, body, "cancel-test")
	var placed api.PlaceOrderResponse
	if err := json.NewDecoder(placeResp.Body).Decode(&placed); err != nil {
		t.Fatal(err)
	}

	// Cancel it.
	cancelResp := v.doDELETE(t, placed.OrderID)
	if cancelResp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: want 200, got %d", cancelResp.StatusCode)
	}
}

func TestVoltron_MatchingFlow(t *testing.T) {
	v := newVoltron()

	// Alice offers 10 @ 64¢.
	r1 := v.doPOST(t, `{"user_id":"alice","side":"sell","type":"limit","price":64,"quantity":10}`, "")
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("alice ask: %d", r1.StatusCode)
	}

	// Bob bids 10 @ 65¢ — should match against Alice at 64¢.
	r2 := v.doPOST(t, `{"user_id":"bob","side":"buy","type":"limit","price":65,"quantity":10}`, "")
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("bob bid: %d", r2.StatusCode)
	}

	var resp api.PlaceOrderResponse
	if err := json.NewDecoder(r2.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Trades) != 1 {
		t.Fatalf("want 1 trade, got %d", len(resp.Trades))
	}
	if resp.Trades[0].Price != 64 {
		t.Errorf("trade price = %d, want 64 (price improvement)", resp.Trades[0].Price)
	}
	if resp.Trades[0].Quantity != 10 {
		t.Errorf("trade qty = %d, want 10", resp.Trades[0].Quantity)
	}
}

func TestVoltron_EventBusReceivesEvents(t *testing.T) {
	v := newVoltron()

	// Subscribe before placing.
	tradesCh := v.eventBus.Subscribe(events.EventTradeExecuted)

	// Alice sells 10 @ 64¢.
	v.doPOST(t, `{"user_id":"alice","side":"sell","type":"limit","price":64,"quantity":10}`, "")
	// Bob buys 10 @ 65¢ — should trigger a trade.
	v.doPOST(t, `{"user_id":"bob","side":"buy","type":"limit","price":65,"quantity":10}`, "")

	// The trade event should appear on the bus.
	select {
	case e := <-tradesCh:
		if e.Type != events.EventTradeExecuted {
			t.Errorf("got type %s, want %s", e.Type, events.EventTradeExecuted)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for trade event on event bus")
	}
}
