// Package api provides HTTP handlers for the order-book system using
// Go 1.22+ path-pattern routing.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/netologist/go-order-book/internal/events"
	"github.com/netologist/go-order-book/internal/orderbook"
)

// OrderPlacer abstracts order placement (used by the market).
type OrderPlacer interface {
	PlaceOrder(o orderbook.Order) ([]orderbook.Trade, error)
	CancelOrder(id string) error
}

// MarketStater abstracts market lifecycle (state transitions).
type MarketStater interface {
	State() int
	Transition(to int) error
}

// IdempotencyChecker abstracts the idempotency guard.
type IdempotencyChecker interface {
	Lookup(key string) (any, bool)
	Store(key string, response any, ttl time.Duration)
}

// Handlers holds the dependencies for all HTTP handlers.
type Handlers struct {
	Market  OrderPlacer
	State   MarketStater
	Events  *events.EventBus
	Idempot IdempotencyChecker
	Logger  *slog.Logger
}

// PlaceOrderRequest is the JSON body for POST /orders.
type PlaceOrderRequest struct {
	UserID   string `json:"user_id"`
	Side     string `json:"side"`
	Type     string `json:"type"`
	Price    int64  `json:"price,omitempty"`
	Quantity int64  `json:"quantity"`
}

// PlaceOrderResponse is the JSON body returned by POST /orders.
type PlaceOrderResponse struct {
	Trades       []TradeDTO `json:"trades,omitempty"`
	OrderID      string     `json:"order_id,omitempty"`
	RemainingQty int64      `json:"remaining_qty,omitempty"`
}

// TradeDTO is a JSON-safe trade representation.
type TradeDTO struct {
	BuyOrderID  string `json:"buy_order_id"`
	SellOrderID string `json:"sell_order_id"`
	Price       int64  `json:"price"`
	Quantity    int64  `json:"quantity"`
}

func toTradeDTO(t orderbook.Trade) TradeDTO {
	return TradeDTO{
		BuyOrderID:  t.BuyOrderID,
		SellOrderID: t.SellOrderID,
		Price:       t.Price,
		Quantity:    t.Quantity,
	}
}

// Routes registers all HTTP routes on the given mux.
func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /orders", h.PlaceOrder)
	mux.HandleFunc("DELETE /orders/{id}", h.CancelOrder)
	mux.HandleFunc("GET /healthz", h.Healthz)
}

// PlaceOrder handles POST /orders.
func (h *Handlers) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	var req PlaceOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	// Resolve side.
	side, err := parseSide(req.Side)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Resolve order type.
	orderType, err := parseOrderType(req.Type)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Idempotency check.
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" {
		if cached, ok := h.Idempot.Lookup(idempotencyKey); ok {
			writeJSON(w, http.StatusOK, cached)
			return
		}
	}

	o := orderbook.Order{
		ID:        nextID(), // simplified — real system uses UUID
		UserID:    req.UserID,
		Side:      side,
		Type:      orderType,
		Price:     req.Price,
		Quantity:  req.Quantity,
		Timestamp: time.Now(),
	}

	trades, err := h.Market.PlaceOrder(o)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := PlaceOrderResponse{
		OrderID:      o.ID,
		RemainingQty: o.Quantity,
	}
	for _, t := range trades {
		resp.Trades = append(resp.Trades, toTradeDTO(t))
		resp.RemainingQty -= t.Quantity
	}

	// Publish domain events.
	h.Events.Publish(events.Event{
		Type:      events.EventOrderPlaced,
		Timestamp: time.Now(),
		Payload:   o,
	})
	for _, t := range trades {
		h.Events.Publish(events.Event{
			Type:      events.EventTradeExecuted,
			Timestamp: t.Timestamp,
			Payload:   t,
		})
	}

	// Cache for idempotency.
	if idempotencyKey != "" {
		h.Idempot.Store(idempotencyKey, resp, 5*time.Minute)
	}

	writeJSON(w, http.StatusOK, resp)
}

// CancelOrder handles DELETE /orders/{id}.
func (h *Handlers) CancelOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing order id")
		return
	}

	err := h.Market.CancelOrder(id)
	if err != nil {
		if errors.Is(err, orderbook.ErrOrderNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	h.Events.Publish(events.Event{
		Type:      events.EventOrderCancelled,
		Timestamp: time.Now(),
		Payload:   id,
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// Healthz handles GET /healthz.
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func parseSide(s string) (orderbook.Side, error) {
	switch s {
	case "buy":
		return orderbook.Buy, nil
	case "sell":
		return orderbook.Sell, nil
	}
	return 0, errors.New("invalid side: must be 'buy' or 'sell'")
}

func parseOrderType(s string) (orderbook.OrderType, error) {
	switch s {
	case "limit":
		return orderbook.Limit, nil
	case "market":
		return orderbook.Market, nil
	case "ioc":
		return orderbook.IOC, nil
	}
	return 0, errors.New("invalid type: must be 'limit', 'market', or 'ioc'")
}

// Simple monotonic ID generator — real system uses UUID.
var idCounter int64

func nextID() string {
	idCounter++
	return time.Now().Format("150405") + "-" + itoa(idCounter)
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
