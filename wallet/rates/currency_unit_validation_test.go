package rates

import (
	"errors"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestConvertDistinguishesMissingAndInvalidCurrencyUnitIDs(t *testing.T) {
	service := &Service{Store: &walletstore.Store{}}
	tests := []struct {
		name       string
		fromUnitID int64
		toUnitID   int64
		want       error
	}{
		{name: "missing base unit", toUnitID: 2, want: walletstore.ErrMissingCurrencyUnitID},
		{name: "invalid base unit", fromUnitID: -1, toUnitID: 2, want: walletstore.ErrInvalidCurrencyUnitID},
		{name: "missing quote unit", fromUnitID: 1, want: walletstore.ErrMissingCurrencyUnitID},
		{name: "invalid quote unit", fromUnitID: 1, toUnitID: -1, want: walletstore.ErrInvalidCurrencyUnitID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.Convert(t.Context(), "tenant", 100, "USD", tt.fromUnitID, "EUR", tt.toUnitID)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Convert() error = %v, want %v", err, tt.want)
			}
		})
	}
}
