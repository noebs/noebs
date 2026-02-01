package activity

import (
	"context"
	"errors"

	walletstore "github.com/adonese/noebs/wallet/store"
)

var ErrMissingStore = errors.New("missing wallet store")

type LedgerActivities struct {
	Store *walletstore.Store
}

func NewLedgerActivities(store *walletstore.Store) *LedgerActivities {
	return &LedgerActivities{Store: store}
}

func (a *LedgerActivities) ExecuteDoubleEntry(ctx context.Context, params walletstore.DoubleEntryParams) (*walletstore.DoubleEntryResult, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.PostDoubleEntry(ctx, params)
}

func (a *LedgerActivities) CreateHold(ctx context.Context, params walletstore.HoldParams) (*walletstore.BalanceHold, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.CreateHold(ctx, params)
}

func (a *LedgerActivities) ReleaseHold(ctx context.Context, tenantID string, holdID int64) error {
	if a == nil || a.Store == nil {
		return ErrMissingStore
	}
	return a.Store.ReleaseHold(ctx, tenantID, holdID)
}
