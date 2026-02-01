package workflow

import (
	"errors"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	"github.com/google/uuid"
	"go.temporal.io/sdk/workflow"
)

var ErrNotImplemented = errors.New("workflow not implemented")

type DepositParams struct {
	TenantID      string
	ProviderCode  string
	TransactionID string
	WalletID      string
	Currency      string
	Amount        int64
	OwnerType     string
	OwnerID       string
}

type WithdrawalParams struct {
	TenantID     string
	ProviderCode string
	WalletID     string
	OwnerType    string
	OwnerID      string
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
	FromOwnerType  string
	FromOwnerID    string
	ToOwnerType    string
	ToOwnerID      string
}

type ManualTransferParams struct{}

type ReconciliationParams struct{}

type PSPStatusPollerParams struct{}

func Deposit(ctx workflow.Context, params DepositParams) error {
	walletID, err := uuid.Parse(params.WalletID)
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	validationReq := walletvalidation.DepositValidationRequest{
		TenantID:        params.TenantID,
		TransactionType: "deposit",
		ProviderCode:    params.ProviderCode,
		TransactionID:   params.TransactionID,
		WalletID:        walletID,
		Currency:        params.Currency,
		Amount:          params.Amount,
		OwnerType:       params.OwnerType,
		OwnerID:         params.OwnerID,
	}
	var validation walletvalidation.DepositValidationResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDeposit, validationReq).Get(ctx, &validation); err != nil {
		return err
	}

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
	walletID, err := uuid.Parse(params.WalletID)
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	validationReq := walletvalidation.WithdrawalValidationRequest{
		TenantID:        params.TenantID,
		TransactionType: "withdrawal",
		ProviderCode:    params.ProviderCode,
		WalletID:        walletID,
		Currency:        params.Request.Currency,
		Amount:          params.Request.Amount,
		OwnerType:       params.OwnerType,
		OwnerID:         params.OwnerID,
	}
	var validation walletvalidation.WithdrawalValidationResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateWithdrawal, validationReq).Get(ctx, &validation); err != nil {
		return err
	}

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
	validationReq := walletvalidation.P2PValidationRequest{
		TenantID:        params.TenantID,
		TransactionType: "p2p",
		FromWalletID:    fromID,
		ToWalletID:      toID,
		Currency:        params.Currency,
		Amount:          params.Amount,
		FromOwnerType:   params.FromOwnerType,
		FromOwnerID:     params.FromOwnerID,
		ToOwnerType:     params.ToOwnerType,
		ToOwnerID:       params.ToOwnerID,
	}
	var validation walletvalidation.P2PValidationResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateP2PTransfer, validationReq).Get(ctx, &validation); err != nil {
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
