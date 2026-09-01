package orderbook

import (
	"errors"
	"testing"
	"time"
)

func TestSideString(t *testing.T) {
	if got := Buy.String(); got != "buy" {
		t.Errorf("Buy.String() = %q, want %q", got, "buy")
	}
	if got := Sell.String(); got != "sell" {
		t.Errorf("Sell.String() = %q, want %q", got, "sell")
	}
}

func TestOrderTypeString(t *testing.T) {
	cases := []struct {
		in   OrderType
		want string
	}{
		{Limit, "limit"},
		{Market, "market"},
		{IOC, "ioc"},
		{OrderType(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("%d.String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateOrder(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name       string
		order      Order
		wantErr    bool
		wantCauses []error // sentinels expected via errors.Is
	}{
		{
			name: "valid limit order",
			order: Order{
				ID: "o-1", UserID: "u-1", Side: Buy, Type: Limit,
				Price: 60, Quantity: 10, Timestamp: now,
			},
			wantErr: false,
		},
		{
			name: "valid market order without price",
			order: Order{
				ID: "o-1", UserID: "u-1", Side: Buy, Type: Market,
				Price: 0, Quantity: 10, Timestamp: now,
			},
			wantErr: false,
		},
		{
			name: "valid ioc order",
			order: Order{
				ID: "o-1", UserID: "u-1", Side: Sell, Type: IOC,
				Price: 40, Quantity: 5, Timestamp: now,
			},
			wantErr: false,
		},
		{
			name: "missing id",
			order: Order{
				UserID: "u-1", Side: Buy, Type: Limit, Price: 60, Quantity: 10, Timestamp: now,
			},
			wantErr:    true,
			wantCauses: []error{ErrInvalidOrderID},
		},
		{
			name: "missing user id",
			order: Order{
				ID: "o-1", Side: Buy, Type: Limit, Price: 60, Quantity: 10, Timestamp: now,
			},
			wantErr:    true,
			wantCauses: []error{ErrInvalidUserID},
		},
		{
			name: "zero quantity",
			order: Order{
				ID: "o-1", UserID: "u-1", Side: Buy, Type: Limit, Price: 60, Quantity: 0, Timestamp: now,
			},
			wantErr:    true,
			wantCauses: []error{ErrInvalidQuantity},
		},
		{
			name: "negative quantity",
			order: Order{
				ID: "o-1", UserID: "u-1", Side: Buy, Type: Limit, Price: 60, Quantity: -3, Timestamp: now,
			},
			wantErr:    true,
			wantCauses: []error{ErrInvalidQuantity},
		},
		{
			name: "price above 100",
			order: Order{
				ID: "o-1", UserID: "u-1", Side: Buy, Type: Limit, Price: 150, Quantity: 10, Timestamp: now,
			},
			wantErr:    true,
			wantCauses: []error{ErrInvalidPrice},
		},
		{
			name: "price below 1 for limit",
			order: Order{
				ID: "o-1", UserID: "u-1", Side: Buy, Type: Limit, Price: 0, Quantity: 10, Timestamp: now,
			},
			wantErr:    true,
			wantCauses: []error{ErrLimitOrderMissingPrice},
		},
		{
			name: "market order with price",
			order: Order{
				ID: "o-1", UserID: "u-1", Side: Buy, Type: Market, Price: 60, Quantity: 10, Timestamp: now,
			},
			wantErr:    true,
			wantCauses: []error{ErrMarketOrderWithPrice},
		},
		{
			name: "future timestamp",
			order: Order{
				ID: "o-1", UserID: "u-1", Side: Buy, Type: Limit, Price: 60, Quantity: 10,
				Timestamp: now.Add(2 * time.Hour),
			},
			wantErr: true,
		},
		{
			name: "multiple errors at once",
			order: Order{
				Side: Buy, Type: Limit, Price: 0, Quantity: -1,
			},
			wantErr:    true,
			wantCauses: []error{ErrInvalidOrderID, ErrInvalidUserID, ErrInvalidQuantity, ErrLimitOrderMissingPrice},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOrder(tc.order)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if err == nil {
				return
			}
			// Must be a *ValidationErrors for the rich test cases.
			var ve *ValidationErrors
			if !errors.As(err, &ve) {
				t.Fatalf("expected *ValidationErrors, got %T (%v)", err, err)
			}
			for _, want := range tc.wantCauses {
				if !errors.Is(err, want) {
					t.Errorf("expected errors.Is(err, %v) to be true; got errors: %v", want, ve.Errors)
				}
			}
		})
	}
}

func TestValidationErrorsIsMultiError(t *testing.T) {
	// errors.Is should find ErrInvalidPrice inside ValidationErrors.
	ve := &ValidationErrors{}
	ve.Add("price", "out_of_range", "bad", ErrInvalidPrice)
	if !errors.Is(ve, ErrInvalidPrice) {
		t.Fatal("expected errors.Is to find ErrInvalidPrice inside ValidationErrors")
	}
	if errors.Is(ve, ErrInvalidUserID) {
		t.Fatal("did not expect ErrInvalidUserID match")
	}
}
