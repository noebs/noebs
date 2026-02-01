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
	TenantID        string
	ProviderCode    string
	ClientReference string
	WalletID        string
	OwnerType       string
	OwnerID         string
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

	pspTxn, err := loadPSPTransaction(ctx, params.TenantID, params.ClientReference)
	if err != nil {
		return err
	}

	providerCode := params.ProviderCode
	if providerCode == "" {
		providerCode = pspTxn.PSPProvider
	}
	transactionID := ""
	if pspTxn.PSPTransactionID.Valid {
		transactionID = pspTxn.PSPTransactionID.String
	}

	validationReq := walletvalidation.DepositValidationRequest{
		TenantID:        params.TenantID,
		TransactionType: "deposit",
		ProviderCode:    providerCode,
		TransactionID:   transactionID,
		WalletID:        walletID,
		Currency:        pspTxn.Currency,
		Amount:          pspTxn.Amount,
		OwnerType:       params.OwnerType,
		OwnerID:         params.OwnerID,
	}
	var validation walletvalidation.DepositValidationResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDeposit, validationReq).Get(ctx, &validation); err != nil {
		return err
	}

	verifyParams := walletactivity.VerifyDepositParams{
		TenantID:      params.TenantID,
		ProviderCode:  providerCode,
		TransactionID: transactionID,
	}
	var result walletpsp.DepositVerification
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityVerifyDeposit, verifyParams).Get(ctx, &result); err != nil {
		return err
	}

	resolveReq := walletvalidation.PSPAmountResolutionRequest{
		TenantID:           params.TenantID,
		RequestedAmount:    pspTxn.Amount,
		RequestedCurrency:  pspTxn.Currency,
		SettlementAmount:   result.Amount,
		SettlementCurrency: result.Currency,
		WalletCurrency:     pspTxn.Currency,
	}
	var resolved walletvalidation.PSPAmountResolutionResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityResolvePSPDepositAmounts, resolveReq).Get(ctx, &resolved); err != nil {
		return err
	}

	amounts := []walletstore.PSPTransactionAmountInput{
		{
			AmountKind: walletstore.PSPAmountRequested,
			Amount:     pspTxn.Amount,
			Currency:   pspTxn.Currency,
		},
		{
			AmountKind: walletstore.PSPAmountSettlement,
			Amount:     result.Amount,
			Currency:   result.Currency,
		},
		{
			AmountKind: walletstore.PSPAmountWalletCredit,
			Amount:     resolved.WalletCreditAmount,
			Currency:   resolved.WalletCurrency,
			FxRate:     resolved.AppliedFXRate,
			FxSource:   resolved.AppliedFXSource,
		},
	}
	if resolved.AppliedFXRate.Valid {
		amounts[2].FxBaseCurrency = resolveReq.SettlementCurrency
		amounts[2].FxQuoteCurrency = resolved.WalletCurrency
	}
	if pspTxn.FeeAmount.Valid && pspTxn.FeeAmount.Int64 > 0 {
		amounts = append(amounts, walletstore.PSPTransactionAmountInput{
			AmountKind: walletstore.PSPAmountFee,
			Amount:     pspTxn.FeeAmount.Int64,
			Currency:   pspTxn.Currency,
		})
	}
	if pspTxn.NetAmount.Valid && pspTxn.NetAmount.Int64 > 0 {
		amounts = append(amounts, walletstore.PSPTransactionAmountInput{
			AmountKind: walletstore.PSPAmountNet,
			Amount:     pspTxn.NetAmount.Int64,
			Currency:   pspTxn.Currency,
		})
	}
	if resolved.VarianceKind != "" && resolved.VarianceAmount != 0 {
		amounts = append(amounts, walletstore.PSPTransactionAmountInput{
			AmountKind: resolved.VarianceKind,
			Amount:     absInt64(resolved.VarianceAmount),
			Currency:   resolved.WalletCurrency,
		})
	}
	if err := recordPSPAmounts(ctx, params.TenantID, pspTxn.ID, amounts); err != nil {
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

	pspTxn, err := loadPSPTransaction(ctx, params.TenantID, params.Request.ClientReference)
	if err != nil {
		return err
	}

	providerCode := params.ProviderCode
	if providerCode == "" {
		providerCode = pspTxn.PSPProvider
	}

	validationReq := walletvalidation.WithdrawalValidationRequest{
		TenantID:        params.TenantID,
		TransactionType: "withdrawal",
		ProviderCode:    providerCode,
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
		ProviderCode: providerCode,
		Request:      params.Request,
	}
	var result walletpsp.PayoutResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivitySendPayout, payoutParams).Get(ctx, &result); err != nil {
		return err
	}

	amounts := []walletstore.PSPTransactionAmountInput{
		{
			AmountKind: walletstore.PSPAmountRequested,
			Amount:     params.Request.Amount,
			Currency:   params.Request.Currency,
		},
		{
			AmountKind: walletstore.PSPAmountWalletDebit,
			Amount:     validation.TotalDebit,
			Currency:   validation.Currency,
		},
	}
	if pspTxn.FeeAmount.Valid && pspTxn.FeeAmount.Int64 > 0 {
		amounts = append(amounts, walletstore.PSPTransactionAmountInput{
			AmountKind: walletstore.PSPAmountFee,
			Amount:     pspTxn.FeeAmount.Int64,
			Currency:   pspTxn.Currency,
		})
	}
	if pspTxn.NetAmount.Valid && pspTxn.NetAmount.Int64 > 0 {
		amounts = append(amounts, walletstore.PSPTransactionAmountInput{
			AmountKind: walletstore.PSPAmountNet,
			Amount:     pspTxn.NetAmount.Int64,
			Currency:   pspTxn.Currency,
		})
	}
	if err := recordPSPAmounts(ctx, params.TenantID, pspTxn.ID, amounts); err != nil {
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

func loadPSPTransaction(ctx workflow.Context, tenantID, clientReference string) (*walletstore.PSPTransaction, error) {
	var txn walletstore.PSPTransaction
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityGetPSPTransactionByReference, tenantID, clientReference).Get(ctx, &txn); err != nil {
		return nil, err
	}
	return &txn, nil
}

func recordPSPAmounts(ctx workflow.Context, tenantID string, pspTransactionID int64, amounts []walletstore.PSPTransactionAmountInput) error {
	params := walletactivity.AddPSPTransactionAmountsParams{
		TenantID:         tenantID,
		PSPTransactionID: pspTransactionID,
		Amounts:          amounts,
	}
	var stored []walletstore.PSPTransactionAmount
	return workflow.ExecuteActivity(ctx, walletactivity.ActivityAddPSPTransactionAmounts, params).Get(ctx, &stored)
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
