package workflow

import (
	"database/sql"
	"encoding/json"
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
	status := result.Status
	if status == "" {
		status = pspTxn.Status
	}
	update := walletstore.PSPStatusUpdate{Status: status}
	if result.ProviderTxID != "" {
		update.PSPTransactionID = sql.NullString{String: result.ProviderTxID, Valid: true}
	}
	if status == "success" {
		update.ConfirmedAt = sql.NullTime{Time: workflow.Now(ctx), Valid: true}
	}
	updateParams := walletactivity.UpdatePSPTransactionStatusParams{
		TenantID:        params.TenantID,
		ClientReference: params.ClientReference,
		Update:          update,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdatePSPTransactionStatus, updateParams).Get(ctx, nil); err != nil {
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
	if status != "success" {
		return nil
	}

	var treasury walletstore.Wallet
	treasuryParams := walletactivity.EnsureSystemWalletParams{
		TenantID:   params.TenantID,
		Currency:   resolved.WalletCurrency,
		WalletCode: walletstore.SystemTreasury,
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
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDoubleEntry, depositEntry).Get(ctx, nil); err != nil {
		return err
	}
	var posted walletstore.DoubleEntryResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteDoubleEntry, depositEntry).Get(ctx, &posted); err != nil {
		return err
	}

	now := workflow.Now(ctx)
	externalRef := pspTxn.PSPTransactionID
	if result.ProviderTxID != "" {
		externalRef = sql.NullString{String: result.ProviderTxID, Valid: true}
	}
	if !externalRef.Valid {
		externalRef = sql.NullString{String: params.ClientReference, Valid: true}
	}
	source := walletstore.FundingSource{
		TenantID:           params.TenantID,
		WalletID:           walletID,
		SourceType:         "psp",
		PSPProvider:        sql.NullString{String: pspTxn.PSPProvider, Valid: pspTxn.PSPProvider != ""},
		ExternalReference:  externalRef,
		VerificationStatus: "verified",
		VerifiedAt:         sql.NullTime{Time: now, Valid: true},
		Currency:           resolved.WalletCurrency,
		SourceDetails:      json.RawMessage("{}"),
		TotalFunded:        resolved.WalletCreditAmount,
		LastFundedAt:       sql.NullTime{Time: now, Valid: true},
		SupportsWithdrawal: validation.SupportsWithdrawal,
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
		"psp_transaction_id":   result.ProviderTxID,
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
		selected, details, err := selectReturnToSource(sources, params.Request.Currency)
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
			return walletstore.ErrMissingDestinationID
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityResolveWithdrawalDestination, params.TenantID, params.DestinationID).Get(ctx, &destination); err != nil {
			return err
		}
		if !destination.IsActive {
			return walletstore.ErrDestinationNotFound
		}
		if destination.Currency != params.Request.Currency {
			return walletstore.ErrCurrencyMismatch
		}
		if destination.OwnershipStatus != "verified" {
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
				_ = workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateOwnershipVerificationStatus, params.TenantID, stored.ID, "expired", now).Get(ctx, nil)
				return err
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
			} else {
				_ = workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateOwnershipVerificationStatus, params.TenantID, stored.ID, "failed", now).Get(ctx, nil)
				_ = workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateDestinationOwnership, params.TenantID, destination.ID, "rejected", sql.NullTime{}, now).Get(ctx, nil)
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

	releaseHold := func() {
		if holdID <= 0 {
			return
		}
		_ = workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateReleaseHold, params.TenantID, holdID).Get(ctx, nil)
		_ = workflow.ExecuteActivity(ctx, walletactivity.ActivityReleaseHold, params.TenantID, holdID).Get(ctx, nil)
	}

	if params.ApprovalRequired {
		decision, err := awaitWithdrawalApproval(ctx, params)
		if err != nil {
			releaseHold()
			return err
		}
		if !decision.Approved {
			if decision.Reason == "" {
				releaseHold()
				return walletstore.ErrMissingApprovalReason
			}
			rejectMeta, err := auditMetadata(map[string]any{
				"client_reference": params.Request.ClientReference,
				"amount":           params.Request.Amount,
				"currency":         params.Request.Currency,
				"approver_id":      decision.ApproverID,
				"reason":           decision.Reason,
			})
			if err != nil {
				releaseHold()
				return err
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
				releaseHold()
				return err
			}
			releaseHold()
			return walletstore.ErrApprovalRejected
		}
		if decision.ProofOfPayment == "" {
			releaseHold()
			return walletstore.ErrMissingProofOfPayment
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
			releaseHold()
			return metaErr
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
			releaseHold()
			return auditErr
		}
		releaseHold()
		return err
	}
	status := result.Status
	if status == "" {
		status = pspTxn.Status
	}
	update := walletstore.PSPStatusUpdate{Status: status}
	if result.ProviderTxID != "" {
		update.PSPTransactionID = sql.NullString{String: result.ProviderTxID, Valid: true}
	}
	if status == "success" {
		update.ConfirmedAt = sql.NullTime{Time: workflow.Now(ctx), Valid: true}
	}
	updateParams := walletactivity.UpdatePSPTransactionStatusParams{
		TenantID:        params.TenantID,
		ClientReference: params.Request.ClientReference,
		Update:          update,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdatePSPTransactionStatus, updateParams).Get(ctx, nil); err != nil {
		releaseHold()
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
		releaseHold()
		return err
	}
	if status != "success" {
		failMeta, metaErr := auditMetadata(map[string]any{
			"client_reference": params.Request.ClientReference,
			"provider_code":    providerCode,
			"psp_status":       status,
			"amount":           params.Request.Amount,
			"currency":         params.Request.Currency,
		})
		if metaErr != nil {
			releaseHold()
			return metaErr
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
			releaseHold()
			return auditErr
		}
		releaseHold()
		return fmt.Errorf("withdrawal status %s", status)
	}

	var treasury walletstore.Wallet
	treasuryParams := walletactivity.EnsureSystemWalletParams{
		TenantID:   params.TenantID,
		Currency:   validation.Currency,
		WalletCode: walletstore.SystemTreasury,
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, treasuryParams).Get(ctx, &treasury); err != nil {
		releaseHold()
		return err
	}

	withdrawEntry := walletstore.DoubleEntryParams{
		TenantID:       params.TenantID,
		IdempotencyKey: params.Request.ClientReference + ":withdrawal",
		Currency:       validation.Currency,
		ReferenceType:  "withdrawal",
		ReferenceID:    params.Request.ClientReference,
		DebitWalletID:  walletID,
		CreditWalletID: treasury.ID,
		Amount:         params.Request.Amount,
		Description:    "withdrawal",
	}
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDoubleEntry, withdrawEntry).Get(ctx, nil); err != nil {
		releaseHold()
		return err
	}
	var posted walletstore.DoubleEntryResult
	if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteDoubleEntry, withdrawEntry).Get(ctx, &posted); err != nil {
		releaseHold()
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
			Currency:   validation.Currency,
			WalletCode: walletstore.SystemFees,
		}
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityEnsureSystemWallet, feesParams).Get(ctx, &feesWallet); err != nil {
			releaseHold()
			return err
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
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityValidateDoubleEntry, feeEntry).Get(ctx, nil); err != nil {
			releaseHold()
			return err
		}
		var feePosted walletstore.DoubleEntryResult
		if err := workflow.ExecuteActivity(ctx, walletactivity.ActivityExecuteDoubleEntry, feeEntry).Get(ctx, &feePosted); err != nil {
			releaseHold()
			return err
		}
		_ = feePosted
	}

	releaseHold()

	now := workflow.Now(ctx)
	if destinationID > 0 {
		_ = workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateWithdrawalDestinationUsage, params.TenantID, destinationID, params.Request.Amount, now).Get(ctx, nil)
	}
	if fundingSource != nil {
		_ = workflow.ExecuteActivity(ctx, walletactivity.ActivityUpdateFundingSourceUsage, params.TenantID, fundingSource.ID, params.Request.Amount, now).Get(ctx, nil)
	}
	metadata, err := auditMetadata(map[string]any{
		"client_reference":   params.Request.ClientReference,
		"provider_code":      providerCode,
		"psp_transaction_id": result.ProviderTxID,
		"amount":             params.Request.Amount,
		"currency":           params.Request.Currency,
		"fee_amount":         feeAmount,
		"destination_id":     destinationID,
		"funding_source_id":  fundingSourceID(fundingSource),
		"ledger_transaction": posted.TransactionID,
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
	if err := validateManualTransferDecision(params.RequestedBy, decision); err != nil {
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
	rejectMeta, err := auditMetadata(map[string]any{
		"transfer_type": params.TransferType,
		"amount":        params.Amount,
		"currency":      params.Currency,
		"approver_id":   decision.ApproverID,
		"reason":        decision.Reason,
	})
	if err != nil {
		return err
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
			Currency:      txn.Currency,
			Direction:     txn.Direction,
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

	var decision DestinationVerificationDecision
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
		return DestinationVerificationDecision{}, ErrWithdrawalVerificationTimedOut
	}
	if decision.VerificationID != verificationID {
		return DestinationVerificationDecision{}, walletstore.ErrMissingVerificationID
	}
	return decision, nil
}

func selectReturnToSource(sources []walletstore.FundingSource, currency string) (*walletstore.FundingSource, map[string]any, error) {
	for _, source := range sources {
		if currency != "" && source.Currency != currency {
			continue
		}
		if len(source.WithdrawalMethod) == 0 {
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

func regionFromRawRequest(raw json.RawMessage) string {
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

func recordAuditEvent(ctx workflow.Context, event walletstore.AuditEvent) error {
	return workflow.ExecuteActivity(ctx, walletactivity.ActivityRecordAuditEvent, event).Get(ctx, nil)
}

func fundingSourceID(source *walletstore.FundingSource) int64 {
	if source == nil {
		return 0
	}
	return source.ID
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
