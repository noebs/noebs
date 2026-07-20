package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type DepositIntentActivities struct {
	Store *walletstore.Store
}

func NewDepositIntentActivities(store *walletstore.Store) *DepositIntentActivities {
	return &DepositIntentActivities{Store: store}
}

func (a *DepositIntentActivities) GetDepositIntentByReference(ctx context.Context, tenantID, reference string) (*walletstore.DepositIntent, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.GetDepositIntentByReference(ctx, tenantID, reference)
}
