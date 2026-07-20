package workflow

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	ErrManualTransferTimedOut     = errors.New("manual transfer approval timed out")
	ErrWithdrawalApprovalTimedOut = errors.New("withdrawal approval timed out")
)

const (
	ManualTransferDecisionSignal  = "manual_transfer_decision"
	ManualTransferStatusPending   = "pending"
	ManualTransferStatusApproved  = "approved"
	ManualTransferStatusRejected  = "rejected"
	ManualTransferStatusCompleted = "completed"
	WithdrawalApprovalSignal      = "withdrawal_approval"
	PSPStatusUpdateSignal         = "psp_status_update"
)

type DepositParams struct {
	TenantID        string
	IntentReference string
}

type WithdrawalParams struct {
	TenantID        string
	ClientReference string
}

type withdrawalExecutionParams struct {
	TenantID               string
	ProviderCode           string
	WalletID               uuid.UUID
	OwnerType              string
	OwnerID                string
	DestinationID          int64
	AllowReturnToSource    bool
	ApprovalRequired       bool
	ApprovalTimeoutSeconds int
	HoldExpirySeconds      int
	Region                 string
	Request                walletpsp.PayoutRequest
}

type P2PParams struct {
	TenantID       string
	IdempotencyKey string
}

type ManualTransferParams struct {
	TenantID       string
	IdempotencyKey string
}

type ManualTransferDecision struct {
	Approved            bool
	DecidedByOperatorID int64
	Reason              string
	ProofOfPayment      string
}

type WithdrawalApprovalDecision struct {
	Approved            bool
	DecidedByOperatorID int64
	Reason              string
	ProofOfPayment      string
}

type ReconciliationParams struct {
	TenantID      string
	Status        string
	StartTime     time.Time
	EndTime       time.Time
	Limit         int
	LookbackHours int
}

type PSPStatusPollerParams struct {
	TenantID            string
	Limit               int
	PollIntervalSeconds int
}

func Deposit(ctx workflow.Context, params DepositParams) error {
	tenantID, err := walletstore.ValidateTenantID(params.TenantID)
	if err != nil {
		return err
	}
	params.TenantID = tenantID
	if missingRequiredText(params.IntentReference) {
		return walletstore.ErrMissingClientReference
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})
	info := workflow.GetInfo(ctx)
	workflowID := ""
	if info != nil {
		workflowID = info.WorkflowExecution.ID
	}

	intent, err := loadDepositIntent(ctx, params.TenantID, params.IntentReference)
	if err != nil {
		return err
	}
	if intent.IntentReference != params.IntentReference || intent.WorkflowID != workflowID {
		return walletstore.ErrInvalidDepositIntent
	}

	validationReq := walletvalidation.DepositValidationRequest{
		TenantID:        intent.TenantID,
		TransactionType: "deposit",
		ProviderCode:    intent.ProviderCode,
		WalletID:        intent.WalletID,
		Currency:        intent.Currency,
		Amount:          intent.Amount,
		OwnerType:       intent.OwnerType,
		OwnerID:         intent.OwnerID,
		Region:          intent.Region,
	}
	var validation walletvalidation.DepositValidationResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDeposit, validationReq).Get(ctx, &validation); err != nil {
		return err
	}
	pspTxn, err := loadPSPTransaction(ctx, intent.TenantID, intent.IntentReference)
	if err != nil {
		return err
	}
	if err := walletstore.ValidateDepositIntentTransaction(intent, pspTxn); err != nil {
		return err
	}
	providerMetadata, err := depositIntentMetadata(intent.Metadata)
	if err != nil {
		return err
	}
	limitUsage := walletstore.LimitUsageParams{
		TenantID:        intent.TenantID,
		CommandID:       "deposit:" + intent.IntentReference,
		WalletID:        intent.WalletID,
		TransactionType: "deposit",
		Currency:        intent.Currency,
		Amount:          intent.Amount,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityReserveLimitUsage, limitUsage).Get(ctx, nil); err != nil {
		return err
	}
	createParams := walletactivity.CreateDepositParams{
		TenantID:     intent.TenantID,
		ProviderCode: intent.ProviderCode,
		Request: walletpsp.DepositRequest{
			ClientReference: intent.IntentReference,
			IdempotencyKey:  intent.IdempotencyKey,
			Amount:          intent.Amount,
			Currency:        intent.Currency,
			Metadata:        providerMetadata,
		},
		Region: intent.Region,
	}
	var created walletpsp.DepositResult
	dispatchErr := workflow.ExecuteActivity(pspRemoteActivityContext(ctx), walletactivity.ActivityCreateDeposit, createParams).Get(ctx, &created)
	var statusResult walletpsp.TxStatus
	var workflowSignalPending bool
	if dispatchErr != nil {
		if isPSPDispatchDefinitivelyNotAccepted(dispatchErr) {
			outcome := "rejected"
			if isPSPDispatchNotAttempted(dispatchErr) {
				outcome = "not_attempted"
			}
			_, _, updateErr := updatePSPTransactionFromStatus(ctx, intent.TenantID, intent.IntentReference, walletpsp.TxStatus{
				Status: walletstore.PSPStatusFailed,
				RawResponse: map[string]any{
					"dispatch_error": dispatchErr.Error(), "dispatch_outcome": outcome,
				},
			})
			releaseErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseLimitUsage, limitUsage).Get(ctx, nil)
			return errors.Join(dispatchErr, updateErr, releaseErr)
		}
		statusResult, workflowSignalPending, err = updatePSPTransactionFromStatus(ctx, intent.TenantID, intent.IntentReference, walletpsp.TxStatus{
			Status: walletstore.PSPStatusProcessing,
			RawResponse: map[string]any{
				"dispatch_error": dispatchErr.Error(), "dispatch_outcome": "unknown",
			},
		})
		if err != nil {
			return errors.Join(dispatchErr, err)
		}
	} else {
		if err := validateCreatedDeposit(intent, created); err != nil {
			return err
		}
		statusResult = statusFromDepositResult(created)
		statusResult, workflowSignalPending, err = updatePSPTransactionFromStatus(ctx, intent.TenantID, intent.IntentReference, statusResult)
		if err != nil {
			return err
		}
	}
	if !isTerminalPSPStatus(statusResult.Status) {
		latest, err := loadPSPTransaction(ctx, intent.TenantID, intent.IntentReference)
		if err != nil {
			return err
		}
		latestStatus, err := statusFromPSPTransaction(latest)
		if err != nil {
			return err
		}
		mergePSPStatus(&statusResult, latestStatus)
		workflowSignalPending = pspWorkflowSignalPending(latest)
	}
	finalStatus, err := awaitTerminalPSPStatus(ctx, intent.TenantID, intent.IntentReference, statusResult, workflowSignalPending)
	if err != nil {
		return err
	}
	if err := validateTerminalDepositStatus(intent, finalStatus); err != nil {
		return err
	}
	if finalStatus.Status != "success" {
		return workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseLimitUsage, limitUsage).Get(ctx, nil)
	}

	amounts := []walletstore.PSPTransactionAmountInput{
		{
			AmountKind: walletstore.PSPAmountRequested,
			Amount:     intent.Amount,
			Currency:   intent.Currency,
		},
		{
			AmountKind: walletstore.PSPAmountSettlement,
			Amount:     finalStatus.Amount,
			Currency:   finalStatus.Currency,
		},
		{
			AmountKind: walletstore.PSPAmountWalletCredit,
			Amount:     intent.Amount,
			Currency:   intent.Currency,
		},
	}
	feeAmount := int64(0)
	if validation.Fee != nil {
		feeAmount = validation.Fee.TotalFee
	}
	if feeAmount > 0 {
		amounts = append(amounts, walletstore.PSPTransactionAmountInput{
			AmountKind: walletstore.PSPAmountFee,
			Amount:     feeAmount,
			Currency:   intent.Currency,
		})
		amounts = append(amounts, walletstore.PSPTransactionAmountInput{
			AmountKind: walletstore.PSPAmountNet,
			Amount:     validation.NetAmount,
			Currency:   intent.Currency,
		})
	}
	if err := recordPSPAmounts(ctx, intent.TenantID, pspTxn.ID, amounts); err != nil {
		return err
	}

	var treasury walletstore.Wallet
	treasuryParams := walletactivity.EnsureSystemWalletParams{
		TenantID:   intent.TenantID,
		Currency:   intent.Currency,
		WalletCode: walletstore.SystemTreasury,
		KYCTier:    walletstore.KYCTierUnverified,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, treasuryParams).Get(ctx, &treasury); err != nil {
		return err
	}

	transfers := []walletstore.SettlementTransfer{{
		DebitWalletID:  treasury.ID,
		CreditWalletID: intent.WalletID,
		Amount:         intent.Amount,
		Description:    "deposit",
	}}
	if feeAmount > 0 {
		var feesWallet walletstore.Wallet
		feesParams := walletactivity.EnsureSystemWalletParams{
			TenantID:   intent.TenantID,
			Currency:   intent.Currency,
			WalletCode: walletstore.SystemFees,
			KYCTier:    walletstore.KYCTierUnverified,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, feesParams).Get(ctx, &feesWallet); err != nil {
			return err
		}
		transfers = append(transfers, walletstore.SettlementTransfer{
			DebitWalletID:  intent.WalletID,
			CreditWalletID: feesWallet.ID,
			Amount:         feeAmount,
			Description:    "deposit fee",
		})
	}
	settlement := walletstore.MultiLegSettlementParams{
		TenantID:       intent.TenantID,
		IdempotencyKey: intent.IntentReference + ":deposit",
		Currency:       intent.Currency,
		ReferenceType:  "deposit",
		ReferenceID:    intent.IntentReference,
		Transfers:      transfers,
		LimitUsage:     limitUsage,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateMultiLegSettlement, settlement).Get(ctx, nil); err != nil {
		return err
	}
	var posted walletstore.MultiLegSettlementResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteSystemFundedMultiLegSettlement, settlement).Get(ctx, &posted); err != nil {
		return err
	}

	now := workflow.Now(ctx)
	externalRef := sql.NullString{String: finalStatus.ProviderTxID, Valid: true}
	source, err := depositFundingSource(pspTxn, intent.WalletID, intent.Currency, externalRef, now, finalStatus.RawResponse)
	if err != nil {
		return err
	}
	var storedSource walletstore.FundingSource
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityRecordFundingSource, source).Get(ctx, &storedSource); err != nil {
		return err
	}

	link := walletstore.LedgerFundingLink{
		TenantID:        intent.TenantID,
		LedgerEntryID:   posted.Transfers[0].CreditEntry.ID,
		FundingSourceID: storedSource.ID,
		Amount:          intent.Amount,
		Currency:        intent.Currency,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityLinkLedgerToFundingSource, link).Get(ctx, nil); err != nil {
		return err
	}

	metadata, err := auditMetadata(map[string]any{
		"client_reference":     intent.IntentReference,
		"provider_code":        intent.ProviderCode,
		"psp_transaction_id":   finalStatus.ProviderTxID,
		"requested_amount":     intent.Amount,
		"requested_currency":   intent.Currency,
		"wallet_credit_amount": intent.Amount,
		"wallet_currency":      intent.Currency,
		"fee_amount":           feeAmount,
		"funding_source_id":    storedSource.ID,
		"ledger_transaction":   posted.TransactionID,
	})
	if err != nil {
		return err
	}
	event := walletstore.AuditEvent{
		TenantID:   intent.TenantID,
		EventType:  "wallet.deposit",
		ActorType:  intent.OwnerType,
		ActorID:    intent.OwnerID,
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   sql.NullString{String: intent.WalletID.String(), Valid: true},
		Action:     "completed",
		Metadata:   metadata,
		WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		RequestID:  sql.NullString{String: intent.IntentReference, Valid: true},
	}
	if err := recordAuditEvent(ctx, event); err != nil {
		return err
	}
	return nil
}

func Withdrawal(ctx workflow.Context, params WithdrawalParams) error {
	tenantID, err := walletstore.ValidateTenantID(params.TenantID)
	if err != nil {
		return err
	}
	if missingRequiredText(params.ClientReference) {
		return walletstore.ErrMissingClientReference
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	info := workflow.GetInfo(ctx)
	workflowID := ""
	if info != nil {
		workflowID = info.WorkflowExecution.ID
	}

	pspTxn, err := loadPSPTransaction(ctx, tenantID, params.ClientReference)
	if err != nil {
		return err
	}
	request, err := walletstore.BindWithdrawalRequest(pspTxn, tenantID, params.ClientReference, workflowID)
	if err != nil {
		return err
	}
	execution := withdrawalExecutionParams{
		TenantID:               request.TenantID,
		ProviderCode:           request.ProviderCode,
		WalletID:               request.WalletID,
		OwnerType:              request.OwnerType,
		OwnerID:                request.OwnerID,
		DestinationID:          request.DestinationID,
		AllowReturnToSource:    request.AllowReturnToSource,
		ApprovalRequired:       request.ApprovalRequired,
		ApprovalTimeoutSeconds: request.ApprovalTimeoutSeconds,
		HoldExpirySeconds:      request.HoldExpirySeconds,
		Region:                 request.Region,
		Request: walletpsp.PayoutRequest{
			ClientReference: request.ClientReference,
			IdempotencyKey:  pspTxn.IdempotencyKey,
			Amount:          request.Amount,
			Currency:        request.Currency,
			Metadata:        request.Metadata,
		},
	}
	return executeWithdrawal(ctx, execution, pspTxn, workflowID)
}

func executeWithdrawal(ctx workflow.Context, params withdrawalExecutionParams, pspTxn *walletstore.PSPTransaction, workflowID string) error {
	var err error
	walletID := params.WalletID
	providerCode := params.ProviderCode

	validationReq := walletvalidation.WithdrawalValidationRequest{
		TenantID:        params.TenantID,
		TransactionType: "withdrawal",
		ProviderCode:    providerCode,
		WalletID:        walletID,
		Currency:        params.Request.Currency,
		Amount:          params.Request.Amount,
		OwnerType:       params.OwnerType,
		OwnerID:         params.OwnerID,
		Region:          params.Region,
	}
	var validation walletvalidation.WithdrawalValidationResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateWithdrawal, validationReq).Get(ctx, &validation); err != nil {
		return err
	}

	var destination walletstore.WithdrawalDestination
	var destinationDetails map[string]any
	var destinationID int64
	var fundingSourceCandidates []int64
	returnToSource := false

	if params.AllowReturnToSource {
		var sources []walletstore.FundingSource
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityGetReturnToSourceOptions, params.TenantID, walletID).Get(ctx, &sources); err != nil {
			return err
		}
		for _, source := range sources {
			if validateWithdrawalFundingSource(source, walletID, validation.WalletCurrency, validation.WalletDebitAmount, providerCode) == nil {
				fundingSourceCandidates = append(fundingSourceCandidates, source.ID)
			}
		}
		returnToSource = len(fundingSourceCandidates) > 0
	}

	if !returnToSource {
		if params.DestinationID <= 0 {
			if params.AllowReturnToSource {
				return walletstore.ErrFundingSourceNotFound
			}
			return walletstore.ErrMissingDestinationID
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityResolveWithdrawalDestination, params.TenantID, params.DestinationID).Get(ctx, &destination); err != nil {
			return err
		}
		if !destination.IsActive {
			return walletstore.ErrDestinationNotFound
		}
		if destination.WalletID != walletID {
			return walletstore.ErrDestinationNotFound
		}
		if destination.Currency != validation.WalletCurrency {
			return walletstore.ErrCurrencyMismatch
		}
		if destination.LinkedFundingSourceID <= 0 {
			return walletstore.ErrMissingFundingSourceID
		}
		var linkedSource walletstore.FundingSource
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityResolveFundingSource, params.TenantID, destination.LinkedFundingSourceID).Get(ctx, &linkedSource); err != nil {
			return err
		}
		if err := validateWithdrawalFundingSource(linkedSource, walletID, validation.WalletCurrency, validation.WalletDebitAmount, providerCode); err != nil {
			return err
		}
		fundingSourceCandidates = []int64{linkedSource.ID}
		if err := walletstore.ValidateWithdrawalDestinationReadyForWithdrawal(&destination); err != nil {
			return err
		}
		if len(destination.DestinationDetails) == 0 {
			return walletstore.ErrMissingDestinationDetails
		}
		if err := json.Unmarshal(destination.DestinationDetails, &destinationDetails); err != nil {
			return err
		}
		destinationID = destination.ID
		if !destination.PSPProvider.Valid || providerCode != destination.PSPProvider.String {
			return walletstore.ErrMissingProviderCode
		}
	}
	limitUsage := walletstore.LimitUsageParams{
		TenantID:        params.TenantID,
		CommandID:       "withdrawal:" + params.Request.ClientReference,
		WalletID:        walletID,
		TransactionType: "withdrawal",
		Currency:        validation.WalletCurrency,
		Amount:          validation.WalletDebitAmount,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityReserveLimitUsage, limitUsage).Get(ctx, nil); err != nil {
		return err
	}
	releaseLimit := func(cause error) error {
		releaseErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseLimitUsage, limitUsage).Get(ctx, nil)
		return errors.Join(cause, releaseErr)
	}

	holdParams := walletstore.HoldParams{
		TenantID:       params.TenantID,
		WalletID:       walletID,
		Amount:         validation.TotalDebit,
		Reason:         "withdrawal",
		ReferenceType:  "withdrawal",
		ReferenceID:    params.Request.ClientReference,
		IdempotencyKey: params.Request.ClientReference + ":hold",
		ExpiresAt:      workflow.Now(ctx).Add(time.Duration(params.HoldExpirySeconds) * time.Second),
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateHold, holdParams).Get(ctx, nil); err != nil {
		return releaseLimit(err)
	}
	var hold walletstore.BalanceHold
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityCreateHold, holdParams).Get(ctx, &hold); err != nil {
		return releaseLimit(err)
	}
	holdID := hold.ID
	var sourceReservation walletstore.FundingSourceWithdrawalReservationResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityReserveFundingSourceWithdrawal, walletstore.ReserveFundingSourceWithdrawalParams{
		TenantID: params.TenantID, WorkflowID: workflowID, CandidateSourceIDs: fundingSourceCandidates,
		WalletID: walletID, Amount: validation.WalletDebitAmount, Currency: validation.WalletCurrency, ProviderCode: providerCode,
	}).Get(ctx, &sourceReservation); err != nil {
		return releaseLimit(releaseHoldAndReturn(ctx, params.TenantID, holdID, err))
	}
	fundingSource := sourceReservation.Source
	if returnToSource {
		if err := json.Unmarshal(fundingSource.WithdrawalMethod, &destinationDetails); err != nil {
			releaseErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseFundingSourceWithdrawal, walletstore.ReleaseFundingSourceWithdrawalParams{
				TenantID: params.TenantID, WorkflowID: workflowID, ReleasedAt: workflow.Now(ctx),
			}).Get(ctx, nil)
			return releaseLimit(releaseHoldAndReturn(ctx, params.TenantID, holdID, errors.Join(err, releaseErr)))
		}
	} else if err := walletstore.ValidateWithdrawalDestinationFundingSource(destination, &fundingSource); err != nil {
		releaseErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseFundingSourceWithdrawal, walletstore.ReleaseFundingSourceWithdrawalParams{
			TenantID: params.TenantID, WorkflowID: workflowID, ReleasedAt: workflow.Now(ctx),
		}).Get(ctx, nil)
		return releaseLimit(releaseHoldAndReturn(ctx, params.TenantID, holdID, errors.Join(err, releaseErr)))
	}

	releaseHold := func(cause error) error {
		releaseErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseFundingSourceWithdrawal, walletstore.ReleaseFundingSourceWithdrawalParams{
			TenantID: params.TenantID, WorkflowID: workflowID, ReleasedAt: workflow.Now(ctx),
		}).Get(ctx, nil)
		return releaseLimit(releaseHoldAndReturn(ctx, params.TenantID, holdID, errors.Join(cause, releaseErr)))
	}

	if params.ApprovalRequired {
		decisionDeadline := hold.ExpiresAt
		if pspTxn.DecisionDeadlineAt.Valid && pspTxn.DecisionDeadlineAt.Time.Before(decisionDeadline) {
			decisionDeadline = pspTxn.DecisionDeadlineAt.Time
		}
		decision, err := awaitWithdrawalApproval(ctx, params, pspTxn.ID, decisionDeadline)
		if err != nil {
			return releaseHold(err)
		}
		if !decision.Approved {
			if err := validateWithdrawalApprovalDecision(decision); err != nil {
				return releaseHold(err)
			}
			rejectMeta, err := auditMetadata(map[string]any{
				"client_reference": params.Request.ClientReference,
				"amount":           params.Request.Amount,
				"currency":         params.Request.Currency,
				"operator_id":      decision.DecidedByOperatorID,
				"reason":           decision.Reason,
			})
			if err != nil {
				return releaseHold(err)
			}
			rejectEvent := walletstore.AuditEvent{
				TenantID:   params.TenantID,
				EventType:  "wallet.withdrawal",
				ActorType:  "operator",
				ActorID:    fmt.Sprintf("%d", decision.DecidedByOperatorID),
				TargetType: sql.NullString{String: "wallet", Valid: true},
				TargetID:   sql.NullString{String: walletID.String(), Valid: true},
				Action:     "rejected",
				Metadata:   rejectMeta,
				WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
				RequestID:  sql.NullString{String: params.Request.ClientReference, Valid: params.Request.ClientReference != ""},
			}
			if err := recordAuditEvent(ctx, rejectEvent); err != nil {
				return releaseHold(err)
			}
			return releaseHold(walletstore.ErrApprovalRejected)
		}
		if err := validateWithdrawalApprovalDecision(decision); err != nil {
			return releaseHold(err)
		}
	}

	payout := params.Request
	payout.Destination = destinationDetails
	if payout.Metadata == nil {
		payout.Metadata = map[string]any{}
	}
	if destinationID > 0 {
		payout.Metadata["destination_id"] = destinationID
	}
	payout.Metadata["funding_source_id"] = fundingSource.ID

	payoutParams := walletactivity.SendPayoutParams{
		TenantID:     params.TenantID,
		ProviderCode: providerCode,
		Request:      payout,
		Region:       params.Region,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityCommitHold, params.TenantID, holdID).Get(ctx, nil); err != nil {
		return releaseHold(err)
	}

	var result walletpsp.PayoutResult
	sendErr := workflow.ExecuteActivity(pspRemoteActivityContext(ctx), walletactivity.ActivitySendPayout, payoutParams).Get(ctx, &result)
	var statusResult walletpsp.TxStatus
	var workflowSignalPending bool
	if sendErr != nil {
		if !isPSPDispatchDefinitivelyNotAccepted(sendErr) {
			statusResult, workflowSignalPending, err = updatePSPTransactionFromStatus(ctx, params.TenantID, params.Request.ClientReference, walletpsp.TxStatus{
				Status: walletstore.PSPStatusProcessing,
				RawResponse: map[string]any{
					"dispatch_error": sendErr.Error(), "dispatch_outcome": "unknown",
				},
			})
			if err != nil {
				return errors.Join(sendErr, err)
			}
		} else {
			statusResult, workflowSignalPending, err = updatePSPTransactionFromStatus(ctx, params.TenantID, params.Request.ClientReference, walletpsp.TxStatus{
				Status: walletstore.PSPStatusFailed,
				RawResponse: map[string]any{
					"dispatch_error": sendErr.Error(),
					"rejected":       true,
				},
			})
			if err != nil {
				return releaseHold(errors.Join(sendErr, err))
			}
			failMeta, metaErr := auditMetadata(map[string]any{
				"client_reference": params.Request.ClientReference,
				"provider_code":    providerCode,
				"amount":           params.Request.Amount,
				"currency":         params.Request.Currency,
				"error":            sendErr.Error(),
			})
			if metaErr != nil {
				return releaseHold(errors.Join(sendErr, metaErr))
			}
			failEvent := walletstore.AuditEvent{
				TenantID:   params.TenantID,
				EventType:  "wallet.withdrawal",
				ActorType:  "workflow",
				ActorID:    workflowID,
				TargetType: sql.NullString{String: "wallet", Valid: true},
				TargetID:   sql.NullString{String: walletID.String(), Valid: true},
				Action:     "rejected",
				Metadata:   failMeta,
				WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
				RequestID:  sql.NullString{String: params.Request.ClientReference, Valid: params.Request.ClientReference != ""},
			}
			if auditErr := recordAuditEvent(ctx, failEvent); auditErr != nil {
				return releaseHold(errors.Join(sendErr, auditErr))
			}
		}
	} else {
		statusResult = statusFromPayoutResult(result)
		statusResult, workflowSignalPending, err = updatePSPTransactionFromStatus(ctx, params.TenantID, params.Request.ClientReference, statusResult)
		if err != nil {
			return err
		}
	}
	if !isTerminalPSPStatus(statusResult.Status) {
		latest, err := loadPSPTransaction(ctx, params.TenantID, params.Request.ClientReference)
		if err != nil {
			return err
		}
		latestStatus, err := statusFromPSPTransaction(latest)
		if err != nil {
			return err
		}
		mergePSPStatus(&statusResult, latestStatus)
		workflowSignalPending = pspWorkflowSignalPending(latest)
	}
	finalStatus, err := awaitTerminalPSPStatus(ctx, params.TenantID, params.Request.ClientReference, statusResult, workflowSignalPending)
	if err != nil {
		return err
	}
	if finalStatus.Status != "success" {
		failMeta, metaErr := auditMetadata(map[string]any{
			"client_reference": params.Request.ClientReference,
			"provider_code":    providerCode,
			"psp_status":       finalStatus.Status,
			"amount":           params.Request.Amount,
			"currency":         params.Request.Currency,
		})
		if metaErr != nil {
			return releaseHold(metaErr)
		}
		failEvent := walletstore.AuditEvent{
			TenantID:   params.TenantID,
			EventType:  "wallet.withdrawal",
			ActorType:  "workflow",
			ActorID:    workflowID,
			TargetType: sql.NullString{String: "wallet", Valid: true},
			TargetID:   sql.NullString{String: walletID.String(), Valid: true},
			Action:     "failed",
			Metadata:   failMeta,
			WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
			RequestID:  sql.NullString{String: params.Request.ClientReference, Valid: params.Request.ClientReference != ""},
		}
		if auditErr := recordAuditEvent(ctx, failEvent); auditErr != nil {
			return releaseHold(auditErr)
		}
		return releaseHold(fmt.Errorf("withdrawal status %s", finalStatus.Status))
	}
	if err := validateSuccessfulPayoutStatus(finalStatus, params.Request); err != nil {
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
			Amount:     validation.WalletDebitAmount,
			Currency:   validation.Currency,
			FxRate:     validation.AppliedFXRate,
			FxSource:   validation.AppliedFXSource,
		},
	}
	if validation.AppliedFXRate.Valid {
		amounts[1].FxBaseCurrency = validation.PayoutCurrency
		amounts[1].FxQuoteCurrency = validation.WalletCurrency
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

	var treasury walletstore.Wallet
	treasuryParams := walletactivity.EnsureSystemWalletParams{
		TenantID:   params.TenantID,
		Currency:   validation.Currency,
		WalletCode: walletstore.SystemTreasury,
		KYCTier:    walletstore.KYCTierUnverified,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, treasuryParams).Get(ctx, &treasury); err != nil {
		return err
	}

	feeAmount := int64(0)
	if validation.Fee != nil {
		feeAmount = validation.Fee.TotalFee
	}
	transfers := []walletstore.SettlementTransfer{{
		DebitWalletID:  walletID,
		CreditWalletID: treasury.ID,
		Amount:         validation.WalletDebitAmount,
		Description:    "withdrawal",
	}}
	if feeAmount > 0 {
		var feesWallet walletstore.Wallet
		feesParams := walletactivity.EnsureSystemWalletParams{
			TenantID:   params.TenantID,
			Currency:   validation.Currency,
			WalletCode: walletstore.SystemFees,
			KYCTier:    walletstore.KYCTierUnverified,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, feesParams).Get(ctx, &feesWallet); err != nil {
			return err
		}
		transfers = append(transfers, walletstore.SettlementTransfer{
			DebitWalletID:  walletID,
			CreditWalletID: feesWallet.ID,
			Amount:         feeAmount,
			Description:    "withdrawal fee",
		})
	}
	heldSettlement := walletstore.HeldWithdrawalSettlementParams{
		HoldID: holdID,
		Settlement: walletstore.MultiLegSettlementParams{
			TenantID:       params.TenantID,
			IdempotencyKey: params.Request.ClientReference + ":withdrawal",
			Currency:       validation.Currency,
			ReferenceType:  "withdrawal",
			ReferenceID:    params.Request.ClientReference,
			Transfers:      transfers,
			LimitUsage:     limitUsage,
		},
		FundingSourceID:            fundingSource.ID,
		FundingReservationID:       sourceReservation.Reservation.ID,
		WithdrawalDestinationID:    destinationID,
		FundingTransferIndex:       0,
		FundingReservationProvider: providerCode,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateHeldWithdrawalSettlement, heldSettlement).Get(ctx, nil); err != nil {
		return err
	}
	var posted walletstore.MultiLegSettlementResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteHeldWithdrawalSettlement, heldSettlement).Get(ctx, &posted); err != nil {
		return err
	}
	metadata, err := auditMetadata(map[string]any{
		"client_reference":    params.Request.ClientReference,
		"provider_code":       providerCode,
		"psp_transaction_id":  finalStatus.ProviderTxID,
		"payout_amount":       params.Request.Amount,
		"payout_currency":     params.Request.Currency,
		"wallet_debit_amount": validation.WalletDebitAmount,
		"wallet_currency":     validation.WalletCurrency,
		"fee_amount":          feeAmount,
		"destination_id":      destinationID,
		"funding_source_id":   fundingSource.ID,
		"ledger_transaction":  posted.TransactionID,
	})
	if err != nil {
		return err
	}
	event := walletstore.AuditEvent{
		TenantID:   params.TenantID,
		EventType:  "wallet.withdrawal",
		ActorType:  params.OwnerType,
		ActorID:    params.OwnerID,
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   sql.NullString{String: walletID.String(), Valid: true},
		Action:     "completed",
		Metadata:   metadata,
		WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		RequestID:  sql.NullString{String: params.Request.ClientReference, Valid: params.Request.ClientReference != ""},
	}
	if err := recordAuditEvent(ctx, event); err != nil {
		return err
	}
	return nil
}

func isPSPDispatchRejected(err error) bool {
	var applicationError *temporal.ApplicationError
	return errors.As(err, &applicationError) && applicationError.Type() == walletactivity.PSPDispatchRejectedErrorType
}

func isPSPDispatchNotAttempted(err error) bool {
	var applicationError *temporal.ApplicationError
	return errors.As(err, &applicationError) && applicationError.Type() == walletactivity.PSPDispatchNotAttemptedErrorType
}

func isPSPDispatchDefinitivelyNotAccepted(err error) bool {
	return isPSPDispatchRejected(err) || isPSPDispatchNotAttempted(err)
}

func pspRemoteActivityContext(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    30 * time.Second,
		ScheduleToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    3,
		},
	})
}

func P2P(ctx workflow.Context, params P2PParams) error {
	tenantID, err := walletstore.ValidateTenantID(params.TenantID)
	if err != nil {
		return err
	}
	params.TenantID = tenantID
	if missingRequiredText(params.IdempotencyKey) {
		return walletstore.ErrMissingIdempotencyKey
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
		return walletstore.ErrMissingWorkflowID
	}

	var storedCommand walletstore.P2PCommand
	if err := workflow.ExecuteActivity(
		ctx,
		walletactivity.ActivityGetP2PCommand,
		params.TenantID,
		params.IdempotencyKey,
	).Get(ctx, &storedCommand); err != nil {
		return err
	}
	command, err := walletstore.DecodeP2PCommand(&storedCommand, params.TenantID, params.IdempotencyKey, workflowID)
	if err != nil {
		return err
	}
	if missingRequiredText(command.ReferenceID) {
		return walletstore.ErrMissingReferenceID
	}
	if missingRequiredText(command.Currency) {
		return walletstore.ErrMissingCurrency
	}
	if missingRequiredText(command.FromWalletID) || missingRequiredText(command.ToWalletID) {
		return walletstore.ErrMissingWalletID
	}
	if command.Amount <= 0 {
		return walletstore.ErrInvalidAmount
	}
	if missingRequiredText(command.FromOwnerType) || missingRequiredText(command.ToOwnerType) {
		return walletstore.ErrMissingOwnerType
	}
	if missingRequiredText(command.FromOwnerID) || missingRequiredText(command.ToOwnerID) {
		return walletstore.ErrMissingOwnerID
	}
	fromID, err := uuid.Parse(command.FromWalletID)
	if err != nil {
		return walletstore.ErrMissingWalletID
	}
	toID, err := uuid.Parse(command.ToWalletID)
	if err != nil {
		return walletstore.ErrMissingWalletID
	}
	if fromID == toID {
		return walletstore.ErrInvalidWalletPair
	}

	validationReq := walletvalidation.P2PValidationRequest{
		TenantID:        params.TenantID,
		TransactionType: "p2p",
		FromWalletID:    fromID,
		ToWalletID:      toID,
		Currency:        command.Currency,
		Amount:          command.Amount,
		FromOwnerType:   command.FromOwnerType,
		FromOwnerID:     command.FromOwnerID,
		ToOwnerType:     command.ToOwnerType,
		ToOwnerID:       command.ToOwnerID,
	}
	var validation walletvalidation.P2PValidationResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateP2PTransfer, validationReq).Get(ctx, &validation); err != nil {
		return err
	}
	feeAmount := int64(0)
	if validation.Fee != nil {
		feeAmount = validation.Fee.TotalFee
	}
	transfers := []walletstore.SettlementTransfer{{
		DebitWalletID:  fromID,
		CreditWalletID: toID,
		Amount:         command.Amount,
		Description:    command.Description,
	}}
	if feeAmount > 0 {
		var feesWallet walletstore.Wallet
		feesParams := walletactivity.EnsureSystemWalletParams{
			TenantID:   params.TenantID,
			Currency:   command.Currency,
			WalletCode: walletstore.SystemFees,
			KYCTier:    walletstore.KYCTierUnverified,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, feesParams).Get(ctx, &feesWallet); err != nil {
			return err
		}
		transfers = append(transfers, walletstore.SettlementTransfer{
			DebitWalletID:  fromID,
			CreditWalletID: feesWallet.ID,
			Amount:         feeAmount,
			Description:    "p2p fee",
		})
	}
	settlement := walletstore.MultiLegSettlementParams{
		TenantID:       params.TenantID,
		IdempotencyKey: params.IdempotencyKey,
		Currency:       command.Currency,
		ReferenceType:  "p2p",
		ReferenceID:    command.ReferenceID,
		Transfers:      transfers,
		LimitUsage: walletstore.LimitUsageParams{
			TenantID:        params.TenantID,
			CommandID:       "p2p:" + params.IdempotencyKey,
			WalletID:        fromID,
			TransactionType: "p2p",
			Currency:        command.Currency,
			Amount:          command.Amount,
		},
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateMultiLegSettlement, settlement).Get(ctx, nil); err != nil {
		return err
	}
	var result walletstore.MultiLegSettlementResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteMultiLegSettlement, settlement).Get(ctx, &result); err != nil {
		return err
	}
	meta, err := auditMetadata(map[string]any{
		"reference_id":       command.ReferenceID,
		"amount":             command.Amount,
		"currency":           command.Currency,
		"fee_amount":         feeAmount,
		"ledger_transaction": result.TransactionID,
	})
	if err != nil {
		return err
	}
	debitEvent := walletstore.AuditEvent{
		TenantID:   params.TenantID,
		EventType:  "wallet.p2p",
		ActorType:  command.FromOwnerType,
		ActorID:    command.FromOwnerID,
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   sql.NullString{String: fromID.String(), Valid: true},
		Action:     "debit",
		Metadata:   meta,
		WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		RequestID:  sql.NullString{String: command.ReferenceID, Valid: command.ReferenceID != ""},
	}
	if err := recordAuditEvent(ctx, debitEvent); err != nil {
		return err
	}
	creditEvent := walletstore.AuditEvent{
		TenantID:   params.TenantID,
		EventType:  "wallet.p2p",
		ActorType:  command.ToOwnerType,
		ActorID:    command.ToOwnerID,
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   sql.NullString{String: toID.String(), Valid: true},
		Action:     "credit",
		Metadata:   meta,
		WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		RequestID:  sql.NullString{String: command.ReferenceID, Valid: command.ReferenceID != ""},
	}
	if err := recordAuditEvent(ctx, creditEvent); err != nil {
		return err
	}
	return nil
}

func ManualTransfer(ctx workflow.Context, params ManualTransferParams) error {
	tenantID, err := walletstore.ValidateTenantID(params.TenantID)
	if err != nil {
		return err
	}
	params.TenantID = tenantID
	if missingRequiredText(params.IdempotencyKey) {
		return walletstore.ErrMissingIdempotencyKey
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
		return walletstore.ErrMissingWorkflowID
	}

	var stored walletstore.ManualTransfer
	if err := workflow.ExecuteActivity(
		ctx,
		walletactivity.ActivityGetManualTransferByWorkflow,
		params.TenantID,
		workflowID,
	).Get(ctx, &stored); err != nil {
		return err
	}
	if stored.TenantID != params.TenantID ||
		stored.WorkflowID != workflowID ||
		stored.IdempotencyKey != params.IdempotencyKey {
		return walletstore.ErrDuplicateManualTransfer
	}
	walletID, err := uuid.Parse(stored.WalletID.String)
	if err != nil || !stored.WalletID.Valid || walletID == uuid.Nil {
		return walletstore.ErrMissingWalletID
	}
	requestMeta, err := auditMetadata(map[string]any{
		"transfer_type": stored.TransferType,
		"amount":        stored.Amount,
		"currency":      stored.Currency,
		"reason":        stored.Reason,
	})
	if err != nil {
		return err
	}
	requestEvent := walletstore.AuditEvent{
		TenantID:   stored.TenantID,
		EventType:  "wallet.manual_transfer",
		ActorType:  "operator",
		ActorID:    fmt.Sprintf("%d", stored.RequestedByOperatorID),
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   stored.WalletID,
		Action:     "requested",
		Metadata:   requestMeta,
		WorkflowID: sql.NullString{String: stored.WorkflowID, Valid: true},
		RequestID:  sql.NullString{String: stored.IdempotencyKey, Valid: true},
	}
	if err := recordAuditEvent(ctx, requestEvent); err != nil {
		return err
	}

	var holdID int64
	holdDeadline := stored.DecisionDeadlineAt
	if walletstore.IsManualTransferDebit(stored.TransferType) {
		holdParams := walletstore.HoldParams{
			TenantID:       stored.TenantID,
			WalletID:       walletID,
			Amount:         stored.Amount,
			Reason:         "manual_transfer",
			ReferenceType:  stored.TransferType,
			ReferenceID:    workflowID,
			IdempotencyKey: stored.IdempotencyKey + ":hold",
			ExpiresAt:      holdDeadline,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateHold, holdParams).Get(ctx, nil); err != nil {
			return err
		}
		var hold walletstore.BalanceHold
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityCreateHold, holdParams).Get(ctx, &hold); err != nil {
			return err
		}
		holdID = hold.ID
		holdDeadline = hold.ExpiresAt
	}
	if stored.DecisionDeadlineAt.Before(holdDeadline) {
		holdDeadline = stored.DecisionDeadlineAt
	}

	decision, err := awaitManualTransferDecision(ctx, stored.TenantID, stored.ID, holdDeadline)
	if err != nil {
		update := walletstore.ManualTransferStatusUpdate{
			Status:          ManualTransferStatusRejected,
			RejectionReason: sql.NullString{String: err.Error(), Valid: true},
		}
		releaseErr := releaseBalanceHold(ctx, stored.TenantID, holdID)
		updateErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, stored.TenantID, workflowID, update).Get(ctx, nil)
		return errors.Join(err, releaseErr, updateErr)
	}
	if err := validateManualTransferDecision(stored.RequestedByOperatorID, decision); err != nil {
		update := walletstore.ManualTransferStatusUpdate{
			Status:          ManualTransferStatusRejected,
			RejectionReason: sql.NullString{String: err.Error(), Valid: true},
		}
		releaseErr := releaseBalanceHold(ctx, stored.TenantID, holdID)
		updateErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, stored.TenantID, workflowID, update).Get(ctx, nil)
		return errors.Join(err, releaseErr, updateErr)
	}

	now := workflow.Now(ctx)
	if decision.Approved {
		if err := validateManualTransferDecisionText(decision); err != nil {
			return releaseHoldAndReturn(ctx, stored.TenantID, holdID, err)
		}
		if holdID > 0 {
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityCommitHold, stored.TenantID, holdID).Get(ctx, nil); err != nil {
				update := walletstore.ManualTransferStatusUpdate{
					Status:          ManualTransferStatusRejected,
					RejectionReason: sql.NullString{String: err.Error(), Valid: true},
				}
				releaseErr := releaseBalanceHold(ctx, stored.TenantID, holdID)
				updateErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, stored.TenantID, workflowID, update).Get(ctx, nil)
				return errors.Join(err, releaseErr, updateErr)
			}
		}
		approval := walletstore.ManualTransferApproval{
			TenantID:            stored.TenantID,
			ManualTransferID:    stored.ID,
			DecidedByOperatorID: decision.DecidedByOperatorID,
			Decision:            ManualTransferStatusApproved,
			Reason:              sql.NullString{String: decision.Reason, Valid: decision.Reason != ""},
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityAddManualTransferApproval, approval).Get(ctx, nil); err != nil {
			return releaseHoldAndReturn(ctx, stored.TenantID, holdID, err)
		}
		update := walletstore.ManualTransferStatusUpdate{
			Status:               ManualTransferStatusApproved,
			ApprovedByOperatorID: sql.NullInt64{Int64: decision.DecidedByOperatorID, Valid: true},
			ApprovedAt:           sql.NullTime{Time: now, Valid: true},
			ProofOfPayment:       sql.NullString{String: decision.ProofOfPayment, Valid: true},
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, stored.TenantID, workflowID, update).Get(ctx, nil); err != nil {
			return releaseHoldAndReturn(ctx, stored.TenantID, holdID, err)
		}
		var treasury walletstore.Wallet
		treasuryParams := walletactivity.EnsureSystemWalletParams{
			TenantID:   stored.TenantID,
			Currency:   stored.Currency,
			WalletCode: walletstore.SystemTreasury,
			KYCTier:    walletstore.KYCTierUnverified,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, treasuryParams).Get(ctx, &treasury); err != nil {
			return releaseHoldAndReturn(ctx, stored.TenantID, holdID, err)
		}

		debitID := walletID
		creditID := treasury.ID
		if stored.TransferType == walletstore.ManualTransferTypeCredit {
			debitID = treasury.ID
			creditID = walletID
		}
		entry := walletstore.DoubleEntryParams{
			TenantID:       stored.TenantID,
			IdempotencyKey: stored.IdempotencyKey + ":ledger",
			Currency:       stored.Currency,
			ReferenceType:  stored.TransferType,
			ReferenceID:    workflowID,
			DebitWalletID:  debitID,
			CreditWalletID: creditID,
			Amount:         stored.Amount,
			Description:    stored.Reason,
		}
		var posted walletstore.DoubleEntryResult
		if holdID > 0 {
			heldEntry := walletstore.HeldDoubleEntryParams{HoldID: holdID, Entry: entry}
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateHeldDoubleEntry, heldEntry).Get(ctx, nil); err != nil {
				return releaseHoldAndReturn(ctx, stored.TenantID, holdID, err)
			}
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteHeldDoubleEntry, heldEntry).Get(ctx, &posted); err != nil {
				return releaseHoldAndReturn(ctx, stored.TenantID, holdID, err)
			}
		} else if stored.TransferType == walletstore.ManualTransferTypeCredit {
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateSystemDebitDoubleEntry, entry).Get(ctx, nil); err != nil {
				return err
			}
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteSystemDebitDoubleEntry, entry).Get(ctx, &posted); err != nil {
				return err
			}
		} else {
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDoubleEntry, entry).Get(ctx, nil); err != nil {
				return err
			}
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteDoubleEntry, entry).Get(ctx, &posted); err != nil {
				return err
			}
		}
		_ = posted

		complete := walletstore.ManualTransferStatusUpdate{
			Status:      ManualTransferStatusCompleted,
			CompletedAt: sql.NullTime{Time: workflow.Now(ctx), Valid: true},
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, stored.TenantID, workflowID, complete).Get(ctx, nil); err != nil {
			return err
		}
		completeMeta, err := auditMetadata(map[string]any{
			"transfer_type": stored.TransferType,
			"amount":        stored.Amount,
			"currency":      stored.Currency,
			"operator_id":   decision.DecidedByOperatorID,
		})
		if err != nil {
			return err
		}
		completeEvent := walletstore.AuditEvent{
			TenantID:   stored.TenantID,
			EventType:  "wallet.manual_transfer",
			ActorType:  "operator",
			ActorID:    fmt.Sprintf("%d", decision.DecidedByOperatorID),
			TargetType: sql.NullString{String: "wallet", Valid: true},
			TargetID:   stored.WalletID,
			Action:     "completed",
			Metadata:   completeMeta,
			WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
			RequestID:  sql.NullString{String: stored.IdempotencyKey, Valid: true},
		}
		return recordAuditEvent(ctx, completeEvent)
	}

	if err := validateManualTransferDecisionText(decision); err != nil {
		return releaseHoldAndReturn(ctx, stored.TenantID, holdID, err)
	}
	rejection := walletstore.ManualTransferApproval{
		TenantID:            stored.TenantID,
		ManualTransferID:    stored.ID,
		DecidedByOperatorID: decision.DecidedByOperatorID,
		Decision:            ManualTransferStatusRejected,
		Reason:              sql.NullString{String: decision.Reason, Valid: true},
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityAddManualTransferApproval, rejection).Get(ctx, nil); err != nil {
		return releaseHoldAndReturn(ctx, stored.TenantID, holdID, err)
	}
	rejectMeta, err := auditMetadata(map[string]any{
		"transfer_type": stored.TransferType,
		"amount":        stored.Amount,
		"currency":      stored.Currency,
		"operator_id":   decision.DecidedByOperatorID,
		"reason":        decision.Reason,
	})
	if err != nil {
		return releaseHoldAndReturn(ctx, stored.TenantID, holdID, err)
	}
	rejectEvent := walletstore.AuditEvent{
		TenantID:   stored.TenantID,
		EventType:  "wallet.manual_transfer",
		ActorType:  "operator",
		ActorID:    fmt.Sprintf("%d", decision.DecidedByOperatorID),
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   stored.WalletID,
		Action:     "rejected",
		Metadata:   rejectMeta,
		WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		RequestID:  sql.NullString{String: stored.IdempotencyKey, Valid: true},
	}
	if err := recordAuditEvent(ctx, rejectEvent); err != nil {
		return releaseHoldAndReturn(ctx, stored.TenantID, holdID, err)
	}
	if holdID > 0 {
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateReleaseHold, stored.TenantID, holdID).Get(ctx, nil); err != nil {
			return err
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseHold, stored.TenantID, holdID).Get(ctx, nil); err != nil {
			return err
		}
	}
	update := walletstore.ManualTransferStatusUpdate{
		Status:          ManualTransferStatusRejected,
		RejectionReason: sql.NullString{String: decision.Reason, Valid: true},
	}
	return workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, stored.TenantID, workflowID, update).Get(ctx, nil)
}

func Reconciliation(ctx workflow.Context, params ReconciliationParams) error {
	tenantID, err := walletstore.ValidateTenantID(params.TenantID)
	if err != nil {
		return err
	}
	params.TenantID = tenantID
	if params.Status == "" {
		return walletstore.ErrMissingStatus
	}
	if params.Limit <= 0 {
		return walletstore.ErrInvalidLimit
	}
	startTime := params.StartTime
	endTime := params.EndTime
	if startTime.IsZero() && endTime.IsZero() {
		if params.LookbackHours <= 0 {
			return walletstore.ErrMissingStartTime
		}
		endTime = workflow.Now(ctx)
		startTime = endTime.Add(time.Duration(-params.LookbackHours) * time.Hour)
	}
	if startTime.IsZero() {
		return walletstore.ErrMissingStartTime
	}
	if endTime.IsZero() {
		return walletstore.ErrMissingEndTime
	}
	if startTime.After(endTime) {
		return walletstore.ErrInvalidTimeRange
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})
	info := workflow.GetInfo(ctx)
	workflowID := ""
	if info != nil {
		workflowID = info.WorkflowExecution.ID
	}

	listParams := walletactivity.ListPSPTransactionsByStatusParams{
		TenantID: params.TenantID,
		Status:   params.Status,
		Start:    startTime,
		End:      endTime,
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
		referenceType := referenceTypeForPSPDirection(txn.Direction)
		if referenceType == "" {
			missing = append(missing, txn.ClientReference)
			logger.Warn("missing ledger transaction for psp transaction with unknown direction", "client_reference", txn.ClientReference, "direction", txn.Direction)
			continue
		}
		var exists bool
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityLedgerTransactionExistsByReference, params.TenantID, referenceType, txn.ClientReference).Get(ctx, &exists); err != nil {
			return err
		}
		if !exists {
			missing = append(missing, txn.ClientReference)
			logger.Warn("missing ledger transaction for psp transaction", "client_reference", txn.ClientReference)
		}
	}
	if len(missing) > 0 {
		meta, err := auditMetadata(map[string]any{
			"status":             params.Status,
			"start_time":         params.StartTime.Format(time.RFC3339),
			"end_time":           params.EndTime.Format(time.RFC3339),
			"missing_count":      len(missing),
			"missing_references": missing,
		})
		if err != nil {
			return err
		}
		event := walletstore.AuditEvent{
			TenantID:   params.TenantID,
			EventType:  "wallet.reconciliation",
			ActorType:  "workflow",
			ActorID:    "reconciliation",
			Action:     "mismatch",
			Metadata:   meta,
			WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		}
		if err := recordAuditEvent(ctx, event); err != nil {
			return err
		}
		return fmt.Errorf("reconciliation mismatch: %d missing ledger entries", len(missing))
	}
	return nil
}

func referenceTypeForPSPDirection(direction string) string {
	switch direction {
	case "inbound":
		return "deposit"
	case "outbound":
		return "withdrawal"
	default:
		return ""
	}
}

func PSPStatusPoller(ctx workflow.Context, params PSPStatusPollerParams) error {
	tenantID, err := walletstore.ValidateTenantID(params.TenantID)
	if err != nil {
		return err
	}
	params.TenantID = tenantID
	if params.Limit <= 0 {
		return walletstore.ErrInvalidLimit
	}
	if params.PollIntervalSeconds <= 0 {
		return walletstore.ErrMissingStatusTimeout
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})
	logger := workflow.GetLogger(ctx)

	var expiredHolds int
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExpireHolds, params.TenantID, params.Limit).Get(ctx, &expiredHolds); err != nil {
		return err
	}

	var txns []walletstore.PSPTransaction
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityListPSPTransactionsForPolling, params.TenantID, params.Limit).Get(ctx, &txns); err != nil {
		return err
	}

	now := workflow.Now(ctx)
	nextPoll := sql.NullTime{Time: now.Add(time.Duration(params.PollIntervalSeconds) * time.Second), Valid: true}
	lockExpiry := now.Add(time.Duration(params.PollIntervalSeconds) * time.Second)

	for _, txn := range txns {
		lockToken, err := newLockToken(ctx)
		if err != nil {
			return err
		}
		lockParams := walletactivity.TryAcquirePSPTransactionLockParams{
			TenantID:        params.TenantID,
			ClientReference: txn.ClientReference,
			LockToken:       lockToken,
			LockExpiresAt:   lockExpiry,
		}
		var acquired bool
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityTryAcquirePSPTransactionLock, lockParams).Get(ctx, &acquired); err != nil {
			return err
		}
		if !acquired {
			continue
		}
		locked, err := loadPSPTransaction(ctx, params.TenantID, txn.ClientReference)
		if err != nil {
			return err
		}
		txn = *locked
		pendingSignal := len(txn.WorkflowSignalPayload) > 0 && !txn.WorkflowSignalDeliveredAt.Valid
		if txn.WorkflowSignalDeliveredAt.Valid {
			continue
		}
		if pendingSignal {
			signaled, err := deliverPSPStatusSignal(ctx, txn, lockParams.LockToken, now)
			if err != nil {
				if signaled {
					return err
				}
				logger.Warn("psp status signal remains pending", "workflow_id", txn.WorkflowID.String, "client_reference", txn.ClientReference, "error", err)
			}
			continue
		}
		direction := "deposit"
		if txn.Direction == "outbound" {
			direction = "withdrawal"
		}
		statusParams := walletactivity.GetStatusParams{
			TenantID:        params.TenantID,
			ProviderCode:    txn.PSPProvider,
			TransactionID:   txn.PSPTransactionID.String,
			IdempotencyKey:  txn.IdempotencyKey,
			ClientReference: txn.ClientReference,
			Amount:          txn.Amount,
			Currency:        txn.Currency,
			Direction:       direction,
			Region:          regionFromRawRequest(txn.RawRequest),
		}
		var status walletpsp.TxStatus
		pollErr := workflow.ExecuteActivity(pspRemoteActivityContext(ctx), walletactivity.ActivityGetTransactionStatus, statusParams).Get(ctx, &status)
		update := walletstore.PSPStatusUpdate{
			Status:       txn.Status,
			LastPolledAt: sql.NullTime{Time: now, Valid: true},
			NextPollAt:   nextPoll,
			RetryCount:   txn.RetryCount + 1,
			LockToken:    sql.NullString{String: lockParams.LockToken, Valid: true},
		}
		if pollErr != nil || status.Status == "" {
			update.LastErrorType = sql.NullString{String: "poll_error", Valid: true}
			update.LastErrorAt = sql.NullTime{Time: now, Valid: true}
		} else {
			status.Status = normalizePSPStatus(status.Status)
			update.Status = status.Status
			if status.ProviderTxID != "" {
				update.PSPTransactionID = sql.NullString{String: status.ProviderTxID, Valid: true}
			}
			if len(status.RawResponse) > 0 {
				raw, err := auditMetadata(status.RawResponse)
				if err != nil {
					return err
				}
				update.RawResponse = walletstore.RawJSON(raw)
			}
		}
		if update.Status == "success" && !txn.ConfirmedAt.Valid {
			update.ConfirmedAt = sql.NullTime{Time: now, Valid: true}
		}
		var workflowSignal *walletstore.PSPWorkflowSignal
		if pollErr == nil && isTerminalPSPStatus(update.Status) && txn.WorkflowID.Valid {
			if status.ProviderTxID == "" && txn.PSPTransactionID.Valid {
				status.ProviderTxID = txn.PSPTransactionID.String
			}
			var err error
			workflowSignal, err = pspWorkflowSignalFromStatus(status)
			if err != nil {
				return err
			}
		}
		updateParams := walletactivity.UpdatePSPTransactionStatusParams{
			TenantID:        params.TenantID,
			ClientReference: txn.ClientReference,
			Update:          update,
			WorkflowSignal:  workflowSignal,
		}
		var stored walletstore.PSPTransaction
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdatePSPTransactionStatus, updateParams).Get(ctx, &stored); err != nil {
			return err
		}
		if len(stored.WorkflowSignalPayload) > 0 && !stored.WorkflowSignalDeliveredAt.Valid {
			signaled, err := deliverPSPStatusSignal(ctx, stored, lockParams.LockToken, now)
			if err != nil {
				if signaled {
					return err
				}
				logger.Warn("psp status signal remains pending", "workflow_id", stored.WorkflowID.String, "client_reference", stored.ClientReference, "error", err)
			}
		}
	}
	return nil
}

func deliverPSPStatusSignal(ctx workflow.Context, txn walletstore.PSPTransaction, lockToken string, deliveredAt time.Time) (bool, error) {
	if !txn.WorkflowID.Valid {
		return false, walletstore.ErrMissingWorkflowID
	}
	if len(txn.WorkflowSignalPayload) == 0 {
		return false, walletstore.ErrMissingWorkflowSignal
	}
	signal, err := walletstore.ParsePSPWorkflowSignal(txn.WorkflowSignalPayload)
	if err != nil {
		return false, err
	}
	if err := workflow.SignalExternalWorkflow(ctx, txn.WorkflowID.String, "", PSPStatusUpdateSignal, signal).Get(ctx, nil); err != nil {
		latest, loadErr := loadPSPTransaction(ctx, txn.TenantID, txn.ClientReference)
		if loadErr == nil && latest.WorkflowSignalDeliveredAt.Valid {
			return true, nil
		}
		if loadErr != nil {
			return false, loadErr
		}
		return false, err
	}
	ack := walletactivity.AcknowledgePSPWorkflowSignalParams{
		TenantID:        txn.TenantID,
		ClientReference: txn.ClientReference,
		DeliveredAt:     deliveredAt,
		LockToken:       lockToken,
	}
	return true, workflow.ExecuteActivity(ctx, walletactivity.ActivityAcknowledgePSPWorkflowSignal, ack).Get(ctx, nil)
}

func loadPSPTransaction(ctx workflow.Context, tenantID, clientReference string) (*walletstore.PSPTransaction, error) {
	var txn walletstore.PSPTransaction
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityGetPSPTransactionByReference, tenantID, clientReference).Get(ctx, &txn); err != nil {
		return nil, err
	}
	return &txn, nil
}

func loadDepositIntent(ctx workflow.Context, tenantID, reference string) (*walletstore.DepositIntent, error) {
	var intent walletstore.DepositIntent
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityGetDepositIntentByReference, tenantID, reference).Get(ctx, &intent); err != nil {
		return nil, err
	}
	return &intent, nil
}

func statusFromDepositResult(result walletpsp.DepositResult) walletpsp.TxStatus {
	return walletpsp.TxStatus{
		ProviderTxID: result.ProviderTxID,
		Amount:       result.Amount,
		Currency:     result.Currency,
		Status:       normalizePSPStatus(result.Status),
		RawResponse:  result.RawResponse,
	}
}

func validateCreatedDeposit(intent *walletstore.DepositIntent, result walletpsp.DepositResult) error {
	if intent == nil || result.ClientReference != intent.IntentReference {
		return walletstore.ErrInvalidDepositIntent
	}
	if result.ProviderTxID == "" {
		return walletstore.ErrMissingPSPTransactionID
	}
	if result.Amount != intent.Amount {
		return walletstore.ErrInvalidAmount
	}
	if result.Currency != intent.Currency {
		return walletstore.ErrCurrencyMismatch
	}
	return walletstore.ValidatePSPTransactionStatus(normalizePSPStatus(result.Status))
}

func validateTerminalDepositStatus(intent *walletstore.DepositIntent, status walletpsp.TxStatus) error {
	if intent == nil || !isTerminalPSPStatus(status.Status) {
		return walletstore.ErrInvalidStatus
	}
	if status.ProviderTxID == "" {
		return walletstore.ErrMissingPSPTransactionID
	}
	if status.Amount != intent.Amount {
		return walletstore.ErrInvalidAmount
	}
	if status.Currency != intent.Currency {
		return walletstore.ErrCurrencyMismatch
	}
	return nil
}

func depositIntentMetadata(raw walletstore.RawJSON) (map[string]any, error) {
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, err
	}
	return metadata, nil
}

func statusFromPayoutResult(result walletpsp.PayoutResult) walletpsp.TxStatus {
	return walletpsp.TxStatus{
		ProviderTxID: result.ProviderTxID,
		Amount:       result.Amount,
		Currency:     result.Currency,
		Status:       normalizePSPStatus(result.Status),
		RawResponse:  result.RawResponse,
	}
}

func validateSuccessfulPayoutStatus(status walletpsp.TxStatus, request walletpsp.PayoutRequest) error {
	if normalizePSPStatus(status.Status) != walletstore.PSPStatusSuccess {
		return nil
	}
	if status.ProviderTxID == "" {
		return walletstore.ErrMissingPSPTransactionID
	}
	if status.Amount != request.Amount {
		return walletstore.ErrInvalidAmount
	}
	if status.Currency != request.Currency {
		return walletstore.ErrCurrencyMismatch
	}
	return nil
}

func pspWorkflowSignalFromStatus(status walletpsp.TxStatus) (*walletstore.PSPWorkflowSignal, error) {
	var rawResponse walletstore.RawJSON
	if status.RawResponse != nil {
		raw, err := auditMetadata(status.RawResponse)
		if err != nil {
			return nil, err
		}
		rawResponse = walletstore.RawJSON(raw)
	}
	return &walletstore.PSPWorkflowSignal{
		ProviderTxID: status.ProviderTxID,
		Amount:       status.Amount,
		Currency:     status.Currency,
		Status:       normalizePSPStatus(status.Status),
		RawResponse:  rawResponse,
	}, nil
}

func statusFromPSPWorkflowSignal(signal walletstore.PSPWorkflowSignal) (walletpsp.TxStatus, error) {
	status := walletpsp.TxStatus{
		ProviderTxID: signal.ProviderTxID,
		Amount:       signal.Amount,
		Currency:     signal.Currency,
		Status:       normalizePSPStatus(signal.Status),
	}
	if len(signal.RawResponse) > 0 {
		if err := json.Unmarshal(signal.RawResponse, &status.RawResponse); err != nil {
			return walletpsp.TxStatus{}, err
		}
	}
	return status, nil
}

func statusFromPSPTransaction(txn *walletstore.PSPTransaction) (walletpsp.TxStatus, error) {
	if txn == nil {
		return walletpsp.TxStatus{}, nil
	}
	if len(txn.WorkflowSignalPayload) > 0 {
		signal, err := walletstore.ParsePSPWorkflowSignal(txn.WorkflowSignalPayload)
		if err != nil {
			return walletpsp.TxStatus{}, err
		}
		return statusFromPSPWorkflowSignal(signal)
	}
	status := walletpsp.TxStatus{
		Amount: txn.Amount, Currency: txn.Currency, Status: normalizePSPStatus(txn.Status),
	}
	if txn.PSPTransactionID.Valid {
		status.ProviderTxID = txn.PSPTransactionID.String
	}
	if len(txn.RawResponse) == 0 {
		return status, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(txn.RawResponse, &payload); err != nil {
		return walletpsp.TxStatus{}, err
	}
	status.RawResponse = payload
	return status, nil
}

func awaitTerminalPSPStatus(ctx workflow.Context, tenantID, clientReference string, initial walletpsp.TxStatus, workflowSignalPending bool) (walletpsp.TxStatus, error) {
	current := initial
	current.Status = normalizePSPStatus(current.Status)
	if isTerminalPSPStatus(current.Status) {
		if workflowSignalPending {
			persisted, ready, err := consumePersistedPSPWorkflowSignal(ctx, tenantID, clientReference)
			if err != nil {
				return walletpsp.TxStatus{}, err
			}
			if !ready {
				return walletpsp.TxStatus{}, walletstore.ErrMissingWorkflowSignal
			}
			return persisted, nil
		}
		return current, nil
	}
	statusCh := workflow.GetSignalChannel(ctx, PSPStatusUpdateSignal)
	for {
		var wakeup any
		statusCh.Receive(ctx, &wakeup)
		persisted, ready, err := consumePersistedPSPWorkflowSignal(ctx, tenantID, clientReference)
		if err != nil {
			return walletpsp.TxStatus{}, err
		}
		if ready {
			return persisted, nil
		}
	}
}

func consumePersistedPSPWorkflowSignal(ctx workflow.Context, tenantID, clientReference string) (walletpsp.TxStatus, bool, error) {
	stored, err := acknowledgePSPWorkflowSignal(ctx, tenantID, clientReference, workflow.Now(ctx), "")
	if err != nil {
		return walletpsp.TxStatus{}, false, err
	}
	if len(stored.WorkflowSignalPayload) == 0 {
		return walletpsp.TxStatus{}, false, nil
	}
	status, err := statusFromPSPTransaction(stored)
	if err != nil {
		return walletpsp.TxStatus{}, false, err
	}
	if !isTerminalPSPStatus(status.Status) {
		return walletpsp.TxStatus{}, false, walletstore.ErrInvalidStatusTransition
	}
	return status, true, nil
}

func acknowledgePSPWorkflowSignal(ctx workflow.Context, tenantID, clientReference string, deliveredAt time.Time, lockToken string) (*walletstore.PSPTransaction, error) {
	params := walletactivity.AcknowledgePSPWorkflowSignalParams{
		TenantID:        tenantID,
		ClientReference: clientReference,
		DeliveredAt:     deliveredAt,
		LockToken:       lockToken,
	}
	var stored walletstore.PSPTransaction
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityAcknowledgePSPWorkflowSignal, params).Get(ctx, &stored); err != nil {
		return nil, err
	}
	return &stored, nil
}

func pspWorkflowSignalPending(txn *walletstore.PSPTransaction) bool {
	return txn != nil && len(txn.WorkflowSignalPayload) > 0 && !txn.WorkflowSignalDeliveredAt.Valid
}

func updatePSPTransactionFromStatus(ctx workflow.Context, tenantID, clientReference string, status walletpsp.TxStatus) (walletpsp.TxStatus, bool, error) {
	update := walletstore.PSPStatusUpdate{Status: normalizePSPStatus(status.Status)}
	if update.Status == "" {
		return walletpsp.TxStatus{}, false, walletstore.ErrMissingStatus
	}
	if status.ProviderTxID != "" {
		update.PSPTransactionID = sql.NullString{String: status.ProviderTxID, Valid: true}
	}
	if update.Status == "success" {
		update.ConfirmedAt = sql.NullTime{Time: workflow.Now(ctx), Valid: true}
	}
	if len(status.RawResponse) > 0 {
		raw, err := auditMetadata(status.RawResponse)
		if err != nil {
			return walletpsp.TxStatus{}, false, err
		}
		update.RawResponse = walletstore.RawJSON(raw)
	}
	params := walletactivity.UpdatePSPTransactionStatusParams{
		TenantID:        tenantID,
		ClientReference: clientReference,
		Update:          update,
	}
	var stored walletstore.PSPTransaction
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdatePSPTransactionStatus, params).Get(ctx, &stored); err != nil {
		return walletpsp.TxStatus{}, false, err
	}
	persisted, err := statusFromPSPTransaction(&stored)
	if err != nil {
		return walletpsp.TxStatus{}, false, err
	}
	return persisted, pspWorkflowSignalPending(&stored), nil
}

func mergePSPStatus(current *walletpsp.TxStatus, update walletpsp.TxStatus) {
	if current == nil {
		return
	}
	if update.ProviderTxID != "" {
		current.ProviderTxID = update.ProviderTxID
	}
	if update.Amount > 0 {
		current.Amount = update.Amount
	}
	if update.Currency != "" {
		current.Currency = update.Currency
	}
	if update.Status != "" {
		current.Status = normalizePSPStatus(update.Status)
	}
	if update.RawResponse != nil {
		current.RawResponse = update.RawResponse
	}
}

func normalizePSPStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func isTerminalPSPStatus(status string) bool {
	switch normalizePSPStatus(status) {
	case "success", "failed", "cancelled":
		return true
	default:
		return false
	}
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

func releaseBalanceHold(ctx workflow.Context, tenantID string, holdID int64) error {
	if holdID <= 0 {
		return nil
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateReleaseHold, tenantID, holdID).Get(ctx, nil); err != nil {
		return err
	}
	return workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseHold, tenantID, holdID).Get(ctx, nil)
}

func releaseHoldAndReturn(ctx workflow.Context, tenantID string, holdID int64, cause error) error {
	return errors.Join(cause, releaseBalanceHold(ctx, tenantID, holdID))
}

func awaitManualTransferDecision(ctx workflow.Context, tenantID string, subjectID int64, decisionDeadline time.Time) (ManualTransferDecision, error) {
	if decisionDeadline.IsZero() {
		return ManualTransferDecision{}, walletstore.ErrMissingApprovalTimeout
	}
	stored, err := awaitWorkflowDecision(ctx, walletstore.WorkflowDecisionKey{
		TenantID:   tenantID,
		WorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID,
		Kind:       walletstore.WorkflowDecisionManualTransfer,
		SubjectID:  subjectID,
	}, ManualTransferDecisionSignal, decisionDeadline, ErrManualTransferTimedOut)
	if err != nil {
		return ManualTransferDecision{}, err
	}
	return ManualTransferDecision{
		Approved:            stored.Approved,
		DecidedByOperatorID: stored.DecidedByOperatorID,
		Reason:              stored.Reason.String,
		ProofOfPayment:      stored.ProofOfPayment.String,
	}, nil
}

func validateManualTransferDecision(requestedBy int64, decision ManualTransferDecision) error {
	if requestedBy > 0 && decision.DecidedByOperatorID == requestedBy {
		return walletstore.ErrApproverIsRequester
	}
	return nil
}

func validateManualTransferDecisionText(decision ManualTransferDecision) error {
	if decision.Approved {
		if missingRequiredText(decision.ProofOfPayment) {
			return walletstore.ErrMissingProofOfPayment
		}
		return nil
	}
	if missingRequiredText(decision.Reason) {
		return walletstore.ErrMissingReason
	}
	return nil
}

func validateWithdrawalApprovalDecision(decision WithdrawalApprovalDecision) error {
	if decision.Approved {
		if missingRequiredText(decision.ProofOfPayment) {
			return walletstore.ErrMissingProofOfPayment
		}
		return nil
	}
	if missingRequiredText(decision.Reason) {
		return walletstore.ErrMissingApprovalReason
	}
	return nil
}

func awaitWithdrawalApproval(ctx workflow.Context, params withdrawalExecutionParams, subjectID int64, holdDeadline time.Time) (WithdrawalApprovalDecision, error) {
	timeout := time.Duration(params.ApprovalTimeoutSeconds) * time.Second
	if timeout <= 0 {
		return WithdrawalApprovalDecision{}, walletstore.ErrMissingApprovalTimeout
	}
	stored, err := awaitWorkflowDecision(ctx, walletstore.WorkflowDecisionKey{
		TenantID:   params.TenantID,
		WorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID,
		Kind:       walletstore.WorkflowDecisionWithdrawal,
		SubjectID:  subjectID,
	}, WithdrawalApprovalSignal, workflowDecisionDeadline(ctx, timeout, holdDeadline), ErrWithdrawalApprovalTimedOut)
	if err != nil {
		return WithdrawalApprovalDecision{}, err
	}
	return WithdrawalApprovalDecision{
		Approved:            stored.Approved,
		DecidedByOperatorID: stored.DecidedByOperatorID,
		Reason:              stored.Reason.String,
		ProofOfPayment:      stored.ProofOfPayment.String,
	}, nil
}

func awaitWorkflowDecision(ctx workflow.Context, key walletstore.WorkflowDecisionKey, signalName string, deadline time.Time, timeoutErr error) (walletstore.WorkflowDecision, error) {
	if key.SubjectID <= 0 {
		return walletstore.WorkflowDecision{}, walletstore.ErrMissingDecisionSubject
	}
	decisionCh := workflow.GetSignalChannel(ctx, signalName)
	for {
		now := workflow.Now(ctx)
		if !deadline.After(now) {
			return closeWorkflowDecisionWindow(ctx, key, timeoutErr)
		}
		remaining := deadline.Sub(now)
		if remaining < time.Millisecond {
			return closeWorkflowDecisionWindow(ctx, key, timeoutErr)
		}
		activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout:    min(5*time.Second, remaining),
			ScheduleToCloseTimeout: remaining,
		})
		var lookup walletstore.WorkflowDecisionLookup
		if err := workflow.ExecuteActivity(activityCtx, walletactivity.ActivityLookupWorkflowDecision, key).Get(ctx, &lookup); err != nil {
			if !deadline.After(workflow.Now(ctx)) {
				return closeWorkflowDecisionWindow(ctx, key, timeoutErr)
			}
			return walletstore.WorkflowDecision{}, err
		}
		if lookup.Found {
			return lookup.Decision, nil
		}

		wait := min(time.Minute, deadline.Sub(workflow.Now(ctx)))
		if wait <= 0 {
			return closeWorkflowDecisionWindow(ctx, key, timeoutErr)
		}
		timerCtx, cancelTimer := workflow.WithCancel(ctx)
		timer := workflow.NewTimer(timerCtx, wait)
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(decisionCh, func(c workflow.ReceiveChannel, more bool) {
			var ignored any
			c.Receive(ctx, &ignored)
		})
		selector.AddFuture(timer, func(workflow.Future) {})
		selector.Select(ctx)
		cancelTimer()
	}
}

func closeWorkflowDecisionWindow(ctx workflow.Context, key walletstore.WorkflowDecisionKey, timeoutErr error) (walletstore.WorkflowDecision, error) {
	var lookup walletstore.WorkflowDecisionLookup
	err := workflow.ExecuteActivity(ctx, walletactivity.ActivityCloseWorkflowDecisionWindow, walletstore.WorkflowDecisionWindowClose{
		Key: key, Reason: timeoutErr.Error(),
	}).Get(ctx, &lookup)
	if err != nil {
		return walletstore.WorkflowDecision{}, err
	}
	if lookup.Found {
		return lookup.Decision, nil
	}
	return walletstore.WorkflowDecision{}, timeoutErr
}

func workflowDecisionDeadline(ctx workflow.Context, timeout time.Duration, holdDeadline time.Time) time.Time {
	deadline := workflow.Now(ctx).Add(timeout)
	if !holdDeadline.IsZero() && holdDeadline.Before(deadline) {
		return holdDeadline
	}
	return deadline
}

type fundingSourceSpec struct {
	sourceType            string
	externalReference     string
	verificationStatus    string
	sourceDetails         map[string]any
	withdrawalMethod      map[string]any
	supportsWithdrawal    bool
	supportsWithdrawalSet bool
}

func depositFundingSource(txn *walletstore.PSPTransaction, walletID uuid.UUID, currency string, transactionExternalRef sql.NullString, fundedAt time.Time, providerPayloads ...map[string]any) (walletstore.FundingSource, error) {
	if txn == nil {
		return walletstore.FundingSource{}, walletstore.ErrPSPTransactionNotFound
	}
	providerSpec := fundingSourceSpec{}
	for _, payload := range providerPayloads {
		providerSpec = mergeFundingSourceSpecs(providerSpec, fundingSourceSpecFromPayload(payload))
	}
	if providerSpec.sourceType == "" {
		providerSpec.sourceType = "psp"
	}
	externalRef := transactionExternalRef
	if providerSpec.externalReference != "" {
		externalRef = sql.NullString{String: providerSpec.externalReference, Valid: true}
	}
	sourceDetails := providerSpec.sourceDetails
	if sourceDetails == nil {
		sourceDetails = map[string]any{}
	}
	if providerSpec.sourceType != "" {
		sourceDetails["source_type"] = providerSpec.sourceType
	}
	if providerSpec.externalReference != "" {
		sourceDetails["external_reference"] = providerSpec.externalReference
	}
	sourceDetailsRaw, err := auditMetadata(sourceDetails)
	if err != nil {
		return walletstore.FundingSource{}, err
	}
	withdrawalMethodRaw, err := auditMetadata(providerSpec.withdrawalMethod)
	if err != nil {
		return walletstore.FundingSource{}, err
	}
	verificationStatus := walletstore.FundingSourceStatusPending
	verifiedAt := sql.NullTime{}
	if providerSpec.verificationStatus == walletstore.FundingSourceStatusVerified {
		verificationStatus = walletstore.FundingSourceStatusVerified
		verifiedAt = sql.NullTime{Time: fundedAt, Valid: true}
	}
	supportsWithdrawal := verificationStatus == walletstore.FundingSourceStatusVerified &&
		providerSpec.supportsWithdrawalSet && providerSpec.supportsWithdrawal && len(withdrawalMethodRaw) > 0
	return walletstore.FundingSource{
		TenantID:           txn.TenantID,
		WalletID:           walletID,
		SourceType:         providerSpec.sourceType,
		PSPProvider:        sql.NullString{String: txn.PSPProvider, Valid: txn.PSPProvider != ""},
		ExternalReference:  externalRef,
		VerificationStatus: verificationStatus,
		VerifiedAt:         verifiedAt,
		Currency:           currency,
		SourceDetails:      sourceDetailsRaw,
		SupportsWithdrawal: supportsWithdrawal,
		WithdrawalMethod:   withdrawalMethodRaw,
	}, nil
}

func fundingSourceSpecFromPayload(payload map[string]any) fundingSourceSpec {
	if payload == nil {
		return fundingSourceSpec{}
	}
	spec := fundingSourceSpecFromMap(payload)
	if nested := mapFromAny(payload["metadata"]); nested != nil {
		spec = mergeFundingSourceSpecs(spec, fundingSourceSpecFromPayload(nested))
	}
	if nested := mapFromAny(payload["meta"]); nested != nil {
		spec = mergeFundingSourceSpecs(spec, fundingSourceSpecFromPayload(nested))
	}
	if nested := mapFromAny(payload["funding_source"]); nested != nil {
		spec = mergeFundingSourceSpecs(spec, fundingSourceSpecFromMap(nested))
	}
	if nested := mapFromAny(payload["fundingSource"]); nested != nil {
		spec = mergeFundingSourceSpecs(spec, fundingSourceSpecFromMap(nested))
	}
	return spec
}

func fundingSourceSpecFromMap(payload map[string]any) fundingSourceSpec {
	spec := fundingSourceSpec{
		sourceType:         stringFromAnyMap(payload, "source_type", "funding_source_type", "method_type", "payment_method", "payment_method_type"),
		externalReference:  stringFromAnyMap(payload, "external_reference", "funding_source_reference", "source_reference", "method_reference", "payment_method_reference", "account_reference", "card_token", "wallet_address"),
		verificationStatus: strings.ToLower(stringFromAnyMap(payload, "verification_status", "funding_source_verification_status")),
		sourceDetails:      firstMapFromAnyMap(payload, "source_details", "sourceDetails", "payment_method_details", "method_details"),
		withdrawalMethod:   firstMapFromAnyMap(payload, "withdrawal_method", "withdrawalMethod", "withdrawal_details", "withdrawalDetails"),
	}
	if value, ok := boolFromAnyMap(payload, "supports_withdrawal", "supportsWithdrawal"); ok {
		spec.supportsWithdrawal = value
		spec.supportsWithdrawalSet = true
	}
	return spec
}

func mergeFundingSourceSpecs(base, overlay fundingSourceSpec) fundingSourceSpec {
	if overlay.sourceType != "" {
		base.sourceType = overlay.sourceType
	}
	if overlay.externalReference != "" {
		base.externalReference = overlay.externalReference
	}
	if overlay.verificationStatus != "" {
		base.verificationStatus = overlay.verificationStatus
	}
	base.sourceDetails = mergeAnyMaps(base.sourceDetails, overlay.sourceDetails)
	base.withdrawalMethod = mergeAnyMaps(base.withdrawalMethod, overlay.withdrawalMethod)
	if overlay.supportsWithdrawalSet {
		base.supportsWithdrawal = overlay.supportsWithdrawal
		base.supportsWithdrawalSet = true
	}
	return base
}

func validateWithdrawalFundingSource(source walletstore.FundingSource, walletID uuid.UUID, currency string, amount int64, providerCode string) error {
	if source.ID <= 0 {
		return walletstore.ErrMissingFundingSourceID
	}
	if source.WalletID != walletID {
		return walletstore.ErrFundingSourceNotFound
	}
	if currency != "" && source.Currency != currency {
		return walletstore.ErrCurrencyMismatch
	}
	if err := walletstore.ValidateFundingSourceReadyForWithdrawal(&source); err != nil {
		return err
	}
	if source.PSPProvider.String != providerCode {
		return walletstore.ErrMissingProviderCode
	}
	if amount > 0 && source.TotalFunded-source.TotalWithdrawn < amount {
		return walletstore.ErrFundingSourceLimitExceeded
	}
	return nil
}

func auditMetadata(values map[string]any) (json.RawMessage, error) {
	if values == nil {
		return nil, nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func regionFromRawRequest(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	region, ok := payload["region"]
	if !ok {
		return ""
	}
	if value, ok := region.(string); ok {
		return value
	}
	return ""
}

func stringFromAnyMap(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed != "" {
				return typed
			}
		case json.Number:
			return typed.String()
		case float64:
			if math.IsNaN(typed) || math.IsInf(typed, 0) {
				continue
			}
			if math.Trunc(typed) == typed {
				return strconv.FormatInt(int64(typed), 10)
			}
			return strconv.FormatFloat(typed, 'f', -1, 64)
		}
	}
	return ""
}

func boolFromAnyMap(payload map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "true", "yes", "1":
				return true, true
			case "false", "no", "0":
				return false, true
			}
		}
	}
	return false, false
}

func firstMapFromAnyMap(payload map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value := mapFromAny(payload[key]); value != nil {
			return value
		}
	}
	return nil
}

func mapFromAny(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		result := make(map[string]any, len(typed))
		for key, value := range typed {
			result[key] = value
		}
		return result
	default:
		return nil
	}
}

func mergeAnyMaps(base, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := make(map[string]any, len(base)+len(overlay))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}

func recordAuditEvent(ctx workflow.Context, event walletstore.AuditEvent) error {
	return workflow.ExecuteActivity(ctx, walletactivity.ActivityRecordAuditEvent, event).Get(ctx, nil)
}

func newLockToken(ctx workflow.Context) (string, error) {
	var token string
	err := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return uuid.NewString()
	}).Get(&token)
	return token, err
}

func missingRequiredText(value string) bool {
	return strings.TrimSpace(value) == ""
}
