package activity

import (
	"context"

	walletsecurity "github.com/adonese/noebs/wallet/security"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

type SecurityActivities struct {
	Store *walletstore.Store
}

func NewSecurityActivities(store *walletstore.Store) *SecurityActivities {
	return &SecurityActivities{Store: store}
}

func (a *SecurityActivities) VerifyWalletPIN(ctx context.Context, tenantID string, walletID uuid.UUID, pin string) (bool, error) {
	if a == nil || a.Store == nil {
		return false, ErrMissingStore
	}
	walletRow, err := a.Store.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		return false, err
	}
	hash := ""
	if walletRow.WalletPinHash.Valid {
		hash = walletRow.WalletPinHash.String
	}
	return walletsecurity.VerifyPIN(hash, pin), nil
}
