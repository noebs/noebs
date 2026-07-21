package workflow

import (
	"errors"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestValidateRequiredCurrencyUnitIDsUsesTypedErrors(t *testing.T) {
	tests := []struct {
		name string
		ids  []int64
		want error
	}{
		{name: "all valid", ids: []int64{1, 2, 3}},
		{name: "zero is missing", ids: []int64{1, 0, 3}, want: walletstore.ErrMissingCurrencyUnitID},
		{name: "negative is invalid", ids: []int64{1, -2, 3}, want: walletstore.ErrInvalidCurrencyUnitID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequiredCurrencyUnitIDs(tt.ids...)
			if !errors.Is(err, tt.want) {
				t.Fatalf("validateRequiredCurrencyUnitIDs() error = %v, want %v", err, tt.want)
			}
		})
	}
}
