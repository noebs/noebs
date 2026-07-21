package activity

import (
	"context"

	walletrates "github.com/adonese/noebs/wallet/rates"
	walletstore "github.com/adonese/noebs/wallet/store"
)

type RateActivities struct {
	Store *walletstore.Store
}

func NewRateActivities(store *walletstore.Store) *RateActivities {
	return &RateActivities{Store: store}
}

func (a *RateActivities) ConvertCurrency(ctx context.Context, tenantID string, amount int64, fromCurrency string, fromCurrencyUnitID int64, toCurrency string, toCurrencyUnitID int64) (int64, error) {
	if a == nil || a.Store == nil {
		return 0, ErrMissingStore
	}
	svc := walletrates.Service{Store: a.Store}
	return svc.Convert(ctx, tenantID, amount, fromCurrency, fromCurrencyUnitID, toCurrency, toCurrencyUnitID)
}
