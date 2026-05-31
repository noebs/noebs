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
	"go.temporal.io/sdk/workflow"
)

var (
	ErrNotImplemented                 = errors.New("workflow not implemented")
	ErrManualTransferTimedOut         = errors.New("manual transfer approval timed out")
	ErrWithdrawalApprovalTimedOut     = errors.New("withdrawal approval timed out")
	ErrWithdrawalVerificationTimedOut = errors.New("withdrawal destination verification timed out")
)

const (
	ManualTransferDecisionSignal  = "manual_transfer_decision"
	ManualTransferStatusPending   = "pending"
	ManualTransferStatusApproved  = "approved"
	ManualTransferStatusRejected  = "rejected"
	ManualTransferStatusCompleted = "completed"
	WithdrawalApprovalSignal      = "withdrawal_approval"
	WithdrawalVerificationSignal  = "withdrawal_destination_verification"
	PSPStatusUpdateSignal         = "psp_status_update"
)

type DepositParams struct {
	TenantID        string
	ProviderCode    string
	ClientReference string
	WalletID        string
	OwnerType       string
	OwnerID         string
	Region          string
}

type WithdrawalParams struct {
	TenantID                   string
	ProviderCode               string
	WalletID                   string
	OwnerType                  string
	OwnerID                    string
	UserID                     int64
	WalletPIN                  string
	RequirePIN                 bool
	TwoFACode                  string
	Require2FA                 bool
	DestinationID              int64
	AllowReturnToSource        bool
	ApprovalRequired           bool
	ApprovalTimeoutSeconds     int
	VerificationTimeoutSeconds int
	HoldExpirySeconds          int
	Region                     string
	Request                    walletpsp.PayoutRequest
}

type P2PParams struct {
	TenantID       string
	IdempotencyKey string
	Currency       string
	FromWalletID   string
	ToWalletID     string
	Amount         int64
	UserID         int64
	WalletPIN      string
	RequirePIN     bool
	TwoFACode      string
	Require2FA     bool
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

type WithdrawalApprovalDecision struct {
	Approved       bool
	ApproverID     int64
	Reason         string
	ProofOfPayment string
}

type DestinationVerificationDecision struct {
	VerificationID int64
	Verified       bool
	Reason         string
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
	walletID, err := uuid.Parse(params.WalletID)
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})
	info := workflow.GetInfo(ctx)
	workflowID := ""
	if info != nil {
		workflowID = info.WorkflowExecution.ID
	}

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
		Region:          params.Region,
	}
	var validation walletvalidation.DepositValidationResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDeposit, validationReq).Get(ctx, &validation); err != nil {
		return err
	}

	statusResult := statusFromPSPTransaction(pspTxn)
	if transactionID != "" {
		verifyParams := walletactivity.VerifyDepositParams{
			TenantID:      params.TenantID,
			ProviderCode:  providerCode,
			TransactionID: transactionID,
			Currency:      pspTxn.Currency,
			Region:        params.Region,
		}
		var result walletpsp.DepositVerification
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityVerifyDeposit, verifyParams).Get(ctx, &result); err != nil {
			return err
		}
		statusResult = statusFromDepositVerification(result)
		if err := updatePSPTransactionFromStatus(ctx, params.TenantID, params.ClientReference, statusResult); err != nil {
			return err
		}
	}
	if !isTerminalPSPStatus(statusResult.Status) {
		latest, err := loadPSPTransaction(ctx, params.TenantID, params.ClientReference)
		if err != nil {
			return err
		}
		mergePSPStatus(&statusResult, statusFromPSPTransaction(latest))
	}
	finalStatus, err := awaitTerminalPSPStatus(ctx, statusResult)
	if err != nil {
		return err
	}
	if finalStatus.Status != statusResult.Status || finalStatus.ProviderTxID != statusResult.ProviderTxID || finalStatus.RawResponse != nil {
		if err := updatePSPTransactionFromStatus(ctx, params.TenantID, params.ClientReference, finalStatus); err != nil {
			return err
		}
	}
	if finalStatus.Status != "success" {
		return nil
	}

	resolveReq := walletvalidation.PSPAmountResolutionRequest{
		TenantID:           params.TenantID,
		RequestedAmount:    pspTxn.Amount,
		RequestedCurrency:  pspTxn.Currency,
		SettlementAmount:   finalStatus.Amount,
		SettlementCurrency: finalStatus.Currency,
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
			Amount:     finalStatus.Amount,
			Currency:   finalStatus.Currency,
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

	var treasury walletstore.Wallet
	treasuryParams := walletactivity.EnsureSystemWalletParams{
		TenantID:   params.TenantID,
		Currency:   resolved.WalletCurrency,
		WalletCode: walletstore.SystemTreasury,
		KYCTier:    walletstore.KYCTierUnverified,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, treasuryParams).Get(ctx, &treasury); err != nil {
		return err
	}

	depositEntry := walletstore.DoubleEntryParams{
		TenantID:       params.TenantID,
		IdempotencyKey: params.ClientReference + ":deposit",
		Currency:       resolved.WalletCurrency,
		ReferenceType:  "deposit",
		ReferenceID:    params.ClientReference,
		DebitWalletID:  treasury.ID,
		CreditWalletID: walletID,
		Amount:         resolved.WalletCreditAmount,
		Description:    "deposit",
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateSystemDebitDoubleEntry, depositEntry).Get(ctx, nil); err != nil {
		return err
	}
	var posted walletstore.DoubleEntryResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteSystemDebitDoubleEntry, depositEntry).Get(ctx, &posted); err != nil {
		return err
	}

	now := workflow.Now(ctx)
	externalRef := pspTxn.PSPTransactionID
	if finalStatus.ProviderTxID != "" {
		externalRef = sql.NullString{String: finalStatus.ProviderTxID, Valid: true}
	}
	if !externalRef.Valid {
		externalRef = sql.NullString{String: params.ClientReference, Valid: true}
	}
	source, err := depositFundingSource(pspTxn, walletID, resolved.WalletCurrency, externalRef, validation.SupportsWithdrawal, now, finalStatus.RawResponse)
	if err != nil {
		return err
	}
	var storedSource walletstore.FundingSource
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityRecordFundingSource, source).Get(ctx, &storedSource); err != nil {
		return err
	}

	link := walletstore.LedgerFundingLink{
		TenantID:        params.TenantID,
		LedgerEntryID:   posted.CreditEntry.ID,
		FundingSourceID: storedSource.ID,
		Amount:          resolved.WalletCreditAmount,
		Currency:        resolved.WalletCurrency,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityLinkLedgerToFundingSource, link).Get(ctx, nil); err != nil {
		return err
	}

	feeAmount := int64(0)
	if validation.Fee != nil {
		feeAmount = validation.Fee.TotalFee
	}
	if feeAmount > 0 {
		var feesWallet walletstore.Wallet
		feesParams := walletactivity.EnsureSystemWalletParams{
			TenantID:   params.TenantID,
			Currency:   resolved.WalletCurrency,
			WalletCode: walletstore.SystemFees,
			KYCTier:    walletstore.KYCTierUnverified,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, feesParams).Get(ctx, &feesWallet); err != nil {
			return err
		}
		feeEntry := walletstore.DoubleEntryParams{
			TenantID:       params.TenantID,
			IdempotencyKey: params.ClientReference + ":deposit_fee",
			Currency:       resolved.WalletCurrency,
			ReferenceType:  "fee",
			ReferenceID:    params.ClientReference,
			DebitWalletID:  walletID,
			CreditWalletID: feesWallet.ID,
			Amount:         feeAmount,
			Description:    "deposit fee",
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDoubleEntry, feeEntry).Get(ctx, nil); err != nil {
			return err
		}
		var feePosted walletstore.DoubleEntryResult
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteDoubleEntry, feeEntry).Get(ctx, &feePosted); err != nil {
			return err
		}
		_ = feePosted
	}
	metadata, err := auditMetadata(map[string]any{
		"client_reference":     params.ClientReference,
		"provider_code":        providerCode,
		"psp_transaction_id":   finalStatus.ProviderTxID,
		"requested_amount":     pspTxn.Amount,
		"requested_currency":   pspTxn.Currency,
		"wallet_credit_amount": resolved.WalletCreditAmount,
		"wallet_currency":      resolved.WalletCurrency,
		"fee_amount":           feeAmount,
		"funding_source_id":    storedSource.ID,
		"ledger_transaction":   posted.TransactionID,
	})
	if err != nil {
		return err
	}
	event := walletstore.AuditEvent{
		TenantID:   params.TenantID,
		EventType:  "wallet.deposit",
		ActorType:  params.OwnerType,
		ActorID:    params.OwnerID,
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   sql.NullString{String: walletID.String(), Valid: true},
		Action:     "completed",
		Metadata:   metadata,
		WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		RequestID:  sql.NullString{String: params.ClientReference, Valid: params.ClientReference != ""},
	}
	if err := recordAuditEvent(ctx, event); err != nil {
		return err
	}
	return nil
}

func Withdrawal(ctx workflow.Context, params WithdrawalParams) error {
	if params.TenantID == "" {
		return walletstore.ErrMissingTenantID
	}
	if params.WalletID == "" {
		return walletstore.ErrMissingWalletID
	}
	if params.OwnerType == "" {
		return walletstore.ErrMissingOwnerType
	}
	if params.OwnerID == "" {
		return walletstore.ErrMissingOwnerID
	}
	if params.Request.ClientReference == "" {
		return walletstore.ErrMissingClientReference
	}
	if params.Request.Currency == "" {
		return walletstore.ErrMissingCurrency
	}
	if params.Request.Amount <= 0 {
		return walletstore.ErrInvalidAmount
	}
	if params.RequirePIN && params.WalletPIN == "" {
		return walletstore.ErrMissingWalletPIN
	}
	if params.Require2FA {
		if params.UserID <= 0 {
			return walletstore.ErrInvalidUserID
		}
		if params.TwoFACode == "" {
			return walletstore.ErrMissingTwoFACode
		}
	}
	if params.HoldExpirySeconds <= 0 {
		return walletstore.ErrMissingHoldExpiry
	}
	if params.ApprovalRequired && params.ApprovalTimeoutSeconds <= 0 {
		return walletstore.ErrMissingApprovalTimeout
	}
	if params.DestinationID <= 0 && !params.AllowReturnToSource {
		return walletstore.ErrMissingDestinationID
	}

	walletID, err := uuid.Parse(params.WalletID)
	if err != nil {
		return err
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	info := workflow.GetInfo(ctx)
	workflowID := ""
	if info != nil {
		workflowID = info.WorkflowExecution.ID
	}

	pspTxn, err := loadPSPTransaction(ctx, params.TenantID, params.Request.ClientReference)
	if err != nil {
		return err
	}

	providerCode := params.ProviderCode
	if providerCode == "" {
		providerCode = pspTxn.PSPProvider
	}

	if params.RequirePIN {
		var ok bool
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityVerifyWalletPIN, params.TenantID, walletID, params.WalletPIN).Get(ctx, &ok); err != nil {
			return err
		}
		if !ok {
			return walletstore.ErrInvalidWalletPIN
		}
	}
	if params.Require2FA {
		var ok bool
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityVerifyUserTOTP, params.TenantID, params.UserID, params.TwoFACode).Get(ctx, &ok); err != nil {
			return err
		}
		if !ok {
			return walletstore.ErrInvalidTwoFACode
		}
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
		Region:          params.Region,
	}
	var validation walletvalidation.WithdrawalValidationResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateWithdrawal, validationReq).Get(ctx, &validation); err != nil {
		return err
	}

	var destination walletstore.WithdrawalDestination
	var destinationDetails map[string]any
	var destinationID int64
	var fundingSource *walletstore.FundingSource

	if params.AllowReturnToSource {
		var sources []walletstore.FundingSource
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityGetReturnToSourceOptions, params.TenantID, walletID).Get(ctx, &sources); err != nil {
			return err
		}
		selected, details, err := selectReturnToSource(sources, walletID, params.Request.Currency, params.Request.Amount, providerCode)
		if err != nil {
			return err
		}
		if selected != nil {
			fundingSource = selected
			destinationDetails = details
		}
	}

	if fundingSource == nil {
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
		if destination.Currency != params.Request.Currency {
			return walletstore.ErrCurrencyMismatch
		}
		if destination.IsReturnToSource && !destination.LinkedFundingSourceID.Valid {
			return walletstore.ErrMissingFundingSourceID
		}
		if destination.LinkedFundingSourceID.Valid {
			var linkedSource walletstore.FundingSource
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityResolveFundingSource, params.TenantID, destination.LinkedFundingSourceID.Int64).Get(ctx, &linkedSource); err != nil {
				return err
			}
			if err := validateWithdrawalFundingSource(linkedSource, walletID, params.Request.Currency, params.Request.Amount, providerCode); err != nil {
				return err
			}
			fundingSource = &linkedSource
		}
		if err := walletstore.ValidateWithdrawalDestinationReadyForWithdrawal(&destination); err != nil {
			if !errors.Is(err, walletstore.ErrDestinationNotVerified) {
				return err
			}
			if !destination.OwnershipVerificationMethod.Valid {
				return walletstore.ErrMissingVerificationType
			}
			if params.VerificationTimeoutSeconds <= 0 {
				return walletstore.ErrMissingVerificationTimeout
			}
			now := workflow.Now(ctx)
			verification := walletstore.OwnershipVerification{
				TenantID:         params.TenantID,
				DestinationID:    destination.ID,
				VerificationType: destination.OwnershipVerificationMethod.String,
				Status:           "pending",
				ExpiresAt:        now.Add(time.Duration(params.VerificationTimeoutSeconds) * time.Second),
				WorkflowID:       sql.NullString{String: workflowID, Valid: workflowID != ""},
				ReferenceID:      sql.NullString{String: params.Request.ClientReference, Valid: true},
			}
			var stored walletstore.OwnershipVerification
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityInitiateOwnershipVerification, verification).Get(ctx, &stored); err != nil {
				return err
			}
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateDestinationOwnership, params.TenantID, destination.ID, "pending", sql.NullTime{}, now).Get(ctx, nil); err != nil {
				return err
			}
			decision, err := awaitDestinationVerificationDecision(ctx, stored.ID, params.VerificationTimeoutSeconds)
			if err != nil {
				now = workflow.Now(ctx)
				updateErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateOwnershipVerificationStatus, params.TenantID, stored.ID, "expired", now).Get(ctx, nil)
				return errors.Join(err, updateErr)
			}
			now = workflow.Now(ctx)
			if decision.Verified {
				if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateOwnershipVerificationStatus, params.TenantID, stored.ID, "verified", now).Get(ctx, nil); err != nil {
					return err
				}
				if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateDestinationOwnership, params.TenantID, destination.ID, "verified", sql.NullTime{Time: now, Valid: true}, now).Get(ctx, nil); err != nil {
					return err
				}
				destination.OwnershipStatus = "verified"
				destination.OwnershipVerifiedAt = sql.NullTime{Time: now, Valid: true}
			} else {
				if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateOwnershipVerificationStatus, params.TenantID, stored.ID, "failed", now).Get(ctx, nil); err != nil {
					return err
				}
				if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateDestinationOwnership, params.TenantID, destination.ID, "rejected", sql.NullTime{}, now).Get(ctx, nil); err != nil {
					return err
				}
				rejectMeta, err := auditMetadata(map[string]any{
					"client_reference": params.Request.ClientReference,
					"destination_id":   destination.ID,
					"reason":           decision.Reason,
				})
				if err != nil {
					return err
				}
				rejectEvent := walletstore.AuditEvent{
					TenantID:   params.TenantID,
					EventType:  "wallet.withdrawal",
					ActorType:  "workflow",
					ActorID:    workflowID,
					TargetType: sql.NullString{String: "destination", Valid: true},
					TargetID:   sql.NullString{String: fmt.Sprintf("%d", destination.ID), Valid: true},
					Action:     "destination_rejected",
					Metadata:   rejectMeta,
					WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
					RequestID:  sql.NullString{String: params.Request.ClientReference, Valid: params.Request.ClientReference != ""},
				}
				if err := recordAuditEvent(ctx, rejectEvent); err != nil {
					return err
				}
				return walletstore.ErrDestinationNotVerified
			}
		}
		if len(destination.DestinationDetails) == 0 {
			return walletstore.ErrMissingDestinationDetails
		}
		if err := json.Unmarshal(destination.DestinationDetails, &destinationDetails); err != nil {
			return err
		}
		destinationID = destination.ID
		if destination.PSPProvider.Valid {
			if providerCode == "" {
				providerCode = destination.PSPProvider.String
			} else if providerCode != destination.PSPProvider.String {
				return walletstore.ErrMissingProviderCode
			}
		}
	} else {
		if fundingSource.PSPProvider.Valid {
			if providerCode == "" {
				providerCode = fundingSource.PSPProvider.String
			} else if providerCode != fundingSource.PSPProvider.String {
				return walletstore.ErrMissingProviderCode
			}
		} else if providerCode == "" {
			return walletstore.ErrMissingProviderCode
		}
	}

	if providerCode == "" {
		return walletstore.ErrMissingProviderCode
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
		return err
	}
	var hold walletstore.BalanceHold
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityCreateHold, holdParams).Get(ctx, &hold); err != nil {
		return err
	}
	holdID := hold.ID

	releaseHold := func(cause error) error {
		return releaseHoldAndReturn(ctx, params.TenantID, holdID, cause)
	}

	if params.ApprovalRequired {
		decision, err := awaitWithdrawalApproval(ctx, params)
		if err != nil {
			return releaseHold(err)
		}
		if !decision.Approved {
			if decision.Reason == "" {
				return releaseHold(walletstore.ErrMissingApprovalReason)
			}
			rejectMeta, err := auditMetadata(map[string]any{
				"client_reference": params.Request.ClientReference,
				"amount":           params.Request.Amount,
				"currency":         params.Request.Currency,
				"approver_id":      decision.ApproverID,
				"reason":           decision.Reason,
			})
			if err != nil {
				return releaseHold(err)
			}
			rejectEvent := walletstore.AuditEvent{
				TenantID:   params.TenantID,
				EventType:  "wallet.withdrawal",
				ActorType:  "admin",
				ActorID:    fmt.Sprintf("%d", decision.ApproverID),
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
		if decision.ProofOfPayment == "" {
			return releaseHold(walletstore.ErrMissingProofOfPayment)
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
	if fundingSource != nil {
		payout.Metadata["funding_source_id"] = fundingSource.ID
	}

	payoutParams := walletactivity.SendPayoutParams{
		TenantID:     params.TenantID,
		ProviderCode: providerCode,
		Request:      payout,
		Region:       params.Region,
	}
	var result walletpsp.PayoutResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivitySendPayout, payoutParams).Get(ctx, &result); err != nil {
		failMeta, metaErr := auditMetadata(map[string]any{
			"client_reference": params.Request.ClientReference,
			"provider_code":    providerCode,
			"amount":           params.Request.Amount,
			"currency":         params.Request.Currency,
			"error":            err.Error(),
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
		return releaseHold(err)
	}
	statusResult := statusFromPayoutResult(result)
	if err := updatePSPTransactionFromStatus(ctx, params.TenantID, params.Request.ClientReference, statusResult); err != nil {
		return releaseHold(err)
	}
	if !isTerminalPSPStatus(statusResult.Status) {
		latest, err := loadPSPTransaction(ctx, params.TenantID, params.Request.ClientReference)
		if err != nil {
			return releaseHold(err)
		}
		mergePSPStatus(&statusResult, statusFromPSPTransaction(latest))
	}
	finalStatus, err := awaitTerminalPSPStatus(ctx, statusResult)
	if err != nil {
		return releaseHold(err)
	}
	if finalStatus.Status != statusResult.Status || finalStatus.ProviderTxID != statusResult.ProviderTxID || finalStatus.RawResponse != nil {
		if err := updatePSPTransactionFromStatus(ctx, params.TenantID, params.Request.ClientReference, finalStatus); err != nil {
			return releaseHold(err)
		}
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
		return releaseHold(err)
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

	var treasury walletstore.Wallet
	treasuryParams := walletactivity.EnsureSystemWalletParams{
		TenantID:   params.TenantID,
		Currency:   validation.Currency,
		WalletCode: walletstore.SystemTreasury,
		KYCTier:    walletstore.KYCTierUnverified,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, treasuryParams).Get(ctx, &treasury); err != nil {
		return releaseHold(err)
	}

	withdrawEntry := walletstore.DoubleEntryParams{
		TenantID:       params.TenantID,
		IdempotencyKey: params.Request.ClientReference + ":withdrawal",
		Currency:       validation.Currency,
		ReferenceType:  "withdrawal",
		ReferenceID:    params.Request.ClientReference,
		DebitWalletID:  walletID,
		CreditWalletID: treasury.ID,
		Amount:         validation.WalletDebitAmount,
		Description:    "withdrawal",
	}
	heldWithdrawEntry := walletstore.HeldDoubleEntryParams{HoldID: holdID, Entry: withdrawEntry}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateHeldDoubleEntry, heldWithdrawEntry).Get(ctx, nil); err != nil {
		return releaseHold(err)
	}
	var posted walletstore.DoubleEntryResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteHeldDoubleEntry, heldWithdrawEntry).Get(ctx, &posted); err != nil {
		return releaseHold(err)
	}

	feeAmount := int64(0)
	if validation.Fee != nil {
		feeAmount = validation.Fee.TotalFee
	}
	if feeAmount > 0 {
		var feesWallet walletstore.Wallet
		feesParams := walletactivity.EnsureSystemWalletParams{
			TenantID:   params.TenantID,
			Currency:   validation.Currency,
			WalletCode: walletstore.SystemFees,
			KYCTier:    walletstore.KYCTierUnverified,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, feesParams).Get(ctx, &feesWallet); err != nil {
			return releaseHold(err)
		}
		feeEntry := walletstore.DoubleEntryParams{
			TenantID:       params.TenantID,
			IdempotencyKey: params.Request.ClientReference + ":withdrawal_fee",
			Currency:       validation.Currency,
			ReferenceType:  "fee",
			ReferenceID:    params.Request.ClientReference,
			DebitWalletID:  walletID,
			CreditWalletID: feesWallet.ID,
			Amount:         feeAmount,
			Description:    "withdrawal fee",
		}
		heldFeeEntry := walletstore.HeldDoubleEntryParams{HoldID: holdID, Entry: feeEntry}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateHeldDoubleEntry, heldFeeEntry).Get(ctx, nil); err != nil {
			return releaseHold(err)
		}
		var feePosted walletstore.DoubleEntryResult
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteHeldDoubleEntry, heldFeeEntry).Get(ctx, &feePosted); err != nil {
			return releaseHold(err)
		}
		_ = feePosted
	}

	if destinationID > 0 {
		link := walletstore.LedgerWithdrawalDestinationLink{
			TenantID:      params.TenantID,
			LedgerEntryID: posted.DebitEntry.ID,
			DestinationID: destinationID,
			Amount:        params.Request.Amount,
			Currency:      params.Request.Currency,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityLinkLedgerToWithdrawalDestination, link).Get(ctx, nil); err != nil {
			return err
		}
	}
	if fundingSource != nil {
		link := walletstore.LedgerFundingLink{
			TenantID:        params.TenantID,
			LedgerEntryID:   posted.DebitEntry.ID,
			FundingSourceID: fundingSource.ID,
			Amount:          validation.WalletDebitAmount,
			Currency:        validation.WalletCurrency,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityLinkLedgerToFundingSource, link).Get(ctx, nil); err != nil {
			return err
		}
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
		"funding_source_id":   fundingSourceID(fundingSource),
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

func P2P(ctx workflow.Context, params P2PParams) error {
	fromID, err := uuid.Parse(params.FromWalletID)
	if err != nil {
		return err
	}
	toID, err := uuid.Parse(params.ToWalletID)
	if err != nil {
		return err
	}
	info := workflow.GetInfo(ctx)
	workflowID := ""
	if info != nil {
		workflowID = info.WorkflowExecution.ID
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	if params.RequirePIN {
		var ok bool
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityVerifyWalletPIN, params.TenantID, fromID, params.WalletPIN).Get(ctx, &ok); err != nil {
			return err
		}
		if !ok {
			return walletstore.ErrInvalidWalletPIN
		}
	}
	if params.Require2FA {
		var ok bool
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityVerifyUserTOTP, params.TenantID, params.UserID, params.TwoFACode).Get(ctx, &ok); err != nil {
			return err
		}
		if !ok {
			return walletstore.ErrInvalidTwoFACode
		}
	}

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
	_ = result

	feeAmount := int64(0)
	if validation.Fee != nil {
		feeAmount = validation.Fee.TotalFee
	}
	if feeAmount > 0 {
		var feesWallet walletstore.Wallet
		feesParams := walletactivity.EnsureSystemWalletParams{
			TenantID:   params.TenantID,
			Currency:   params.Currency,
			WalletCode: walletstore.SystemFees,
			KYCTier:    walletstore.KYCTierUnverified,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, feesParams).Get(ctx, &feesWallet); err != nil {
			return err
		}
		feeEntry := walletstore.DoubleEntryParams{
			TenantID:       params.TenantID,
			IdempotencyKey: params.IdempotencyKey + ":fee",
			Currency:       params.Currency,
			ReferenceType:  "fee",
			ReferenceID:    params.ReferenceID,
			DebitWalletID:  fromID,
			CreditWalletID: feesWallet.ID,
			Amount:         feeAmount,
			Description:    "p2p fee",
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDoubleEntry, feeEntry).Get(ctx, nil); err != nil {
			return err
		}
		var feePosted walletstore.DoubleEntryResult
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteDoubleEntry, feeEntry).Get(ctx, &feePosted); err != nil {
			return err
		}
		_ = feePosted
	}
	meta, err := auditMetadata(map[string]any{
		"reference_id":       params.ReferenceID,
		"amount":             params.Amount,
		"currency":           params.Currency,
		"fee_amount":         feeAmount,
		"ledger_transaction": result.TransactionID,
	})
	if err != nil {
		return err
	}
	debitEvent := walletstore.AuditEvent{
		TenantID:   params.TenantID,
		EventType:  "wallet.p2p",
		ActorType:  params.FromOwnerType,
		ActorID:    params.FromOwnerID,
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   sql.NullString{String: fromID.String(), Valid: true},
		Action:     "debit",
		Metadata:   meta,
		WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		RequestID:  sql.NullString{String: params.ReferenceID, Valid: params.ReferenceID != ""},
	}
	if err := recordAuditEvent(ctx, debitEvent); err != nil {
		return err
	}
	creditEvent := walletstore.AuditEvent{
		TenantID:   params.TenantID,
		EventType:  "wallet.p2p",
		ActorType:  params.ToOwnerType,
		ActorID:    params.ToOwnerID,
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   sql.NullString{String: toID.String(), Valid: true},
		Action:     "credit",
		Metadata:   meta,
		WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		RequestID:  sql.NullString{String: params.ReferenceID, Valid: params.ReferenceID != ""},
	}
	if err := recordAuditEvent(ctx, creditEvent); err != nil {
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
	if err := walletstore.ValidateManualTransferType(params.TransferType); err != nil {
		return err
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
		return walletstore.ErrMissingRequesterID
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
	requestMeta, err := auditMetadata(map[string]any{
		"transfer_type": params.TransferType,
		"amount":        params.Amount,
		"currency":      params.Currency,
		"reason":        params.Reason,
	})
	if err != nil {
		return err
	}
	requestEvent := walletstore.AuditEvent{
		TenantID:   params.TenantID,
		EventType:  "wallet.manual_transfer",
		ActorType:  "admin",
		ActorID:    fmt.Sprintf("%d", params.RequestedBy),
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   sql.NullString{String: params.WalletID, Valid: params.WalletID != ""},
		Action:     "requested",
		Metadata:   requestMeta,
		WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		RequestID:  sql.NullString{String: params.IdempotencyKey, Valid: params.IdempotencyKey != ""},
	}
	if err := recordAuditEvent(ctx, requestEvent); err != nil {
		return err
	}

	var holdID int64
	if walletstore.IsManualTransferDebit(params.TransferType) {
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
		update := walletstore.ManualTransferStatusUpdate{
			Status:          ManualTransferStatusRejected,
			RejectionReason: sql.NullString{String: err.Error(), Valid: true},
		}
		releaseErr := releaseBalanceHold(ctx, params.TenantID, holdID)
		updateErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, params.TenantID, workflowID, update).Get(ctx, nil)
		return errors.Join(err, releaseErr, updateErr)
	}
	if err := validateManualTransferDecision(params.RequestedBy, decision); err != nil {
		update := walletstore.ManualTransferStatusUpdate{
			Status:          ManualTransferStatusRejected,
			RejectionReason: sql.NullString{String: err.Error(), Valid: true},
		}
		releaseErr := releaseBalanceHold(ctx, params.TenantID, holdID)
		updateErr := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, params.TenantID, workflowID, update).Get(ctx, nil)
		return errors.Join(err, releaseErr, updateErr)
	}

	now := workflow.Now(ctx)
	if decision.Approved {
		if decision.ProofOfPayment == "" {
			return releaseHoldAndReturn(ctx, params.TenantID, holdID, walletstore.ErrMissingProofOfPayment)
		}
		approval := walletstore.ManualTransferApproval{
			TenantID:         params.TenantID,
			ManualTransferID: stored.ID,
			ApproverID:       decision.ApproverID,
			Decision:         ManualTransferStatusApproved,
			Reason:           sql.NullString{String: decision.Reason, Valid: decision.Reason != ""},
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityAddManualTransferApproval, approval).Get(ctx, nil); err != nil {
			return releaseHoldAndReturn(ctx, params.TenantID, holdID, err)
		}
		update := walletstore.ManualTransferStatusUpdate{
			Status:         ManualTransferStatusApproved,
			ApprovedBy:     sql.NullInt64{Int64: decision.ApproverID, Valid: true},
			ApprovedAt:     sql.NullTime{Time: now, Valid: true},
			ProofOfPayment: sql.NullString{String: decision.ProofOfPayment, Valid: true},
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, params.TenantID, workflowID, update).Get(ctx, nil); err != nil {
			return releaseHoldAndReturn(ctx, params.TenantID, holdID, err)
		}
		var treasury walletstore.Wallet
		treasuryParams := walletactivity.EnsureSystemWalletParams{
			TenantID:   params.TenantID,
			Currency:   params.Currency,
			WalletCode: walletstore.SystemTreasury,
			KYCTier:    walletstore.KYCTierUnverified,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, treasuryParams).Get(ctx, &treasury); err != nil {
			return releaseHoldAndReturn(ctx, params.TenantID, holdID, err)
		}

		debitID := walletID
		creditID := treasury.ID
		if params.TransferType == walletstore.ManualTransferTypeCredit {
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
		var posted walletstore.DoubleEntryResult
		if holdID > 0 {
			heldEntry := walletstore.HeldDoubleEntryParams{HoldID: holdID, Entry: entry}
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateHeldDoubleEntry, heldEntry).Get(ctx, nil); err != nil {
				return releaseHoldAndReturn(ctx, params.TenantID, holdID, err)
			}
			if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteHeldDoubleEntry, heldEntry).Get(ctx, &posted); err != nil {
				return releaseHoldAndReturn(ctx, params.TenantID, holdID, err)
			}
		} else if params.TransferType == walletstore.ManualTransferTypeCredit {
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
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateManualTransferStatus, params.TenantID, workflowID, complete).Get(ctx, nil); err != nil {
			return err
		}
		completeMeta, err := auditMetadata(map[string]any{
			"transfer_type": params.TransferType,
			"amount":        params.Amount,
			"currency":      params.Currency,
			"approver_id":   decision.ApproverID,
		})
		if err != nil {
			return err
		}
		completeEvent := walletstore.AuditEvent{
			TenantID:   params.TenantID,
			EventType:  "wallet.manual_transfer",
			ActorType:  "admin",
			ActorID:    fmt.Sprintf("%d", decision.ApproverID),
			TargetType: sql.NullString{String: "wallet", Valid: true},
			TargetID:   sql.NullString{String: params.WalletID, Valid: params.WalletID != ""},
			Action:     "completed",
			Metadata:   completeMeta,
			WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
			RequestID:  sql.NullString{String: params.IdempotencyKey, Valid: params.IdempotencyKey != ""},
		}
		return recordAuditEvent(ctx, completeEvent)
	}

	if decision.Reason == "" {
		return releaseHoldAndReturn(ctx, params.TenantID, holdID, walletstore.ErrMissingReason)
	}
	rejection := walletstore.ManualTransferApproval{
		TenantID:         params.TenantID,
		ManualTransferID: stored.ID,
		ApproverID:       decision.ApproverID,
		Decision:         ManualTransferStatusRejected,
		Reason:           sql.NullString{String: decision.Reason, Valid: true},
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityAddManualTransferApproval, rejection).Get(ctx, nil); err != nil {
		return releaseHoldAndReturn(ctx, params.TenantID, holdID, err)
	}
	rejectMeta, err := auditMetadata(map[string]any{
		"transfer_type": params.TransferType,
		"amount":        params.Amount,
		"currency":      params.Currency,
		"approver_id":   decision.ApproverID,
		"reason":        decision.Reason,
	})
	if err != nil {
		return releaseHoldAndReturn(ctx, params.TenantID, holdID, err)
	}
	rejectEvent := walletstore.AuditEvent{
		TenantID:   params.TenantID,
		EventType:  "wallet.manual_transfer",
		ActorType:  "admin",
		ActorID:    fmt.Sprintf("%d", decision.ApproverID),
		TargetType: sql.NullString{String: "wallet", Valid: true},
		TargetID:   sql.NullString{String: params.WalletID, Valid: params.WalletID != ""},
		Action:     "rejected",
		Metadata:   rejectMeta,
		WorkflowID: sql.NullString{String: workflowID, Valid: workflowID != ""},
		RequestID:  sql.NullString{String: params.IdempotencyKey, Valid: params.IdempotencyKey != ""},
	}
	if err := recordAuditEvent(ctx, rejectEvent); err != nil {
		return releaseHoldAndReturn(ctx, params.TenantID, holdID, err)
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
	if params.Limit <= 0 {
		return walletstore.ErrInvalidLimit
	}
	startTime := params.StartTime
	endTime := params.EndTime
	if startTime.IsZero() || endTime.IsZero() {
		lookback := params.LookbackHours
		if lookback <= 0 {
			lookback = 24
		}
		endTime = workflow.Now(ctx)
		startTime = endTime.Add(time.Duration(-lookback) * time.Hour)
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
	if params.TenantID == "" {
		return walletstore.ErrMissingTenantID
	}
	if params.Limit <= 0 {
		return walletstore.ErrInvalidLimit
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})
	confirmedAtVersion := workflow.GetVersion(ctx, "psp_status_poller_confirmed_at", workflow.DefaultVersion, 1)
	logger := workflow.GetLogger(ctx)

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
		direction := "deposit"
		if txn.Direction == "outbound" {
			direction = "withdrawal"
		}
		statusParams := walletactivity.GetStatusParams{
			TenantID:      params.TenantID,
			ProviderCode:  txn.PSPProvider,
			TransactionID: txn.PSPTransactionID.String,
			Currency:      txn.Currency,
			Direction:     direction,
			Region:        regionFromRawRequest(txn.RawRequest),
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
		if confirmedAtVersion == 1 && update.Status == "success" && !txn.ConfirmedAt.Valid {
			update.ConfirmedAt = sql.NullTime{Time: now, Valid: true}
		}
		updateParams := walletactivity.UpdatePSPTransactionStatusParams{
			TenantID:        params.TenantID,
			ClientReference: txn.ClientReference,
			Update:          update,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdatePSPTransactionStatus, updateParams).Get(ctx, nil); err != nil {
			return err
		}
		if pollErr == nil && status.Status != "" && txn.WorkflowID.Valid {
			signalFuture := workflow.SignalExternalWorkflow(ctx, txn.WorkflowID.String, "", PSPStatusUpdateSignal, status)
			if err := signalFuture.Get(ctx, nil); err != nil {
				logger.Warn("failed to signal psp status update", "workflow_id", txn.WorkflowID.String, "client_reference", txn.ClientReference, "error", err)
			}
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

func statusFromDepositVerification(result walletpsp.DepositVerification) walletpsp.TxStatus {
	return walletpsp.TxStatus{
		ProviderTxID: result.ProviderTxID,
		Amount:       result.Amount,
		Currency:     result.Currency,
		Status:       normalizePSPStatus(result.Status),
		RawResponse:  result.Metadata,
	}
}

func statusFromPayoutResult(result walletpsp.PayoutResult) walletpsp.TxStatus {
	return walletpsp.TxStatus{
		ProviderTxID: result.ProviderTxID,
		Status:       normalizePSPStatus(result.Status),
		RawResponse:  result.RawResponse,
	}
}

func statusFromPSPTransaction(txn *walletstore.PSPTransaction) walletpsp.TxStatus {
	if txn == nil {
		return walletpsp.TxStatus{}
	}
	status := walletpsp.TxStatus{Status: normalizePSPStatus(txn.Status)}
	if txn.PSPTransactionID.Valid {
		status.ProviderTxID = txn.PSPTransactionID.String
	}
	if len(txn.RawResponse) == 0 {
		return status
	}
	var payload map[string]any
	if err := json.Unmarshal(txn.RawResponse, &payload); err != nil {
		return status
	}
	status.RawResponse = payload
	update := walletpsp.TxStatus{
		ProviderTxID: stringFromAnyMap(payload, "psp_transaction_id", "transaction_id", "id"),
		Amount:       int64FromAnyMap(payload, "amount"),
		Currency:     stringFromAnyMap(payload, "currency"),
		Status:       normalizePSPStatus(stringFromAnyMap(payload, "status", "state")),
		RawResponse:  payload,
	}
	mergePSPStatus(&status, update)
	return status
}

func awaitTerminalPSPStatus(ctx workflow.Context, initial walletpsp.TxStatus) (walletpsp.TxStatus, error) {
	current := initial
	current.Status = normalizePSPStatus(current.Status)
	if isTerminalPSPStatus(current.Status) {
		return current, nil
	}
	statusCh := workflow.GetSignalChannel(ctx, PSPStatusUpdateSignal)
	for {
		var update walletpsp.TxStatus
		statusCh.Receive(ctx, &update)
		mergePSPStatus(&current, update)
		if isTerminalPSPStatus(current.Status) {
			return current, nil
		}
	}
}

func updatePSPTransactionFromStatus(ctx workflow.Context, tenantID, clientReference string, status walletpsp.TxStatus) error {
	update := walletstore.PSPStatusUpdate{Status: normalizePSPStatus(status.Status)}
	if update.Status == "" {
		return walletstore.ErrMissingStatus
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
			return err
		}
		update.RawResponse = walletstore.RawJSON(raw)
	}
	params := walletactivity.UpdatePSPTransactionStatusParams{
		TenantID:        tenantID,
		ClientReference: clientReference,
		Update:          update,
	}
	return workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdatePSPTransactionStatus, params).Get(ctx, nil)
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

func awaitManualTransferDecision(ctx workflow.Context, params ManualTransferParams) (ManualTransferDecision, error) {
	timeout := time.Duration(params.ApprovalTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 24 * time.Hour
	}
	decisionCh := workflow.GetSignalChannel(ctx, ManualTransferDecisionSignal)
	timer := workflow.NewTimer(ctx, timeout)

	for {
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
			continue
		}
		if err := validateManualTransferDecision(params.RequestedBy, decision); err != nil {
			continue
		}
		return decision, nil
	}
}

func validateManualTransferDecision(requestedBy int64, decision ManualTransferDecision) error {
	if requestedBy > 0 && decision.ApproverID == requestedBy {
		return walletstore.ErrApproverIsRequester
	}
	return nil
}

func awaitWithdrawalApproval(ctx workflow.Context, params WithdrawalParams) (WithdrawalApprovalDecision, error) {
	timeout := time.Duration(params.ApprovalTimeoutSeconds) * time.Second
	if timeout <= 0 {
		return WithdrawalApprovalDecision{}, walletstore.ErrMissingApprovalTimeout
	}
	decisionCh := workflow.GetSignalChannel(ctx, WithdrawalApprovalSignal)
	timer := workflow.NewTimer(ctx, timeout)

	var decision WithdrawalApprovalDecision
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
		return WithdrawalApprovalDecision{}, ErrWithdrawalApprovalTimedOut
	}
	if decision.ApproverID <= 0 {
		return WithdrawalApprovalDecision{}, walletstore.ErrMissingApproverID
	}
	return decision, nil
}

func awaitDestinationVerificationDecision(ctx workflow.Context, verificationID int64, timeoutSeconds int) (DestinationVerificationDecision, error) {
	if verificationID <= 0 {
		return DestinationVerificationDecision{}, walletstore.ErrMissingVerificationID
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		return DestinationVerificationDecision{}, walletstore.ErrMissingVerificationTimeout
	}
	decisionCh := workflow.GetSignalChannel(ctx, WithdrawalVerificationSignal)
	timer := workflow.NewTimer(ctx, timeout)

	for {
		var decision DestinationVerificationDecision
		timedOut := false
		received := false
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(decisionCh, func(c workflow.ReceiveChannel, more bool) {
			c.Receive(ctx, &decision)
			received = true
		})
		selector.AddFuture(timer, func(f workflow.Future) {
			timedOut = true
		})
		selector.Select(ctx)
		if timedOut {
			return DestinationVerificationDecision{}, ErrWithdrawalVerificationTimedOut
		}
		if received && decision.VerificationID == verificationID {
			return decision, nil
		}
	}
}

type fundingSourceSpec struct {
	sourceType            string
	externalReference     string
	verificationStatus    string
	sourceDetails         map[string]any
	withdrawalMethod      map[string]any
	supportsWithdrawal    bool
	supportsWithdrawalSet bool
	hasData               bool
}

func depositFundingSource(txn *walletstore.PSPTransaction, walletID uuid.UUID, currency string, transactionExternalRef sql.NullString, supportsWithdrawalFromValidation bool, fundedAt time.Time, providerPayloads ...map[string]any) (walletstore.FundingSource, error) {
	if txn == nil {
		return walletstore.FundingSource{}, walletstore.ErrPSPTransactionNotFound
	}
	requestPayload, err := rawJSONMap(txn.RawRequest)
	if err != nil {
		return walletstore.FundingSource{}, err
	}
	requestSpec := fundingSourceSpecFromPayload(requestPayload)
	providerSpec := fundingSourceSpec{}
	for _, payload := range providerPayloads {
		providerSpec = mergeFundingSourceSpecs(providerSpec, fundingSourceSpecFromPayload(payload))
	}
	spec := mergeFundingSourceSpecs(requestSpec, providerSpec)
	if spec.sourceType == "" {
		spec.sourceType = "psp"
	}
	externalRef := transactionExternalRef
	if spec.externalReference != "" {
		externalRef = sql.NullString{String: spec.externalReference, Valid: true}
	}
	sourceDetails := mergeAnyMaps(requestSpec.sourceDetails, providerSpec.sourceDetails)
	if sourceDetails == nil {
		sourceDetails = map[string]any{}
	}
	if spec.sourceType != "" {
		sourceDetails["source_type"] = spec.sourceType
	}
	if spec.externalReference != "" {
		sourceDetails["external_reference"] = spec.externalReference
	}
	sourceDetailsRaw, err := auditMetadata(sourceDetails)
	if err != nil {
		return walletstore.FundingSource{}, err
	}
	withdrawalMethod := mergeAnyMaps(requestSpec.withdrawalMethod, providerSpec.withdrawalMethod)
	withdrawalMethodRaw, err := auditMetadata(withdrawalMethod)
	if err != nil {
		return walletstore.FundingSource{}, err
	}
	supportsWithdrawal := supportsWithdrawalFromValidation
	if spec.supportsWithdrawalSet {
		supportsWithdrawal = spec.supportsWithdrawal
	}
	if len(withdrawalMethodRaw) > 0 {
		supportsWithdrawal = true
	}
	verificationStatus := fundingSourceVerificationStatus(requestSpec, providerSpec, spec)
	verifiedAt := sql.NullTime{}
	if verificationStatus == "verified" {
		verifiedAt = sql.NullTime{Time: fundedAt, Valid: true}
	}
	return walletstore.FundingSource{
		TenantID:           txn.TenantID,
		WalletID:           walletID,
		SourceType:         spec.sourceType,
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

func rawJSONMap(raw walletstore.RawJSON) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
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
	spec.hasData = spec.sourceType != "" || spec.externalReference != "" || spec.verificationStatus != "" || spec.sourceDetails != nil || spec.withdrawalMethod != nil || spec.supportsWithdrawalSet
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
	base.hasData = base.hasData || overlay.hasData
	return base
}

func fundingSourceVerificationStatus(requestSpec, providerSpec, merged fundingSourceSpec) string {
	if merged.verificationStatus != "" {
		return merged.verificationStatus
	}
	if !merged.hasData {
		return "verified"
	}
	if requestSpec.externalReference != "" && providerSpec.externalReference != "" && requestSpec.externalReference == providerSpec.externalReference {
		return "verified"
	}
	return "pending"
}

func selectReturnToSource(sources []walletstore.FundingSource, walletID uuid.UUID, currency string, amount int64, providerCode string) (*walletstore.FundingSource, map[string]any, error) {
	for _, source := range sources {
		if err := validateWithdrawalFundingSource(source, walletID, currency, amount, providerCode); err != nil {
			continue
		}
		var details map[string]any
		if err := json.Unmarshal(source.WithdrawalMethod, &details); err != nil {
			return nil, nil, err
		}
		selected := source
		return &selected, details, nil
	}
	return nil, nil, nil
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
	if providerCode != "" && source.PSPProvider.Valid && source.PSPProvider.String != providerCode {
		return walletstore.ErrMissingProviderCode
	}
	if err := walletstore.ValidateFundingSourceReadyForWithdrawal(&source); err != nil {
		return err
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

func int64FromAnyMap(payload map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int64:
			return typed
		case int:
			return int64(typed)
		case float64:
			if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
				continue
			}
			return int64(typed)
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func recordAuditEvent(ctx workflow.Context, event walletstore.AuditEvent) error {
	return workflow.ExecuteActivity(ctx, walletactivity.ActivityRecordAuditEvent, event).Get(ctx, nil)
}

func fundingSourceID(source *walletstore.FundingSource) int64 {
	if source == nil {
		return 0
	}
	return source.ID
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
