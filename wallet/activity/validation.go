package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
)

type ValidationActivities struct {
	Service *walletvalidation.Service
}

func NewValidationActivities(store *walletstore.Store) *ValidationActivities {
	return &ValidationActivities{Service: &walletvalidation.Service{Store: store}}
}

func (a *ValidationActivities) ValidateP2PTransfer(ctx context.Context, req walletvalidation.P2PValidationRequest) (*walletvalidation.P2PValidationResult, error) {
	if a == nil || a.Service == nil {
		return nil, ErrMissingStore
	}
	return a.Service.ValidateP2P(ctx, req)
}

func (a *ValidationActivities) ValidateDeposit(ctx context.Context, req walletvalidation.DepositValidationRequest) (*walletvalidation.DepositValidationResult, error) {
	if a == nil || a.Service == nil {
		return nil, ErrMissingStore
	}
	return a.Service.ValidateDeposit(ctx, req)
}

func (a *ValidationActivities) ValidateWithdrawal(ctx context.Context, req walletvalidation.WithdrawalValidationRequest) (*walletvalidation.WithdrawalValidationResult, error) {
	if a == nil || a.Service == nil {
		return nil, ErrMissingStore
	}
	return a.Service.ValidateWithdrawal(ctx, req)
}
