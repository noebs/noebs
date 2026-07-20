package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type P2PActivities struct {
	Store *walletstore.Store
}

func NewP2PActivities(store *walletstore.Store) *P2PActivities {
	return &P2PActivities{Store: store}
}

func (a *P2PActivities) GetP2PCommand(ctx context.Context, tenantID, idempotencyKey string) (*walletstore.P2PCommand, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.GetP2PCommand(ctx, tenantID, idempotencyKey)
}
