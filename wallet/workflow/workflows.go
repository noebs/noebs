package workflow

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	"github.com/google/uuid"
	"go.temporal.io/sdk/workflow"
)

var (
	ErrNotImplemented         = errors.New("workflow not implemented")
	ErrManualTransferTimedOut = errors.New("manual transfer approval timed out")
)

const (
	ManualTransferDecisionSignal  = "manual_transfer_decision"
	ManualTransferStatusPending   = "pending"
	ManualTransferStatusApproved  = "approved"
	ManualTransferStatusRejected  = "rejected"
	ManualTransferStatusCompleted = "completed"
)

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

type ManualTransferParams struct {
	TenantID               string
	IdempotencyKey         string
	TransferType           string
	WalletID               string
	Amount                 int64
	Currency               string
	Reason                 string
	RequestedBy            int64
	PSPProvider            string
	PSPReference           string
	ApprovalTimeoutSeconds int
}

type ManualTransferDecision struct {
	Approved       bool
	ApproverID     int64
	Reason         string
	ProofOfPayment string
}

type ReconciliationParams struct {
	TenantID  string
	Status    string
	StartTime time.Time
	EndTime   time.Time
	Limit     int
}

type PSPStatusPollerParams struct {
	TenantID            string
	Limit               int
	PollIntervalSeconds int
}

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
	if params.TenantID == "" {
		return walletstore.ErrMissingTenantID
	}
	if params.IdempotencyKey == "" {
		return walletstore.ErrMissingIdempotencyKey
	}
	if params.TransferType == "" {
		return walletstore.ErrMissingTransferType
	}
	if params.WalletID == "" {
		return walletstore.ErrMissingWalletID
	}
	if params.Amount <= 0 {
		return walletstore.ErrInvalidAmount
	}
	if params.Currency == "" {
		return walletstore.ErrMissingCurrency
	}
	if params.Reason == "" {
		return walletstore.ErrMissingReason
	}
	if params.RequestedBy <= 0 {
		return walletstore.ErrMissingApproverID
	}

	walletID, err := uuid.Parse(params.WalletID)
	if err != nil {
		return walletstore.ErrMissingWalletID
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	info := workflow.GetInfo(ctx)
	workflowID := ""
	if info != nil {
		workflowID = info.WorkflowExecution.ID
	}
	if workflowID == "" {
		workflowID = params.IdempotencyKey
	}

	transfer := walletstore.ManualTransfer{
		TenantID:       params.TenantID,
		WorkflowID:     workflowID,
		IdempotencyKey: params.IdempotencyKey,
		TransferType:   params.TransferType,
		WalletID:       sql.NullString{String: params.WalletID, Valid: true},
		Amount:         params.Amount,
		Currency:       params.Currency,
		Reason:         params.Reason,
		Status:         ManualTransferStatusPending,
		RequestedBy:    sql.NullInt64{Int64: params.RequestedBy, Valid: true},
		PSPProvider:    sql.NullString{String: params.PSPProvider, Valid: params.PSPProvider != ""},
		PSPReference:   sql.NullString{String: params.PSPReference, Valid: params.PSPReference != ""},
	}
	var stored walletstore.ManualTransfer
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityCreateManualTransfer, transfer).Get(ctx, &stored); err != nil {
		return err
	}

	var holdID int64
	if isManualTransferDebit(params.TransferType) {
		holdParams := walletstore.HoldParams{
			TenantID:       params.TenantID,
			WalletID:       walletID,
			Amount:         params.Amount,
			Reason:         "manual_transfer",
			ReferenceType:  params.TransferType,
			ReferenceID:    workflowID,
			IdempotencyKey: params.IdempotencyKey + ":hold",
			ExpiresAt:      workflow.Now(ctx).Add(24 * time.Hour),
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateHold, holdParams).Get(ctx, nil); err != nil {
			return err
		}
		var hold walletstore.BalanceHold
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityCreateHold, holdParams).Get(ctx, &hold); err != nil {
			return err
		}
		holdID = hold.ID
	}

	decision, err := awaitManualTransferDecision(ctx, params)
	if err != nil {
		if holdID > 0 {
			_ = workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseHold, params.TenantID, holdID).Get(ctx, nil)
		}
		update := walletstore.ManualTransferStatusUpdate{
			Status:          ManualTransferStatusRejected,
			RejectionReason: sql.NullString{String: err.Error(), Valid: true},
		}
		_ = workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, params.TenantID, workflowID, update).Get(ctx, nil)
		return err
	}

	now := workflow.Now(ctx)
	if decision.Approved {
		if decision.ProofOfPayment == "" {
			return walletstore.ErrMissingProofOfPayment
		}
		approval := walletstore.ManualTransferApproval{
			TenantID:         params.TenantID,
			ManualTransferID: stored.ID,
			ApproverID:       decision.ApproverID,
			Decision:         ManualTransferStatusApproved,
			Reason:           sql.NullString{String: decision.Reason, Valid: decision.Reason != ""},
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityAddManualTransferApproval, approval).Get(ctx, nil); err != nil {
			return err
		}
		update := walletstore.ManualTransferStatusUpdate{
			Status:         ManualTransferStatusApproved,
			ApprovedBy:     sql.NullInt64{Int64: decision.ApproverID, Valid: true},
			ApprovedAt:     sql.NullTime{Time: now, Valid: true},
			ProofOfPayment: sql.NullString{String: decision.ProofOfPayment, Valid: true},
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, params.TenantID, workflowID, update).Get(ctx, nil); err != nil {
			return err
		}
		if holdID > 0 {
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateReleaseHold, params.TenantID, holdID).Get(ctx, nil); err != nil {
				return err
			}
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseHold, params.TenantID, holdID).Get(ctx, nil); err != nil {
				return err
			}
		}
		var treasury walletstore.Wallet
		treasuryParams := walletactivity.EnsureSystemWalletParams{
			TenantID:   params.TenantID,
			Currency:   params.Currency,
			WalletCode: walletstore.SystemTreasury,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, treasuryParams).Get(ctx, &treasury); err != nil {
			return err
		}

		debitID := walletID
		creditID := treasury.ID
		if params.TransferType == "manual_credit" {
			debitID = treasury.ID
			creditID = walletID
		}
		entry := walletstore.DoubleEntryParams{
			TenantID:       params.TenantID,
			IdempotencyKey: params.IdempotencyKey + ":ledger",
			Currency:       params.Currency,
			ReferenceType:  params.TransferType,
			ReferenceID:    workflowID,
			DebitWalletID:  debitID,
			CreditWalletID: creditID,
			Amount:         params.Amount,
			Description:    params.Reason,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDoubleEntry, entry).Get(ctx, nil); err != nil {
			return err
		}
		var posted walletstore.DoubleEntryResult
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteDoubleEntry, entry).Get(ctx, &posted); err != nil {
			return err
		}
		_ = posted

		complete := walletstore.ManualTransferStatusUpdate{
			Status:      ManualTransferStatusCompleted,
			CompletedAt: sql.NullTime{Time: workflow.Now(ctx), Valid: true},
		}
		return workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, params.TenantID, workflowID, complete).Get(ctx, nil)
	}

	if decision.Reason == "" {
		return walletstore.ErrMissingReason
	}
	rejection := walletstore.ManualTransferApproval{
		TenantID:         params.TenantID,
		ManualTransferID: stored.ID,
		ApproverID:       decision.ApproverID,
		Decision:         ManualTransferStatusRejected,
		Reason:           sql.NullString{String: decision.Reason, Valid: true},
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityAddManualTransferApproval, rejection).Get(ctx, nil); err != nil {
		return err
	}
	if holdID > 0 {
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateReleaseHold, params.TenantID, holdID).Get(ctx, nil); err != nil {
			return err
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseHold, params.TenantID, holdID).Get(ctx, nil); err != nil {
			return err
		}
	}
	update := walletstore.ManualTransferStatusUpdate{
		Status:          ManualTransferStatusRejected,
		RejectionReason: sql.NullString{String: decision.Reason, Valid: true},
	}
	return workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, params.TenantID, workflowID, update).Get(ctx, nil)
}

func Reconciliation(ctx workflow.Context, params ReconciliationParams) error {
	if params.TenantID == "" {
		return walletstore.ErrMissingTenantID
	}
	if params.Status == "" {
		return walletstore.ErrMissingStatus
	}
	if params.StartTime.IsZero() {
		return walletstore.ErrMissingStartTime
	}
	if params.EndTime.IsZero() {
		return walletstore.ErrMissingEndTime
	}
	if params.StartTime.After(params.EndTime) {
		return walletstore.ErrInvalidTimeRange
	}
	if params.Limit <= 0 {
		return walletstore.ErrInvalidLimit
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	listParams := walletactivity.ListPSPTransactionsByStatusParams{
		TenantID: params.TenantID,
		Status:   params.Status,
		Start:    params.StartTime,
		End:      params.EndTime,
		Limit:    params.Limit,
	}
	var txns []walletstore.PSPTransaction
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityListPSPTransactionsByStatus, listParams).Get(ctx, &txns); err != nil {
		return err
	}

	logger := workflow.GetLogger(ctx)
	missing := make([]string, 0)
	for _, txn := range txns {
		if txn.ClientReference == "" {
			continue
		}
		var exists bool
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityLedgerTransactionExists, params.TenantID, txn.ClientReference).Get(ctx, &exists); err != nil {
			return err
		}
		if !exists {
			missing = append(missing, txn.ClientReference)
			logger.Warn("missing ledger transaction for psp transaction", "client_reference", txn.ClientReference)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("reconciliation mismatch: %d missing ledger entries", len(missing))
	}
	return nil
}

func PSPStatusPoller(ctx workflow.Context, params PSPStatusPollerParams) error {
	if params.TenantID == "" {
		return walletstore.ErrMissingTenantID
	}
	if params.Limit <= 0 {
		return walletstore.ErrInvalidLimit
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	var txns []walletstore.PSPTransaction
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityListPSPTransactionsForPolling, params.TenantID, params.Limit).Get(ctx, &txns); err != nil {
		return err
	}

	now := workflow.Now(ctx)
	nextPoll := sql.NullTime{}
	if params.PollIntervalSeconds > 0 {
		nextPoll = sql.NullTime{Time: now.Add(time.Duration(params.PollIntervalSeconds) * time.Second), Valid: true}
	}
	lockExpiry := now.Add(time.Minute)
	if params.PollIntervalSeconds > 0 {
		lockExpiry = now.Add(time.Duration(params.PollIntervalSeconds) * time.Second)
	}

	for _, txn := range txns {
		if !txn.PSPTransactionID.Valid {
			continue
		}
		lockParams := walletactivity.TryAcquirePSPTransactionLockParams{
			TenantID:        params.TenantID,
			ClientReference: txn.ClientReference,
			LockToken:       newLockToken(ctx),
			LockExpiresAt:   lockExpiry,
		}
		var acquired bool
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityTryAcquirePSPTransactionLock, lockParams).Get(ctx, &acquired); err != nil {
			return err
		}
		if !acquired {
			continue
		}
		statusParams := walletactivity.GetStatusParams{
			TenantID:      params.TenantID,
			ProviderCode:  txn.PSPProvider,
			TransactionID: txn.PSPTransactionID.String,
		}
		var status walletpsp.TxStatus
		pollErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityGetTransactionStatus, statusParams).Get(ctx, &status)
		update := walletstore.PSPStatusUpdate{
			Status:       txn.Status,
			LastPolledAt: sql.NullTime{Time: now, Valid: true},
			NextPollAt:   nextPoll,
			RetryCount:   txn.RetryCount + 1,
		}
		if pollErr != nil || status.Status == "" {
			update.LastErrorType = sql.NullString{String: "poll_error", Valid: true}
			update.LastErrorAt = sql.NullTime{Time: now, Valid: true}
		} else {
			update.Status = status.Status
		}
		updateParams := walletactivity.UpdatePSPTransactionStatusParams{
			TenantID:        params.TenantID,
			ClientReference: txn.ClientReference,
			Update:          update,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdatePSPTransactionStatus, updateParams).Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
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

func awaitManualTransferDecision(ctx workflow.Context, params ManualTransferParams) (ManualTransferDecision, error) {
	timeout := time.Duration(params.ApprovalTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 24 * time.Hour
	}
	decisionCh := workflow.GetSignalChannel(ctx, ManualTransferDecisionSignal)
	timer := workflow.NewTimer(ctx, timeout)

	var decision ManualTransferDecision
	timedOut := false
	selector := workflow.NewSelector(ctx)
	selector.AddReceive(decisionCh, func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &decision)
	})
	selector.AddFuture(timer, func(f workflow.Future) {
		timedOut = true
	})
	selector.Select(ctx)
	if timedOut {
		return ManualTransferDecision{}, ErrManualTransferTimedOut
	}
	if decision.ApproverID <= 0 {
		return ManualTransferDecision{}, walletstore.ErrMissingApproverID
	}
	return decision, nil
}

func isManualTransferDebit(transferType string) bool {
	switch transferType {
	case "manual_debit", "manual_withdrawal":
		return true
	default:
		return false
	}
}

func newLockToken(ctx workflow.Context) string {
	var token string
	workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return uuid.NewString()
	}).Get(&token)
	return token
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
