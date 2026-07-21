package activity

import (
	"context"

	walletfees "github.com/adonese/noebs/wallet/fees"
	walletstore "github.com/adonese/noebs/wallet/store"
)

type FeeActivities struct {
	Store *walletstore.Store
}

func NewFeeActivities(store *walletstore.Store) *FeeActivities {
	return &FeeActivities{Store: store}
}

func (a *FeeActivities) CalculateFee(ctx context.Context, tenantID, txType, currency string, currencyUnitID, amount int64) (*walletfees.FeeResult, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	engine := walletfees.FeeEngine{Store: a.Store}
	return engine.Calculate(ctx, tenantID, txType, currency, currencyUnitID, amount)
}
