package activity

import (
	"errors"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestFeeActivityRequiresExplicitCurrencyUnitID(t *testing.T) {
	activity := NewFeeActivities(&walletstore.Store{})
	_, err := activity.CalculateFee(t.Context(), "tenant", "deposit", "USD", 0, 100)
	if !errors.Is(err, walletstore.ErrMissingCurrencyUnitID) {
		t.Fatalf("CalculateFee() error = %v, want %v", err, walletstore.ErrMissingCurrencyUnitID)
	}
}

func TestRateActivityRequiresExplicitCurrencyUnitIDs(t *testing.T) {
	activity := NewRateActivities(&walletstore.Store{})
	_, err := activity.ConvertCurrency(t.Context(), "tenant", 100, "USD", 0, "EUR", 2)
	if !errors.Is(err, walletstore.ErrMissingCurrencyUnitID) {
		t.Fatalf("ConvertCurrency() error = %v, want %v", err, walletstore.ErrMissingCurrencyUnitID)
	}
}
