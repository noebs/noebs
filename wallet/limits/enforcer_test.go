package limits

import (
	"context"
	"errors"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

func TestCheckRejectsInvalidInputsBeforeStoreLookup(t *testing.T) {
	enforcer := &Enforcer{Store: &walletstore.Store{}}
	walletID := uuid.New()
	tests := []struct {
		name     string
		tenantID string
		walletID uuid.UUID
		txType   string
		currency string
		amount   int64
		wantErr  error
	}{
		{
			name:     "missing tenant",
			tenantID: "",
			walletID: walletID,
			txType:   "p2p",
			currency: "USD",
			amount:   100,
			wantErr:  walletstore.ErrMissingTenantID,
		},
		{
			name:     "reserved tenant",
			tenantID: "default",
			walletID: walletID,
			txType:   "p2p",
			currency: "USD",
			amount:   100,
			wantErr:  walletstore.ErrInvalidTenantID,
		},
		{
			name:     "missing wallet",
			tenantID: "tenant",
			walletID: uuid.Nil,
			txType:   "p2p",
			currency: "USD",
			amount:   100,
			wantErr:  walletstore.ErrMissingWalletID,
		},
		{
			name:     "missing transaction type",
			tenantID: "tenant",
			walletID: walletID,
			txType:   "",
			currency: "USD",
			amount:   100,
			wantErr:  walletstore.ErrMissingTransactionType,
		},
		{
			name:     "missing currency",
			tenantID: "tenant",
			walletID: walletID,
			txType:   "p2p",
			currency: "",
			amount:   100,
			wantErr:  walletstore.ErrMissingCurrency,
		},
		{
			name:     "zero amount",
			tenantID: "tenant",
			walletID: walletID,
			txType:   "p2p",
			currency: "USD",
			amount:   0,
			wantErr:  walletstore.ErrInvalidAmount,
		},
		{
			name:     "negative amount",
			tenantID: "tenant",
			walletID: walletID,
			txType:   "p2p",
			currency: "USD",
			amount:   -1,
			wantErr:  walletstore.ErrInvalidAmount,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := enforcer.Check(context.Background(), tt.tenantID, tt.walletID, tt.txType, tt.currency, tt.amount)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Check() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
