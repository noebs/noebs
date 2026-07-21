package activity

import (
	"errors"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestEnsureSystemWalletValidatesExplicitCurrencyUnit(t *testing.T) {
	activities := NewWalletActivities(&walletstore.Store{})
	for _, tt := range []struct {
		name           string
		currencyUnitID int64
		want           error
	}{
		{name: "zero is missing", want: walletstore.ErrMissingCurrencyUnitID},
		{name: "negative is invalid", currencyUnitID: -1, want: walletstore.ErrInvalidCurrencyUnitID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := activities.EnsureSystemWallet(t.Context(), EnsureSystemWalletParams{
				TenantID: "tenant", Currency: "USD", CurrencyUnitID: tt.currencyUnitID,
				WalletCode: walletstore.SystemTreasury, KYCTier: walletstore.KYCTierUnverified,
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("EnsureSystemWallet() error = %v, want %v", err, tt.want)
			}
		})
	}
}
