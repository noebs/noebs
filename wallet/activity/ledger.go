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

func (a *LedgerActivities) ValidateDoubleEntry(ctx context.Context, params walletstore.DoubleEntryParams) (struct{}, error) {
	_ = ctx
	if a == nil {
		return struct{}{}, ErrMissingStore
	}
	return struct{}{}, walletstore.ValidateDoubleEntryParams(params)
}

func (a *LedgerActivities) CreateHold(ctx context.Context, params walletstore.HoldParams) (*walletstore.BalanceHold, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.CreateHold(ctx, params)
}

func (a *LedgerActivities) ValidateHold(ctx context.Context, params walletstore.HoldParams) (struct{}, error) {
	_ = ctx
	if a == nil {
		return struct{}{}, ErrMissingStore
	}
	return struct{}{}, walletstore.ValidateHoldParams(params)
}

func (a *LedgerActivities) ReleaseHold(ctx context.Context, tenantID string, holdID int64) error {
	if a == nil || a.Store == nil {
		return ErrMissingStore
	}
	return a.Store.ReleaseHold(ctx, tenantID, holdID)
}

func (a *LedgerActivities) ValidateReleaseHold(ctx context.Context, tenantID string, holdID int64) (struct{}, error) {
	_ = ctx
	if a == nil {
		return struct{}{}, ErrMissingStore
	}
	return struct{}{}, walletstore.ValidateReleaseHold(tenantID, holdID)
}
