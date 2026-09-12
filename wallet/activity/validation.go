package activity

import (
	"context"
	"errors"
	"go.temporal.io/sdk/temporal"

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
	result, err := a.Service.ValidateP2P(ctx, req)
	if req.ExpectedFeeAmount != nil && terminalP2PValidationError(err) {
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), "p2p_validation_failed", err)
	}
	return result, err
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

func (a *ValidationActivities) ResolvePSPDepositAmounts(ctx context.Context, req walletvalidation.PSPAmountResolutionRequest) (*walletvalidation.PSPAmountResolutionResult, error) {
	if a == nil || a.Service == nil {
		return nil, ErrMissingStore
	}
	return a.Service.ResolvePSPDepositAmounts(ctx, req)
}

func terminalP2PValidationError(err error) bool {
	return errors.Is(err, walletstore.ErrP2PFeeChanged) || errors.Is(err, walletstore.ErrCurrencyMismatch) || errors.Is(err, walletstore.ErrInsufficientFunds) || errors.Is(err, walletstore.ErrFeeConfigNotFound) || errors.Is(err, walletstore.ErrTransactionLimitNotFound) || errors.Is(err, walletstore.ErrWalletNotFound) || errors.Is(err, walletstore.ErrAmountOverflow) || errors.Is(err, walletstore.ErrP2PCommandFailed) || errors.Is(err, walletstore.ErrInvalidP2PCommand) || errors.Is(err, walletvalidation.ErrWalletInactive) || errors.Is(err, walletvalidation.ErrWalletOwnerMismatch)
}
