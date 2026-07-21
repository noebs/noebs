package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletfees "github.com/adonese/noebs/wallet/fees"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestWithdrawalFXSettlementUsesWalletAccountingUnits(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Withdrawal)
	registerWithdrawalCompensationTestActivities(env)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.EnsureSystemWalletParams) (*walletstore.Wallet, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityEnsureSystemWallet)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.AddPSPTransactionAmountsParams) ([]walletstore.PSPTransactionAmount, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityAddPSPTransactionAmounts)},
	)

	const (
		tenantID        = "tenant"
		clientReference = "withdrawal-fx"
		destinationID   = int64(55)
		fundingSourceID = int64(66)
		holdID          = int64(77)
		reservationID   = int64(88)
	)
	walletID := uuid.New()
	treasuryID := uuid.New()
	feesID := uuid.New()
	txn := withdrawalWorkflowTestTransaction(t, tenantID, clientReference, walletID, func(request *walletstore.WithdrawalRequestSnapshot) {
		request.DestinationID = destinationID
		request.Currency = "USD"
		request.Amount = 100
	})
	txn.ID = 10
	txn.Status = walletstore.PSPStatusInitiated
	conversionAt := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	validation := walletvalidation.WithdrawalValidationResult{
		WalletID: walletID, Currency: "AED", Amount: 370,
		Fee: &walletfees.FeeResult{TotalFee: 30}, TotalDebit: 400,
		PayoutAmount: 100, PayoutCurrency: "USD", PayoutCurrencyUnitID: 21,
		WalletDebitAmount: 370, WalletCurrency: "AED", WalletCurrencyUnitID: 11,
		AppliedFXRate:         decimal.NullDecimal{Decimal: decimal.RequireFromString("3.7"), Valid: true},
		AppliedFXSource:       "test-rate",
		AppliedFXConversionAt: conversionAt,
	}
	source := verifiedWithdrawalFundingSource(fundingSourceID, tenantID, walletID)
	source.Currency = "AED"
	source.TotalFunded = 1_000
	destination := walletstore.WithdrawalDestination{
		ID: destinationID, TenantID: tenantID, WalletID: walletID,
		DestinationType: source.SourceType, PSPProvider: source.PSPProvider,
		DestinationDetails: source.WithdrawalMethod, Currency: "AED",
		LinkedFundingSourceID: fundingSourceID, IsActive: true,
	}

	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).
		Return(txn, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateWithdrawal), mock.Anything, mock.Anything).
		Return(&validation, nil).Once()
	env.OnActivity(string(walletactivity.ActivityResolveWithdrawalDestination), mock.Anything, tenantID, destinationID).
		Return(&destination, nil).Once()
	env.OnActivity(string(walletactivity.ActivityResolveFundingSource), mock.Anything, tenantID, fundingSourceID).
		Return(source, nil).Once()
	limitUsage := walletstore.LimitUsageParams{
		TenantID: tenantID, CommandID: "withdrawal:" + clientReference, WalletID: walletID,
		TransactionType: "withdrawal", Currency: "AED", Amount: 370,
	}
	env.OnActivity(string(walletactivity.ActivityReserveLimitUsage), mock.Anything, limitUsage).
		Return(&walletstore.LimitUsageReservation{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateHold), mock.Anything, mock.MatchedBy(func(params walletstore.HoldParams) bool {
		return params.Amount == 400
	})).Return(struct{}{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityCreateHold), mock.Anything, mock.Anything).
		Return(&walletstore.BalanceHold{
			ID: holdID, TenantID: tenantID, WalletID: walletID, Amount: 400, AmountRemaining: 400,
			Status: walletstore.HoldStatusActive, ExpiresAt: time.Now().UTC().Add(time.Hour),
		}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityReserveFundingSourceWithdrawal), mock.Anything, walletstore.ReserveFundingSourceWithdrawalParams{
		TenantID: tenantID, WorkflowID: "default-test-workflow-id", CandidateSourceIDs: []int64{fundingSourceID},
		WalletID: walletID, Amount: 370, Currency: "AED", ProviderCode: "pay",
	}).Return(&walletstore.FundingSourceWithdrawalReservationResult{
		Reservation: walletstore.FundingSourceWithdrawalReservation{
			ID: reservationID, TenantID: tenantID, WorkflowID: "default-test-workflow-id",
			FundingSourceID: fundingSourceID, Amount: 370, Currency: "AED", ProviderCode: "pay",
		},
		Source: *source,
	}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityCommitHold), mock.Anything, tenantID, holdID).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivitySendPayout), mock.Anything, mock.MatchedBy(func(params walletactivity.SendPayoutParams) bool {
		return params.CurrencyUnitID == 21 && params.Request.Amount == 100 && params.Request.Currency == "USD"
	})).Return(&walletpsp.PayoutResult{
		ProviderTxID: "provider-fx", Amount: 100, Currency: "USD", Status: walletstore.PSPStatusSuccess,
	}, nil).Once()
	stored := *txn
	stored.Status = walletstore.PSPStatusSuccess
	stored.PSPTransactionID = sql.NullString{String: "provider-fx", Valid: true}
	env.OnActivity(string(walletactivity.ActivityUpdatePSPTransactionStatus), mock.Anything, mock.Anything).
		Return(&stored, nil).Once()
	env.OnActivity(string(walletactivity.ActivityAddPSPTransactionAmounts), mock.Anything, mock.MatchedBy(func(params walletactivity.AddPSPTransactionAmountsParams) bool {
		if len(params.Amounts) != 2 {
			return false
		}
		requested, walletDebit := params.Amounts[0], params.Amounts[1]
		return requested.Currency == "USD" && requested.CurrencyUnitID == 21 &&
			walletDebit.Currency == "AED" && walletDebit.CurrencyUnitID == 11 &&
			walletDebit.FxBaseCurrencyUnitID == 21 && walletDebit.FxQuoteCurrencyUnitID == 11 &&
			walletDebit.FxConversionAt.Equal(conversionAt)
	})).
		Return([]walletstore.PSPTransactionAmount{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityEnsureSystemWallet), mock.Anything, mock.MatchedBy(func(params walletactivity.EnsureSystemWalletParams) bool {
		return params.Currency == "AED" && params.CurrencyUnitID == 11 && params.WalletCode == walletstore.SystemTreasury
	})).Return(&walletstore.Wallet{ID: treasuryID, TenantID: tenantID, Currency: "AED", CurrencyUnitID: 11, OwnerType: walletstore.OwnerTypeSystem}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityEnsureSystemWallet), mock.Anything, mock.MatchedBy(func(params walletactivity.EnsureSystemWalletParams) bool {
		return params.Currency == "AED" && params.CurrencyUnitID == 11 && params.WalletCode == walletstore.SystemFees
	})).Return(&walletstore.Wallet{ID: feesID, TenantID: tenantID, Currency: "AED", CurrencyUnitID: 11, OwnerType: walletstore.OwnerTypeSystem}, nil).Once()
	var captured walletstore.HeldWithdrawalSettlementParams
	env.OnActivity(string(walletactivity.ActivityValidateHeldWithdrawalSettlement), mock.Anything, mock.MatchedBy(func(params walletstore.HeldWithdrawalSettlementParams) bool {
		captured = params
		return true
	})).Return(struct{}{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityExecuteHeldWithdrawalSettlement), mock.Anything, mock.Anything).
		Return(&walletstore.MultiLegSettlementResult{TransactionID: 999}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityRecordAuditEvent), mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(Withdrawal, WithdrawalParams{TenantID: tenantID, ClientReference: clientReference})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("FX withdrawal workflow: %v", err)
	}
	if captured.Settlement.Currency != "AED" || captured.Settlement.LimitUsage.Currency != "AED" ||
		captured.Settlement.LimitUsage.Amount != 370 || captured.WithdrawalDestinationID != destinationID ||
		len(captured.Settlement.Transfers) != 2 ||
		captured.Settlement.Transfers[0].Amount != 370 || captured.Settlement.Transfers[1].Amount != 30 {
		t.Fatalf("captured FX settlement = %+v", captured)
	}
	env.AssertExpectations(t)
}

func TestWithdrawalApprovalRejectionReturnsHoldReleaseError(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Withdrawal)
	registerWithdrawalCompensationTestActivities(env)

	tenantID := "tenant"
	walletID := uuid.New()
	clientReference := "withdrawal-ref"
	holdID := int64(77)

	txn := withdrawalWorkflowTestTransaction(t, tenantID, clientReference, walletID, func(request *walletstore.WithdrawalRequestSnapshot) {
		request.DestinationID = 55
		request.ApprovalRequired = true
		request.ApprovalTimeoutSeconds = 60
	})
	txn.ID = 10
	txn.Status = walletstore.PSPStatusPending
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).
		Return(txn, nil)
	env.OnActivity(string(walletactivity.ActivityValidateWithdrawal), mock.Anything, mock.Anything).
		Return(&walletvalidation.WithdrawalValidationResult{
			WalletID:             walletID,
			Currency:             "AED",
			Amount:               100,
			TotalDebit:           100,
			PayoutAmount:         100,
			PayoutCurrency:       "AED",
			PayoutCurrencyUnitID: 11,
			WalletDebitAmount:    100,
			WalletCurrency:       "AED",
			WalletCurrencyUnitID: 11,
		}, nil)
	env.OnActivity(string(walletactivity.ActivityResolveWithdrawalDestination), mock.Anything, tenantID, int64(55)).
		Return(&walletstore.WithdrawalDestination{
			ID: 55, TenantID: tenantID, WalletID: walletID, DestinationType: "bank_account",
			PSPProvider: sql.NullString{String: "pay", Valid: true}, DestinationDetails: []byte(`{"iban":"AE000000000000000000000"}`),
			Currency: "AED", LinkedFundingSourceID: 66, IsActive: true,
		}, nil)
	env.OnActivity(string(walletactivity.ActivityResolveFundingSource), mock.Anything, tenantID, int64(66)).
		Return(verifiedWithdrawalFundingSource(66, tenantID, walletID), nil)
	limitUsage := walletstore.LimitUsageParams{
		TenantID: tenantID, CommandID: "withdrawal:" + clientReference, WalletID: walletID,
		TransactionType: "withdrawal", Currency: "AED", Amount: 100,
	}
	env.OnActivity(string(walletactivity.ActivityReserveLimitUsage), mock.Anything, limitUsage).
		Return(&walletstore.LimitUsageReservation{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityReleaseLimitUsage), mock.Anything, limitUsage).
		Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateHold), mock.Anything, mock.Anything).
		Return(struct{}{}, nil)
	env.OnActivity(string(walletactivity.ActivityCreateHold), mock.Anything, mock.Anything).
		Return(&walletstore.BalanceHold{ID: holdID, TenantID: tenantID, WalletID: walletID, Status: "active", ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil)
	env.OnActivity(string(walletactivity.ActivityReserveFundingSourceWithdrawal), mock.Anything, mock.Anything).
		Return(&walletstore.FundingSourceWithdrawalReservationResult{
			Reservation: walletstore.FundingSourceWithdrawalReservation{ID: 88, TenantID: tenantID, WorkflowID: "default-test-workflow-id"},
			Source:      *verifiedWithdrawalFundingSource(66, tenantID, walletID),
		}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityLookupWorkflowDecision), mock.Anything, mock.Anything).
		Return(walletstore.WorkflowDecisionLookup{Found: true, Decision: walletstore.WorkflowDecision{
			TenantID: tenantID, WorkflowID: "default-test-workflow-id", Kind: walletstore.WorkflowDecisionWithdrawal,
			SubjectID: 10, Approved: false, DecidedByOperatorID: 42,
			Reason: sql.NullString{String: "risk", Valid: true},
		}}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityRecordAuditEvent), mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(string(walletactivity.ActivityValidateReleaseHold), mock.Anything, tenantID, holdID).
		Return(struct{}{}, nil)
	env.OnActivity(string(walletactivity.ActivityReleaseHold), mock.Anything, tenantID, holdID).
		Return(temporal.NewNonRetryableApplicationError("release hold failed", "release_hold_failed", nil))
	env.OnActivity(string(walletactivity.ActivityReleaseFundingSourceWithdrawal), mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(Withdrawal, WithdrawalParams{
		TenantID:        tenantID,
		ClientReference: clientReference,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow to complete")
	}
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("workflow error = nil, want approval and hold release errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, walletstore.ErrApprovalRejected.Error()) || !strings.Contains(msg, "release hold failed") {
		t.Fatalf("workflow error = %v, want approval rejection and release failure", err)
	}
	env.AssertExpectations(t)
}

func TestWithdrawalReturnToSourceWithoutEligibleSourceFailsWithFundingSourceNotFound(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Withdrawal)
	registerWithdrawalCompensationTestActivities(env)

	tenantID := "tenant"
	walletID := uuid.New()
	clientReference := "withdrawal-rts-ref"

	txn := withdrawalWorkflowTestTransaction(t, tenantID, clientReference, walletID, func(request *walletstore.WithdrawalRequestSnapshot) {
		request.AllowReturnToSource = true
	})
	txn.ID = 10
	txn.Status = walletstore.PSPStatusPending
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).
		Return(txn, nil)
	env.OnActivity(string(walletactivity.ActivityValidateWithdrawal), mock.Anything, mock.Anything).
		Return(&walletvalidation.WithdrawalValidationResult{
			WalletID:             walletID,
			Currency:             "AED",
			Amount:               100,
			TotalDebit:           100,
			PayoutAmount:         100,
			PayoutCurrency:       "AED",
			PayoutCurrencyUnitID: 11,
			WalletDebitAmount:    100,
			WalletCurrency:       "AED",
			WalletCurrencyUnitID: 11,
		}, nil)
	env.OnActivity(string(walletactivity.ActivityGetReturnToSourceOptions), mock.Anything, tenantID, walletID).
		Return([]walletstore.FundingSource{}, nil)

	env.ExecuteWorkflow(Withdrawal, WithdrawalParams{
		TenantID:        tenantID,
		ClientReference: clientReference,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow to complete")
	}
	err := env.GetWorkflowError()
	if err == nil || !strings.Contains(err.Error(), walletstore.ErrFundingSourceNotFound.Error()) {
		t.Fatalf("workflow error = %v, want %v", err, walletstore.ErrFundingSourceNotFound)
	}
	env.AssertExpectations(t)
}

func TestWithdrawalRejectsTemporalStartNotBoundToPersistedWorkflow(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Withdrawal)
	registerWithdrawalCompensationTestActivities(env)

	tenantID := "tenant"
	clientReference := "victim-withdrawal"
	txn := withdrawalWorkflowTestTransaction(t, tenantID, clientReference, uuid.New(), func(request *walletstore.WithdrawalRequestSnapshot) {
		request.DestinationID = 55
	})
	txn.WorkflowID = sql.NullString{String: "withdrawal-tenant-victim-withdrawal", Valid: true}
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).
		Return(txn, nil).Once()

	env.ExecuteWorkflow(Withdrawal, WithdrawalParams{TenantID: tenantID, ClientReference: clientReference})

	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), walletstore.ErrInvalidWithdrawalRequest.Error()) {
		t.Fatalf("workflow error = %v, want %v", err, walletstore.ErrInvalidWithdrawalRequest)
	}
	env.AssertExpectations(t)
}

func TestWithdrawalPermanentDispatchRejectionClosesFailedAndReleases(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Withdrawal)
	registerWithdrawalCompensationTestActivities(env)

	tenantID := "tenant"
	walletID := uuid.New()
	clientReference := "ambiguous-withdrawal"
	holdID := int64(91)
	pspTxn := withdrawalWorkflowTestTransaction(t, tenantID, clientReference, walletID, func(request *walletstore.WithdrawalRequestSnapshot) {
		request.DestinationID = 55
	})
	pspTxn.ID = 12
	pspTxn.Status = walletstore.PSPStatusInitiated
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).
		Return(pspTxn, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateWithdrawal), mock.Anything, mock.Anything).
		Return(&walletvalidation.WithdrawalValidationResult{
			WalletID: walletID, Currency: "AED", Amount: 100, TotalDebit: 100,
			PayoutAmount: 100, PayoutCurrency: "AED", PayoutCurrencyUnitID: 11,
			WalletDebitAmount: 100, WalletCurrency: "AED", WalletCurrencyUnitID: 11,
		}, nil)
	env.OnActivity(string(walletactivity.ActivityResolveWithdrawalDestination), mock.Anything, tenantID, int64(55)).
		Return(&walletstore.WithdrawalDestination{
			ID: 55, TenantID: tenantID, WalletID: walletID, DestinationType: "bank_account",
			PSPProvider:        sql.NullString{String: "pay", Valid: true},
			DestinationDetails: []byte(`{"iban":"AE000000000000000000000"}`), Currency: "AED",
			LinkedFundingSourceID: 66, IsActive: true,
		}, nil)
	env.OnActivity(string(walletactivity.ActivityResolveFundingSource), mock.Anything, tenantID, int64(66)).
		Return(verifiedWithdrawalFundingSource(66, tenantID, walletID), nil)
	limitUsage := walletstore.LimitUsageParams{
		TenantID: tenantID, CommandID: "withdrawal:" + clientReference, WalletID: walletID,
		TransactionType: "withdrawal", Currency: "AED", Amount: 100,
	}
	env.OnActivity(string(walletactivity.ActivityReserveLimitUsage), mock.Anything, limitUsage).
		Return(&walletstore.LimitUsageReservation{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityReleaseLimitUsage), mock.Anything, limitUsage).
		Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateHold), mock.Anything, mock.Anything).Return(struct{}{}, nil)
	env.OnActivity(string(walletactivity.ActivityCreateHold), mock.Anything, mock.Anything).
		Return(&walletstore.BalanceHold{ID: holdID, TenantID: tenantID, WalletID: walletID, Status: walletstore.HoldStatusActive}, nil)
	env.OnActivity(string(walletactivity.ActivityReserveFundingSourceWithdrawal), mock.Anything, mock.Anything).
		Return(&walletstore.FundingSourceWithdrawalReservationResult{
			Reservation: walletstore.FundingSourceWithdrawalReservation{ID: 92, TenantID: tenantID, WorkflowID: "default-test-workflow-id"},
			Source:      *verifiedWithdrawalFundingSource(66, tenantID, walletID),
		}, nil).Once()
	committed := false
	env.OnActivity(string(walletactivity.ActivityCommitHold), mock.Anything, tenantID, holdID).
		Run(func(mock.Arguments) { committed = true }).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivitySendPayout), mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			if !committed {
				t.Error("payout dispatched before the hold was committed")
			}
		}).
		Return(nil, temporal.NewNonRetryableApplicationError(
			walletpsp.ErrPSPPermanent.Error(), walletactivity.PSPDispatchRejectedErrorType, walletpsp.ErrPSPPermanent,
		)).Once()
	statusUpdated := false
	env.OnActivity(string(walletactivity.ActivityUpdatePSPTransactionStatus), mock.Anything, mock.MatchedBy(func(params walletactivity.UpdatePSPTransactionStatusParams) bool {
		return params.TenantID == tenantID && params.ClientReference == clientReference && params.Update.Status == walletstore.PSPStatusFailed
	})).Run(func(mock.Arguments) { statusUpdated = true }).Return(&walletstore.PSPTransaction{
		ID: 12, TenantID: tenantID, PSPProvider: "pay", ClientReference: clientReference,
		Direction: "outbound", Amount: 100, Currency: "AED", Status: walletstore.PSPStatusFailed,
	}, nil).Once()

	actions := make([]string, 0, 2)
	env.OnActivity(string(walletactivity.ActivityRecordAuditEvent), mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			event := args.Get(1).(walletstore.AuditEvent)
			actions = append(actions, event.Action)
		}).Return(nil).Twice()
	env.OnActivity(string(walletactivity.ActivityValidateReleaseHold), mock.Anything, tenantID, holdID).Return(struct{}{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityReleaseHold), mock.Anything, tenantID, holdID).
		Run(func(mock.Arguments) {
			if !statusUpdated {
				t.Error("committed hold released before failed status was persisted")
			}
		}).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityReleaseFundingSourceWithdrawal), mock.Anything, mock.Anything).
		Return(nil).Once()
	env.ExecuteWorkflow(Withdrawal, WithdrawalParams{
		TenantID: tenantID, ClientReference: clientReference,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow to complete")
	}
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "withdrawal status failed") {
		t.Fatalf("workflow error = %v, want terminal failed status", err)
	}
	if len(actions) != 2 || actions[0] != "rejected" || actions[1] != "failed" {
		t.Fatalf("audit actions = %v, want rejected then failed", actions)
	}
	env.AssertExpectations(t)
}

func TestWithdrawalUnknownDispatchKeepsReservationsUntilReconciledFailure(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Withdrawal)
	registerWithdrawalCompensationTestActivities(env)

	tenantID := "tenant"
	walletID := uuid.New()
	clientReference := "unknown-withdrawal"
	transportKey := "withdrawal-transport-key"
	holdID := int64(101)
	pspTxn := withdrawalWorkflowTestTransaction(t, tenantID, clientReference, walletID, func(request *walletstore.WithdrawalRequestSnapshot) {
		request.DestinationID = 55
	})
	pspTxn.ID = 14
	pspTxn.IdempotencyKey = transportKey
	pspTxn.Status = walletstore.PSPStatusInitiated
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).
		Return(pspTxn, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateWithdrawal), mock.Anything, mock.Anything).
		Return(&walletvalidation.WithdrawalValidationResult{
			WalletID: walletID, Currency: "AED", Amount: 100, TotalDebit: 100,
			PayoutAmount: 100, PayoutCurrency: "AED", PayoutCurrencyUnitID: 11,
			WalletDebitAmount: 100, WalletCurrency: "AED", WalletCurrencyUnitID: 11,
		}, nil)
	env.OnActivity(string(walletactivity.ActivityResolveWithdrawalDestination), mock.Anything, tenantID, int64(55)).
		Return(&walletstore.WithdrawalDestination{
			ID: 55, TenantID: tenantID, WalletID: walletID, DestinationType: "bank_account",
			PSPProvider:        sql.NullString{String: "pay", Valid: true},
			DestinationDetails: []byte(`{"iban":"AE000000000000000000000"}`), Currency: "AED",
			LinkedFundingSourceID: 66, IsActive: true,
		}, nil)
	env.OnActivity(string(walletactivity.ActivityResolveFundingSource), mock.Anything, tenantID, int64(66)).
		Return(verifiedWithdrawalFundingSource(66, tenantID, walletID), nil)
	limitUsage := walletstore.LimitUsageParams{
		TenantID: tenantID, CommandID: "withdrawal:" + clientReference, WalletID: walletID,
		TransactionType: "withdrawal", Currency: "AED", Amount: 100,
	}
	env.OnActivity(string(walletactivity.ActivityReserveLimitUsage), mock.Anything, limitUsage).
		Return(&walletstore.LimitUsageReservation{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateHold), mock.Anything, mock.Anything).Return(struct{}{}, nil)
	env.OnActivity(string(walletactivity.ActivityCreateHold), mock.Anything, mock.Anything).
		Return(&walletstore.BalanceHold{ID: holdID, TenantID: tenantID, WalletID: walletID, Status: walletstore.HoldStatusActive}, nil)
	env.OnActivity(string(walletactivity.ActivityReserveFundingSourceWithdrawal), mock.Anything, mock.Anything).
		Return(&walletstore.FundingSourceWithdrawalReservationResult{
			Reservation: walletstore.FundingSourceWithdrawalReservation{ID: 102, TenantID: tenantID, WorkflowID: "default-test-workflow-id"},
			Source:      *verifiedWithdrawalFundingSource(66, tenantID, walletID),
		}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityCommitHold), mock.Anything, tenantID, holdID).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivitySendPayout), mock.Anything, mock.MatchedBy(func(params walletactivity.SendPayoutParams) bool {
		return params.TenantID == tenantID &&
			params.CurrencyUnitID == pspTxn.CurrencyUnitID &&
			params.Request.ClientReference == clientReference &&
			params.Request.IdempotencyKey == transportKey
	})).Return(nil, walletpsp.ErrPSPTemporary).Times(3)

	signal := walletstore.PSPWorkflowSignal{
		ProviderTxID: "provider-after-reconcile", Amount: 100, Currency: "AED",
		Status: walletstore.PSPStatusFailed, RawResponse: walletstore.RawJSON(`{"status":"failed"}`),
	}
	payload, err := json.Marshal(signal)
	if err != nil {
		t.Fatal(err)
	}
	reconciled := *pspTxn
	reconciled.Status = walletstore.PSPStatusFailed
	reconciled.PSPTransactionID = sql.NullString{String: signal.ProviderTxID, Valid: true}
	reconciled.WorkflowSignalPayload = walletstore.RawJSON(payload)
	unknownPersisted := false
	env.OnActivity(string(walletactivity.ActivityUpdatePSPTransactionStatus), mock.Anything, mock.MatchedBy(func(params walletactivity.UpdatePSPTransactionStatusParams) bool {
		var audit map[string]any
		if err := json.Unmarshal(params.Update.RawResponse, &audit); err != nil {
			return false
		}
		return params.TenantID == tenantID && params.ClientReference == clientReference &&
			params.Update.Status == walletstore.PSPStatusProcessing && audit["dispatch_outcome"] == "unknown"
	})).Run(func(mock.Arguments) { unknownPersisted = true }).Return(&reconciled, nil).Once()
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.Anything).
		Return(&reconciled, nil).Once()
	env.OnActivity(string(walletactivity.ActivityRecordAuditEvent), mock.Anything, mock.MatchedBy(func(event walletstore.AuditEvent) bool {
		return event.Action == "failed"
	})).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityReleaseLimitUsage), mock.Anything, limitUsage).
		Run(func(mock.Arguments) {
			if !unknownPersisted {
				t.Error("limit usage released before the unknown outcome was persisted and reconciled")
			}
		}).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateReleaseHold), mock.Anything, tenantID, holdID).Return(struct{}{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityReleaseHold), mock.Anything, tenantID, holdID).
		Run(func(mock.Arguments) {
			if !unknownPersisted {
				t.Error("hold released before the unknown outcome was persisted and reconciled")
			}
		}).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityReleaseFundingSourceWithdrawal), mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(Withdrawal, WithdrawalParams{TenantID: tenantID, ClientReference: clientReference})

	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "withdrawal status failed") {
		t.Fatalf("workflow error = %v, want reconciled terminal failure", err)
	}
	env.AssertExpectations(t)
}

func withdrawalWorkflowTestTransaction(
	t *testing.T,
	tenantID string,
	clientReference string,
	walletID uuid.UUID,
	configure func(*walletstore.WithdrawalRequestSnapshot),
) *walletstore.PSPTransaction {
	t.Helper()
	request := walletstore.WithdrawalRequestSnapshot{
		TenantID:          tenantID,
		ClientReference:   clientReference,
		ProviderCode:      "pay",
		WalletID:          walletID,
		Amount:            100,
		Currency:          "AED",
		OwnerType:         walletstore.OwnerTypeUser,
		OwnerID:           "user-1",
		HoldExpirySeconds: 3600,
		Metadata:          map[string]any{},
	}
	configure(&request)
	currencyUnitID := int64(11)
	if request.Currency == "USD" {
		currencyUnitID = 21
	}
	raw, err := walletstore.MarshalWithdrawalRequest(request)
	if err != nil {
		t.Fatalf("marshal withdrawal request: %v", err)
	}
	return &walletstore.PSPTransaction{
		TenantID:                tenantID,
		PSPProvider:             request.ProviderCode,
		IdempotencyKey:          clientReference,
		ClientReference:         clientReference,
		Direction:               "outbound",
		WalletID:                uuid.NullUUID{UUID: request.WalletID, Valid: true},
		OwnerType:               sql.NullString{String: request.OwnerType, Valid: true},
		OwnerID:                 sql.NullString{String: request.OwnerID, Valid: true},
		WithdrawalDestinationID: sql.NullInt64{Int64: request.DestinationID, Valid: request.DestinationID > 0},
		AllowReturnToSource:     sql.NullBool{Bool: request.AllowReturnToSource, Valid: true},
		Amount:                  request.Amount,
		Currency:                request.Currency,
		CurrencyUnitID:          currencyUnitID,
		WorkflowID:              sql.NullString{String: "default-test-workflow-id", Valid: true},
		RawRequest:              raw,
	}
}

func registerWithdrawalCompensationTestActivities(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterActivityWithOptions(
		func(context.Context, string, string) (*walletstore.PSPTransaction, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityGetPSPTransactionByReference)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletvalidation.WithdrawalValidationRequest) (*walletvalidation.WithdrawalValidationResult, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityValidateWithdrawal)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, int64) (*walletstore.WithdrawalDestination, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityResolveWithdrawalDestination)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, int64) (*walletstore.FundingSource, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityResolveFundingSource)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, uuid.UUID) ([]walletstore.FundingSource, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityGetReturnToSourceOptions)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.ReserveFundingSourceWithdrawalParams) (*walletstore.FundingSourceWithdrawalReservationResult, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityReserveFundingSourceWithdrawal)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.ReleaseFundingSourceWithdrawalParams) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityReleaseFundingSourceWithdrawal)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.LimitUsageParams) (*walletstore.LimitUsageReservation, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityReserveLimitUsage)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.LimitUsageParams) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityReleaseLimitUsage)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.ConsumeLimitUsageParams) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityConsumeLimitUsage)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.HeldWithdrawalSettlementParams) (struct{}, error) {
			return struct{}{}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityValidateHeldWithdrawalSettlement)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.HeldWithdrawalSettlementParams) (*walletstore.MultiLegSettlementResult, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityExecuteHeldWithdrawalSettlement)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.HoldParams) (struct{}, error) { return struct{}{}, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityValidateHold)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.HoldParams) (*walletstore.BalanceHold, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityCreateHold)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.AuditEvent) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityRecordAuditEvent)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, int64) (struct{}, error) { return struct{}{}, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityValidateReleaseHold)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, int64) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityReleaseHold)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, int64) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityCommitHold)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.SendPayoutParams) (*walletpsp.PayoutResult, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivitySendPayout)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.UpdatePSPTransactionStatusParams) (*walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityUpdatePSPTransactionStatus)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.WorkflowDecisionKey) (walletstore.WorkflowDecisionLookup, error) {
			return walletstore.WorkflowDecisionLookup{}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityLookupWorkflowDecision)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.AcknowledgePSPWorkflowSignalParams) (*walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityAcknowledgePSPWorkflowSignal)},
	)
}

func verifiedWithdrawalFundingSource(id int64, tenantID string, walletID uuid.UUID) *walletstore.FundingSource {
	return &walletstore.FundingSource{
		ID: id, TenantID: tenantID, WalletID: walletID,
		SourceType:         "bank_account",
		PSPProvider:        sql.NullString{String: "pay", Valid: true},
		VerificationStatus: "verified", VerifiedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true},
		Currency: "AED", TotalFunded: 1_000, SupportsWithdrawal: true,
		WithdrawalMethod: []byte(`{"iban":"AE000000000000000000000"}`),
	}
}
