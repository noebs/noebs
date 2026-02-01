package validation

import (
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

func TestValidateP2PRequest(t *testing.T) {
	base := P2PValidationRequest{
		TenantID:        "tenant",
		TransactionType: "p2p",
		FromWalletID:    uuid.New(),
		ToWalletID:      uuid.New(),
		Currency:        "USD",
		Amount:          100,
	}

	cases := []struct {
		name    string
		mutate  func(req *P2PValidationRequest)
		wantErr error
	}{
		{"missing-tenant", func(req *P2PValidationRequest) { req.TenantID = "" }, walletstore.ErrMissingTenantID},
		{"missing-tx-type", func(req *P2PValidationRequest) { req.TransactionType = "" }, walletstore.ErrMissingTransactionType},
		{"missing-currency", func(req *P2PValidationRequest) { req.Currency = "" }, walletstore.ErrMissingCurrency},
		{"missing-wallet", func(req *P2PValidationRequest) { req.FromWalletID = uuid.Nil }, walletstore.ErrMissingWalletID},
		{"same-wallet", func(req *P2PValidationRequest) { req.ToWalletID = req.FromWalletID }, walletstore.ErrInvalidWalletPair},
		{"invalid-amount", func(req *P2PValidationRequest) { req.Amount = 0 }, walletstore.ErrInvalidAmount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.mutate(&req)
			err := ValidateP2PRequest(req)
			if err != tc.wantErr {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}
