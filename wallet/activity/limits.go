package activity

import (
	"context"

	walletlimits "github.com/adonese/noebs/wallet/limits"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

type LimitActivities struct {
	Store *walletstore.Store
}

func NewLimitActivities(store *walletstore.Store) *LimitActivities {
	return &LimitActivities{Store: store}
}

func (a *LimitActivities) CheckLimits(ctx context.Context, tenantID string, walletID uuid.UUID, txType, currency string, amount int64) (*walletlimits.CheckResult, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	enforcer := walletlimits.Enforcer{Store: a.Store}
	return enforcer.Check(ctx, tenantID, walletID, txType, currency, amount)
}
