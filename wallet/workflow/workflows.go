package workflow

import (
	"errors"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"go.temporal.io/sdk/workflow"
)

var ErrNotImplemented = errors.New("workflow not implemented")

type DepositParams struct {
	TenantID      string
	ProviderCode  string
	TransactionID string
}

type WithdrawalParams struct {
	TenantID     string
	ProviderCode string
	Request      walletpsp.PayoutRequest
}

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
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	verifyParams := walletactivity.VerifyDepositParams{
		TenantID:      params.TenantID,
		ProviderCode:  params.ProviderCode,
		TransactionID: params.TransactionID,
	}
	var result walletpsp.DepositVerification
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityVerifyDeposit, verifyParams).Get(ctx, &result); err != nil {
		return err
	}
	return nil
}

func Withdrawal(ctx workflow.Context, params WithdrawalParams) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	payoutParams := walletactivity.SendPayoutParams{
		TenantID:     params.TenantID,
		ProviderCode: params.ProviderCode,
		Request:      params.Request,
	}
	var result walletpsp.PayoutResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivitySendPayout, payoutParams).Get(ctx, &result); err != nil {
		return err
	}
	return nil
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
	var validation struct{}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDoubleEntry, activityParams).Get(ctx, &validation); err != nil {
		return err
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
