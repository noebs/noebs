package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type WalletActivities struct {
	Store *walletstore.Store
}

type EnsureSystemWalletParams struct {
	TenantID       string
	Currency       string
	CurrencyUnitID int64
	WalletCode     string
	KYCTier        string
}

func NewWalletActivities(store *walletstore.Store) *WalletActivities {
	return &WalletActivities{Store: store}
}

func (a *WalletActivities) EnsureSystemWallet(ctx context.Context, params EnsureSystemWalletParams) (*walletstore.Wallet, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	if err := walletstore.ValidateCurrencyUnitID(params.CurrencyUnitID); err != nil {
		return nil, err
	}
	return a.Store.EnsureWallet(ctx, walletstore.EnsureWalletParams{
		TenantID:       params.TenantID,
		OwnerType:      walletstore.OwnerTypeSystem,
		OwnerID:        params.WalletCode,
		Currency:       params.Currency,
		CurrencyUnitID: params.CurrencyUnitID,
		KYCTier:        params.KYCTier,
	})
}
