package workflow

import (
	"errors"

	"go.temporal.io/sdk/workflow"
)

var ErrNotImplemented = errors.New("workflow not implemented")

type DepositParams struct{}

type WithdrawalParams struct{}

type P2PParams struct{}

type ManualTransferParams struct{}

type ReconciliationParams struct{}

type PSPStatusPollerParams struct{}

func Deposit(ctx workflow.Context, params DepositParams) error {
	_ = ctx
	_ = params
	return ErrNotImplemented
}

func Withdrawal(ctx workflow.Context, params WithdrawalParams) error {
	_ = ctx
	_ = params
	return ErrNotImplemented
}

func P2P(ctx workflow.Context, params P2PParams) error {
	_ = ctx
	_ = params
	return ErrNotImplemented
}

func ManualTransfer(ctx workflow.Context, params ManualTransferParams) error {
	_ = ctx
	_ = params
	return ErrNotImplemented
}

func Reconciliation(ctx workflow.Context, params ReconciliationParams) error {
	_ = ctx
	_ = params
	return ErrNotImplemented
}

func PSPStatusPoller(ctx workflow.Context, params PSPStatusPollerParams) error {
	_ = ctx
	_ = params
	return ErrNotImplemented
}
