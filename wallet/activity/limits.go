package activity

import (
	"context"
	"errors"

	walletstore "github.com/adonese/noebs/wallet/store"
	"go.temporal.io/sdk/temporal"
)

const TransactionLimitExceededErrorType = "transaction_limit_exceeded"

type LimitActivities struct {
	Store *walletstore.Store
}

func NewLimitActivities(store *walletstore.Store) *LimitActivities {
	return &LimitActivities{Store: store}
}

func (a *LimitActivities) ReserveLimitUsage(ctx context.Context, params walletstore.LimitUsageParams) (*walletstore.LimitUsageReservation, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	reservation, err := a.Store.ReserveLimitUsage(ctx, params)
	return reservation, classifyTransactionLimitError(err)
}

func (a *LimitActivities) ReleaseLimitUsage(ctx context.Context, params walletstore.LimitUsageParams) error {
	if a == nil || a.Store == nil {
		return ErrMissingStore
	}
	return a.Store.ReleaseLimitUsage(ctx, params)
}

func (a *LimitActivities) ConsumeLimitUsage(ctx context.Context, params walletstore.ConsumeLimitUsageParams) error {
	if a == nil || a.Store == nil {
		return ErrMissingStore
	}
	return a.Store.ConsumeLimitUsage(ctx, params)
}

func classifyTransactionLimitError(err error) error {
	if errors.Is(err, walletstore.ErrTransactionLimitExceeded) {
		return temporal.NewNonRetryableApplicationError(err.Error(), TransactionLimitExceededErrorType, err)
	}
	return err
}
