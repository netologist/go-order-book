package orderbook

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sentinel errors. Callers can match with errors.Is.
//
// These are the leaf causes — the constructor wraps one or more of them in
// ValidationErrors so a single PlaceOrder call can return every problem at
// once.
var (
	ErrInvalidOrderID         = errors.New("invalid order id")
	ErrInvalidUserID          = errors.New("invalid user id")
	ErrInvalidQuantity        = errors.New("invalid quantity")
	ErrInvalidPrice           = errors.New("invalid price")
	ErrMarketOrderWithPrice   = errors.New("market order must not carry a price")
	ErrLimitOrderMissingPrice = errors.New("limit/ioc order requires a price")
	ErrOrderNotFound          = errors.New("order not found")
)

// FieldError describes a single validation problem in a structured way.
// Used inside ValidationErrors so the HTTP layer can render
// `{"errors":[{"field":"price","code":"out_of_range","message":"..."}]}`.
//
// Cause is a sentinel error (e.g. ErrInvalidPrice) so callers can use
// errors.Is to discriminate.
type FieldError struct {
	Field   string
	Code    string
	Message string
	Cause   error
}

func (e FieldError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s (%s)", e.Field, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s (%s)", e.Field, e.Message, e.Code)
}

// Unwrap exposes the cause so errors.Is can match sentinels.
func (e FieldError) Unwrap() error { return e.Cause }

// ValidationErrors aggregates one or more FieldError values. A caller can
// still match the underlying cause with errors.Is — both work.
//
// Usage:
//
//	if err := ob.PlaceOrder(o); err != nil {
//	    var ve *orderbook.ValidationErrors
//	    if errors.As(err, &ve) {
//	        for _, fe := range ve.Errors { ... }
//	    }
//	}
type ValidationErrors struct {
	Errors []FieldError
}

func (v *ValidationErrors) Error() string {
	if v == nil || len(v.Errors) == 0 {
		return "validation failed"
	}
	parts := make([]string, len(v.Errors))
	for i, e := range v.Errors {
		parts[i] = e.Error()
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// Unwrap lets errors.Is traverse into the FieldError list, which in turn
// unwraps to the sentinel cause.
func (v *ValidationErrors) Unwrap() []error {
	if v == nil {
		return nil
	}
	out := make([]error, 0, len(v.Errors))
	for _, e := range v.Errors {
		if e.Cause != nil {
			out = append(out, e.Cause)
		} else {
			out = append(out, e)
		}
	}
	return out
}

// Add appends a FieldError with a sentinel cause so callers can use
// errors.Is(err, ErrInvalidPrice) to discriminate.
func (v *ValidationErrors) Add(field, code, message string, cause error) {
	v.Errors = append(v.Errors, FieldError{
		Field:   field,
		Code:    code,
		Message: message,
		Cause:   cause,
	})
}

// ValidateOrder returns nil if o is acceptable, otherwise a *ValidationErrors
// describing every problem. It never mutates o.
//
// Rules (prediction market):
//   - id and userID must be non-empty
//   - quantity must be > 0
//   - price, if present, must be in [1, 100] cents
//   - market orders must not carry a price
//   - limit and ioc orders must carry a price
//   - timestamp, if non-zero, must not be in the future
func ValidateOrder(o Order) error {
	var v ValidationErrors

	if strings.TrimSpace(o.ID) == "" {
		v.Add("id", "required", "order id must be non-empty", ErrInvalidOrderID)
	}
	if strings.TrimSpace(o.UserID) == "" {
		v.Add("user_id", "required", "user id must be non-empty", ErrInvalidUserID)
	}
	if o.Quantity <= 0 {
		v.Add("quantity", "non_positive", fmt.Sprintf("quantity must be > 0, got %d", o.Quantity), ErrInvalidQuantity)
	}

	switch o.Type {
	case Market:
		if o.Price != 0 {
			v.Add("price", "not_allowed", "market order must not carry a price", ErrMarketOrderWithPrice)
		}
	case Limit, IOC:
		if o.Price == 0 {
			v.Add("price", "required", "limit/ioc order requires a price > 0", ErrLimitOrderMissingPrice)
		} else if o.Price < 1 || o.Price > 100 {
			v.Add("price", "out_of_range", fmt.Sprintf("price must be 1..100 cents, got %d", o.Price), ErrInvalidPrice)
		}
	default:
		v.Add("type", "unknown", fmt.Sprintf("unknown order type %d", o.Type), ErrInvalidOrderID)
	}

	if !o.Timestamp.IsZero() && o.Timestamp.After(time.Now().Add(time.Minute)) {
		v.Add("timestamp", "future", "timestamp is in the future", ErrInvalidOrderID)
	}

	if len(v.Errors) == 0 {
		return nil
	}
	return &v
}
