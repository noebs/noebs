package workflow

import (
	"errors"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"go.temporal.io/sdk/workflow"
)

var ErrNotImplemented = errors.New("workflow not implemented")

type DepositParams struct{}

type WithdrawalParams struct{}

type P2PParams struct {
	TenantID       string
	IdempotencyKey string
	Currency       string
	FromWalletID   string
	ToWalletID     string
	Amount         int64
	Description    string
	ReferenceID    string
}

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
	fromID, err := uuid.Parse(params.FromWalletID)
	if err != nil {
		return err
	}
	toID, err := uuid.Parse(params.ToWalletID)
	if err != nil {
		return err
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	activityParams := walletstore.DoubleEntryParams{
		TenantID:       params.TenantID,
		IdempotencyKey: params.IdempotencyKey,
		Currency:       params.Currency,
		ReferenceType:  "p2p",
		ReferenceID:    params.ReferenceID,
		DebitWalletID:  fromID,
		CreditWalletID: toID,
		Amount:         params.Amount,
		Description:    params.Description,
	}
	var result walletstore.DoubleEntryResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteDoubleEntry, activityParams).Get(ctx, &result); err != nil {
		return err
	}
	return nil
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
