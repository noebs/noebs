package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type WalletActivities struct {
	Store *walletstore.Store
}

type EnsureSystemWalletParams struct {
	TenantID   string
	Currency   string
	WalletCode string
}

func NewWalletActivities(store *walletstore.Store) *WalletActivities {
	return &WalletActivities{Store: store}
}

func (a *WalletActivities) EnsureSystemWallet(ctx context.Context, params EnsureSystemWalletParams) (*walletstore.Wallet, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.EnsureWallet(ctx, walletstore.EnsureWalletParams{
		TenantID:  params.TenantID,
		OwnerType: walletstore.OwnerTypeSystem,
		OwnerID:   params.WalletCode,
		Currency:  params.Currency,
	})
}
