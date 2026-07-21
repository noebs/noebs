package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestStatusFromPSPTransactionTreatsRawResponseAsAuditOnly(t *testing.T) {
	txn := &walletstore.PSPTransaction{
		Status:           "processing",
		PSPTransactionID: sql.NullString{String: "stored-psp-id", Valid: true},
		Amount:           100,
		Currency:         "USD",
		CurrencyUnitID:   101,
		RawResponse:      walletstore.RawJSON(`{"status":"SUCCESS","amount":2500,"currency":"AED","transaction_id":"provider-psp-id"}`),
	}

	status, err := statusFromPSPTransaction(txn)
	if err != nil {
		t.Fatalf("status from transaction: %v", err)
	}
	if status.Status != "processing" {
		t.Fatalf("status = %q, want typed relational status", status.Status)
	}
	if status.ProviderTxID != "stored-psp-id" {
		t.Fatalf("provider transaction id = %q, want typed relational id", status.ProviderTxID)
	}
	if status.Amount != 100 || status.Currency != "USD" {
		t.Fatalf("raw response supplied executable amount/currency: %+v", status)
	}
	if !reflect.DeepEqual(status.RawResponse, map[string]any{
		"status": "SUCCESS", "amount": float64(2500), "currency": "AED", "transaction_id": "provider-psp-id",
	}) {
		t.Fatalf("raw audit payload = %+v", status.RawResponse)
	}
}

func TestPSPStatusScopeDirectionRequiresExplicitStoredDirection(t *testing.T) {
	for _, test := range []struct {
		stored string
		want   string
		err    error
	}{
		{stored: "inbound", want: "deposit"},
		{stored: "outbound", want: "withdrawal"},
		{stored: "", err: walletstore.ErrMissingDirection},
		{stored: "sideways", err: walletstore.ErrInvalidDirection},
	} {
		got, err := pspStatusScopeDirection(test.stored)
		if !errors.Is(err, test.err) || got != test.want {
			t.Errorf("pspStatusScopeDirection(%q) = %q, %v; want %q, %v", test.stored, got, err, test.want, test.err)
		}
	}
}

func TestProviderStatusConversionsDoNotFallBackToStoredStatus(t *testing.T) {
	deposit := statusFromDepositResult(walletpsp.DepositResult{
		ProviderTxID: "provider-deposit-id",
		Status:       "",
	})
	if deposit.Status != "" {
		t.Fatalf("deposit status = %q, want empty", deposit.Status)
	}
	if deposit.ProviderTxID != "provider-deposit-id" {
		t.Fatalf("deposit provider id = %q", deposit.ProviderTxID)
	}

	payout := statusFromPayoutResult(walletpsp.PayoutResult{
		ProviderTxID: "provider-payout-id",
		Amount:       1250,
		Currency:     "AED",
		Status:       "",
	})
	if payout.Status != "" {
		t.Fatalf("payout status = %q, want empty", payout.Status)
	}
	if payout.ProviderTxID != "provider-payout-id" || payout.Amount != 1250 || payout.Currency != "AED" {
		t.Fatalf("payout status = %+v", payout)
	}
}

func TestPSPRemoteActivityRetriesAreBounded(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(pspRemoteRetryTestWorkflow)
	var attempts atomic.Int32
	env.RegisterActivityWithOptions(func(context.Context) error {
		attempts.Add(1)
		return walletpsp.ErrPSPTemporary
	}, activity.RegisterOptions{Name: "FailingPSPRemoteActivity"})

	env.ExecuteWorkflow(pspRemoteRetryTestWorkflow)

	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow error = nil, want exhausted remote activity")
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("remote activity attempts = %d, want 3", got)
	}
}

func pspRemoteRetryTestWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(pspRemoteActivityContext(ctx), "FailingPSPRemoteActivity").Get(ctx, nil)
}

func TestSuccessfulPayoutStatusMustMatchPersistedCommand(t *testing.T) {
	request := walletpsp.PayoutRequest{
		IdempotencyKey: "withdrawal-idem", ClientReference: "withdrawal-1", Amount: 1250, Currency: "AED",
	}
	valid := walletpsp.TxStatus{
		ProviderTxID: "provider-1", Amount: request.Amount, Currency: request.Currency, Status: walletstore.PSPStatusSuccess,
	}
	if err := validateSuccessfulPayoutStatus(valid, request); err != nil {
		t.Fatalf("valid successful payout: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*walletpsp.TxStatus)
		want   error
	}{
		{name: "provider id", mutate: func(status *walletpsp.TxStatus) { status.ProviderTxID = "" }, want: walletstore.ErrMissingPSPTransactionID},
		{name: "amount", mutate: func(status *walletpsp.TxStatus) { status.Amount++ }, want: walletstore.ErrInvalidAmount},
		{name: "currency", mutate: func(status *walletpsp.TxStatus) { status.Currency = "USD" }, want: walletstore.ErrCurrencyMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := valid
			test.mutate(&status)
			if err := validateSuccessfulPayoutStatus(status, request); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDepositRejectsInvalidSuccessfulProviderStatusBeforeStoreUpdate(t *testing.T) {
	cases := []struct {
		name    string
		status  walletpsp.DepositResult
		wantErr error
	}{
		{
			name: "zero-amount",
			status: walletpsp.DepositResult{
				ProviderTxID: "provider-deposit-id",
				Status:       "success",
				Amount:       0,
				Currency:     "AED",
			},
			wantErr: walletstore.ErrInvalidAmount,
		},
		{
			name: "missing-currency",
			status: walletpsp.DepositResult{
				ProviderTxID: "provider-deposit-id",
				Status:       "success",
				Amount:       2500,
				Currency:     "",
			},
			wantErr: walletstore.ErrCurrencyMismatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(Deposit)
			registerDepositStatusValidationTestActivities(env)

			tenantID := "tenant"
			clientReference := "deposit-ref"
			walletID := uuid.New()
			intent := depositStatusTestIntent(tenantID, clientReference, walletID, 10)
			tc.status.ClientReference = clientReference
			env.OnActivity(string(walletactivity.ActivityGetDepositIntentByReference), mock.Anything, tenantID, clientReference).Return(intent, nil)
			env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).
				Return(&walletstore.PSPTransaction{
					ID:               10,
					TenantID:         tenantID,
					PSPProvider:      "pay",
					IdempotencyKey:   "request-key",
					PSPTransactionID: sql.NullString{String: "provider-deposit-id", Valid: true},
					ClientReference:  clientReference,
					Direction:        "inbound",
					Amount:           2500,
					Currency:         "AED",
					CurrencyUnitID:   intent.CurrencyUnitID,
					Status:           "pending",
					WorkflowID:       sql.NullString{String: intent.WorkflowID, Valid: true},
					DepositIntentID:  sql.NullInt64{Int64: intent.ID, Valid: true},
				}, nil)
			env.OnActivity(string(walletactivity.ActivityValidateDeposit), mock.Anything, mock.Anything).
				Return(&walletvalidation.DepositValidationResult{
					WalletID:       walletID,
					Currency:       "AED",
					CurrencyUnitID: intent.CurrencyUnitID,
					Amount:         2500,
					NetAmount:      2500,
				}, nil)
			env.OnActivity(string(walletactivity.ActivityCreateDeposit), mock.Anything, mock.Anything).
				Return(&tc.status, nil)

			env.ExecuteWorkflow(Deposit, DepositParams{
				TenantID:        tenantID,
				IntentReference: clientReference,
			})

			if !env.IsWorkflowCompleted() {
				t.Fatal("expected workflow to complete")
			}
			err := env.GetWorkflowError()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr.Error()) {
				t.Fatalf("workflow error = %v, want %v", err, tc.wantErr)
			}
			env.AssertExpectations(t)
		})
	}
}

func registerDepositStatusValidationTestActivities(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterActivityWithOptions(
		func(context.Context, string, string) (*walletstore.DepositIntent, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityGetDepositIntentByReference)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, string) (*walletstore.PSPTransaction, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityGetPSPTransactionByReference)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletvalidation.DepositValidationRequest) (*walletvalidation.DepositValidationResult, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityValidateDeposit)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.CreateDepositParams) (*walletpsp.DepositResult, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityCreateDeposit)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.UpdatePSPTransactionStatusParams) (*walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityUpdatePSPTransactionStatus)},
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
}

func depositStatusTestIntent(tenantID, reference string, walletID uuid.UUID, id int64) *walletstore.DepositIntent {
	return &walletstore.DepositIntent{
		ID:              id,
		TenantID:        tenantID,
		IntentReference: reference,
		ProviderCode:    "pay",
		WalletID:        walletID,
		OwnerType:       walletstore.OwnerTypeUser,
		OwnerID:         "42",
		Amount:          2500,
		Currency:        "AED",
		CurrencyUnitID:  101,
		IdempotencyKey:  "request-key",
		WorkflowID:      "default-test-workflow-id",
		Metadata:        walletstore.RawJSON(`{}`),
	}
}

func TestDepositImmediateTerminalStatusPersistsOnce(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Deposit)
	registerDepositStatusValidationTestActivities(env)

	tenantID := "tenant"
	clientReference := "immediate-terminal-ref"
	walletID := uuid.New()
	intent := depositStatusTestIntent(tenantID, clientReference, walletID, 10)
	txn := &walletstore.PSPTransaction{
		ID:               10,
		TenantID:         tenantID,
		PSPProvider:      "pay",
		IdempotencyKey:   intent.IdempotencyKey,
		PSPTransactionID: sql.NullString{String: "provider-deposit-id", Valid: true},
		ClientReference:  clientReference,
		Direction:        "inbound",
		Amount:           2500,
		Currency:         "AED",
		CurrencyUnitID:   intent.CurrencyUnitID,
		Status:           walletstore.PSPStatusPending,
		WorkflowID:       sql.NullString{String: intent.WorkflowID, Valid: true},
		DepositIntentID:  sql.NullInt64{Int64: intent.ID, Valid: true},
	}
	env.OnActivity(string(walletactivity.ActivityGetDepositIntentByReference), mock.Anything, tenantID, clientReference).Return(intent, nil).Once()
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).Return(txn, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateDeposit), mock.Anything, mock.Anything).Return(&walletvalidation.DepositValidationResult{
		WalletID:       walletID,
		Currency:       "AED",
		CurrencyUnitID: intent.CurrencyUnitID,
		Amount:         2500,
	}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityCreateDeposit), mock.Anything, mock.Anything).Return(&walletpsp.DepositResult{
		ClientReference: clientReference,
		ProviderTxID:    "provider-deposit-id",
		Amount:          2500,
		Currency:        "AED",
		Status:          walletstore.PSPStatusFailed,
		RawResponse: map[string]any{
			"transaction_id": "provider-deposit-id",
			"amount":         2500,
			"currency":       "AED",
			"status":         walletstore.PSPStatusFailed,
		},
	}, nil).Once()
	stored := *txn
	stored.Status = walletstore.PSPStatusFailed
	stored.RawResponse = walletstore.RawJSON(`{"transaction_id":"provider-deposit-id","amount":2500,"currency":"AED","status":"failed"}`)
	env.OnActivity(string(walletactivity.ActivityUpdatePSPTransactionStatus), mock.Anything, mock.MatchedBy(func(params walletactivity.UpdatePSPTransactionStatusParams) bool {
		return params.TenantID == tenantID && params.ClientReference == clientReference && params.Update.Status == walletstore.PSPStatusFailed && params.WorkflowSignal == nil
	})).Return(&stored, nil).Once()

	env.ExecuteWorkflow(Deposit, DepositParams{
		TenantID:        tenantID,
		IntentReference: clientReference,
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("deposit workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestDepositPersistsUnknownDispatchAndAwaitsReconciliation(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Deposit)
	registerDepositStatusValidationTestActivities(env)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.AcknowledgePSPWorkflowSignalParams) (*walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityAcknowledgePSPWorkflowSignal)},
	)

	tenantID := "tenant"
	clientReference := "unknown-dispatch-ref"
	walletID := uuid.New()
	intent := depositStatusTestIntent(tenantID, clientReference, walletID, 13)
	pending := &walletstore.PSPTransaction{
		ID: 13, TenantID: tenantID, PSPProvider: "pay", IdempotencyKey: intent.IdempotencyKey,
		ClientReference: clientReference, Direction: "inbound", Amount: intent.Amount, Currency: intent.Currency,
		CurrencyUnitID: intent.CurrencyUnitID,
		Status:         walletstore.PSPStatusInitiated, WorkflowID: sql.NullString{String: intent.WorkflowID, Valid: true},
		DepositIntentID: sql.NullInt64{Int64: intent.ID, Valid: true},
	}
	signal := walletstore.PSPWorkflowSignal{
		ProviderTxID: "provider-after-reconcile", Amount: intent.Amount, Currency: intent.Currency,
		Status: walletstore.PSPStatusFailed, RawResponse: walletstore.RawJSON(`{"status":"failed"}`),
	}
	payload, err := json.Marshal(signal)
	if err != nil {
		t.Fatal(err)
	}
	reconciled := *pending
	reconciled.Status = walletstore.PSPStatusFailed
	reconciled.PSPTransactionID = sql.NullString{String: signal.ProviderTxID, Valid: true}
	reconciled.WorkflowSignalPayload = walletstore.RawJSON(payload)

	env.OnActivity(string(walletactivity.ActivityGetDepositIntentByReference), mock.Anything, tenantID, clientReference).Return(intent, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateDeposit), mock.Anything, mock.Anything).Return(&walletvalidation.DepositValidationResult{
		WalletID: walletID, Currency: intent.Currency, CurrencyUnitID: intent.CurrencyUnitID, Amount: intent.Amount,
	}, nil).Once()
	limitUsage := walletstore.LimitUsageParams{
		TenantID: tenantID, CommandID: "deposit:" + clientReference, WalletID: walletID,
		TransactionType: "deposit", Currency: intent.Currency, Amount: intent.Amount,
	}
	env.OnActivity(string(walletactivity.ActivityReserveLimitUsage), mock.Anything, limitUsage).
		Return(&walletstore.LimitUsageReservation{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).Return(pending, nil).Once()
	env.OnActivity(string(walletactivity.ActivityCreateDeposit), mock.Anything, mock.Anything).
		Return(nil, walletpsp.ErrPSPTemporary).Times(3)
	env.OnActivity(string(walletactivity.ActivityUpdatePSPTransactionStatus), mock.Anything, mock.MatchedBy(func(params walletactivity.UpdatePSPTransactionStatusParams) bool {
		var audit map[string]any
		if err := json.Unmarshal(params.Update.RawResponse, &audit); err != nil {
			return false
		}
		return params.Update.Status == walletstore.PSPStatusProcessing && audit["dispatch_outcome"] == "unknown"
	})).Return(&reconciled, nil).Once()
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.Anything).Return(&reconciled, nil).Once()
	env.OnActivity(string(walletactivity.ActivityReleaseLimitUsage), mock.Anything, limitUsage).Return(nil).Once()

	env.ExecuteWorkflow(Deposit, DepositParams{TenantID: tenantID, IntentReference: clientReference})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("deposit workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestDepositConsumesPersistedWebhookTerminalWithoutStatusRewrite(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Deposit)
	registerDepositStatusValidationTestActivities(env)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.AcknowledgePSPWorkflowSignalParams) (*walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityAcknowledgePSPWorkflowSignal)},
	)

	tenantID := "tenant"
	clientReference := "persisted-webhook-terminal-ref"
	walletID := uuid.New()
	intent := depositStatusTestIntent(tenantID, clientReference, walletID, 11)
	pending := &walletstore.PSPTransaction{
		ID:              11,
		TenantID:        tenantID,
		PSPProvider:     "pay",
		IdempotencyKey:  intent.IdempotencyKey,
		ClientReference: clientReference,
		Direction:       "inbound",
		Amount:          2500,
		Currency:        "AED",
		CurrencyUnitID:  intent.CurrencyUnitID,
		Status:          walletstore.PSPStatusPending,
		WorkflowID:      sql.NullString{String: intent.WorkflowID, Valid: true},
		DepositIntentID: sql.NullInt64{Int64: intent.ID, Valid: true},
	}
	signal := walletstore.PSPWorkflowSignal{
		ProviderTxID: "provider-deposit-id",
		Amount:       2500,
		Currency:     "AED",
		Status:       walletstore.PSPStatusFailed,
		RawResponse:  walletstore.RawJSON(`{"status":"failed"}`),
	}
	payload, err := json.Marshal(signal)
	if err != nil {
		t.Fatalf("encode workflow signal: %v", err)
	}
	terminal := *pending
	terminal.Status = walletstore.PSPStatusFailed
	terminal.PSPTransactionID = sql.NullString{String: signal.ProviderTxID, Valid: true}
	terminal.WorkflowSignalPayload = walletstore.RawJSON(payload)

	env.OnActivity(string(walletactivity.ActivityGetDepositIntentByReference), mock.Anything, tenantID, clientReference).Return(intent, nil).Once()
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).Return(pending, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateDeposit), mock.Anything, mock.Anything).Return(&walletvalidation.DepositValidationResult{
		WalletID:       walletID,
		Currency:       "AED",
		CurrencyUnitID: intent.CurrencyUnitID,
		Amount:         2500,
	}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityCreateDeposit), mock.Anything, mock.Anything).Return(&walletpsp.DepositResult{
		ClientReference: clientReference,
		ProviderTxID:    signal.ProviderTxID,
		Amount:          2500,
		Currency:        "AED",
		Status:          walletstore.PSPStatusPending,
	}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityUpdatePSPTransactionStatus), mock.Anything, mock.Anything).Return(&terminal, nil).Once()
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.MatchedBy(func(params walletactivity.AcknowledgePSPWorkflowSignalParams) bool {
		return params.TenantID == tenantID && params.ClientReference == clientReference && params.LockToken == ""
	})).Return(&terminal, nil).Once()

	env.ExecuteWorkflow(Deposit, DepositParams{
		TenantID:        tenantID,
		IntentReference: clientReference,
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("deposit workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestDepositAcknowledgesWebhookTerminalThatWinsProviderUpdateRace(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Deposit)
	registerDepositStatusValidationTestActivities(env)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.AcknowledgePSPWorkflowSignalParams) (*walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityAcknowledgePSPWorkflowSignal)},
	)

	tenantID := "tenant"
	clientReference := "provider-webhook-race-ref"
	walletID := uuid.New()
	intent := depositStatusTestIntent(tenantID, clientReference, walletID, 12)
	pending := &walletstore.PSPTransaction{
		ID:               12,
		TenantID:         tenantID,
		PSPProvider:      "pay",
		IdempotencyKey:   intent.IdempotencyKey,
		PSPTransactionID: sql.NullString{String: "provider-deposit-id", Valid: true},
		ClientReference:  clientReference,
		Direction:        "inbound",
		Amount:           2500,
		Currency:         "AED",
		CurrencyUnitID:   intent.CurrencyUnitID,
		Status:           walletstore.PSPStatusPending,
		WorkflowID:       sql.NullString{String: intent.WorkflowID, Valid: true},
		DepositIntentID:  sql.NullInt64{Int64: intent.ID, Valid: true},
	}
	signal := walletstore.PSPWorkflowSignal{
		ProviderTxID: "provider-deposit-id",
		Amount:       2500,
		Currency:     "AED",
		Status:       walletstore.PSPStatusFailed,
		RawResponse:  walletstore.RawJSON(`{"status":"failed","source":"webhook"}`),
	}
	payload, err := json.Marshal(signal)
	if err != nil {
		t.Fatalf("encode workflow signal: %v", err)
	}
	webhookTerminal := *pending
	webhookTerminal.Status = walletstore.PSPStatusFailed
	webhookTerminal.WorkflowSignalPayload = walletstore.RawJSON(payload)

	env.OnActivity(string(walletactivity.ActivityGetDepositIntentByReference), mock.Anything, tenantID, clientReference).Return(intent, nil).Once()
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).Return(pending, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateDeposit), mock.Anything, mock.Anything).Return(&walletvalidation.DepositValidationResult{
		WalletID:       walletID,
		Currency:       "AED",
		CurrencyUnitID: intent.CurrencyUnitID,
		Amount:         2500,
	}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityCreateDeposit), mock.Anything, mock.Anything).Return(&walletpsp.DepositResult{
		ClientReference: clientReference,
		ProviderTxID:    "provider-deposit-id",
		Amount:          2500,
		Currency:        "AED",
		Status:          walletstore.PSPStatusPending,
	}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityUpdatePSPTransactionStatus), mock.Anything, mock.MatchedBy(func(params walletactivity.UpdatePSPTransactionStatusParams) bool {
		return params.Update.Status == walletstore.PSPStatusPending && params.WorkflowSignal == nil
	})).Return(&webhookTerminal, nil).Once()
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.MatchedBy(func(params walletactivity.AcknowledgePSPWorkflowSignalParams) bool {
		return params.TenantID == tenantID && params.ClientReference == clientReference && params.LockToken == ""
	})).Return(&webhookTerminal, nil).Once()

	env.ExecuteWorkflow(Deposit, DepositParams{
		TenantID:        tenantID,
		IntentReference: clientReference,
	})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("deposit workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestAwaitTerminalPSPStatusReceivesSignal(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitForPSPStatusTestWorkflow)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.AcknowledgePSPWorkflowSignalParams) (*walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityAcknowledgePSPWorkflowSignal)},
	)
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.MatchedBy(func(params walletactivity.AcknowledgePSPWorkflowSignalParams) bool {
		return params.TenantID == "tenant" && params.ClientReference == "client-ref" && params.LockToken == "" && !params.DeliveredAt.IsZero()
	})).Return(&walletstore.PSPTransaction{
		TenantID: "tenant", ClientReference: "client-ref", Status: walletstore.PSPStatusPending,
	}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.MatchedBy(func(params walletactivity.AcknowledgePSPWorkflowSignalParams) bool {
		return params.TenantID == "tenant" && params.ClientReference == "client-ref" && params.LockToken == "" && !params.DeliveredAt.IsZero()
	})).Return(&walletstore.PSPTransaction{
		TenantID: "tenant", ClientReference: "client-ref", Status: walletstore.PSPStatusSuccess,
		PSPTransactionID: sql.NullString{String: "provider-psp-id", Valid: true},
		Amount:           2500, Currency: "AED",
		WorkflowSignalPayload: walletstore.RawJSON(`{"provider_transaction_id":"provider-psp-id","amount":2500,"currency":"AED","status":"success","raw_response":{"status":"success"}}`),
	}, nil).Once()
	env.RegisterDelayedCallback(func() {
		signal := walletstore.PSPWorkflowSignal{
			ProviderTxID: "provider-psp-id",
			Amount:       2500,
			Currency:     "AED",
			Status:       "success",
			RawResponse:  walletstore.RawJSON(`{"status":"success"}`),
		}
		env.SignalWorkflow(PSPStatusUpdateSignal, signal)
		env.SignalWorkflow(PSPStatusUpdateSignal, signal)
	}, time.Second)

	env.ExecuteWorkflow(waitForPSPStatusTestWorkflow)

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow to complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("expected no workflow error, got %v", err)
	}
	var status walletpsp.TxStatus
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if status.Status != "success" || status.ProviderTxID != "provider-psp-id" || status.Amount != 2500 || status.Currency != "AED" {
		t.Fatalf("unexpected terminal status: %+v", status)
	}
}

func waitForPSPStatusTestWorkflow(ctx workflow.Context) (walletpsp.TxStatus, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Second})
	return awaitTerminalPSPStatus(ctx, "tenant", "client-ref", walletpsp.TxStatus{Status: "pending"}, false)
}

func TestPSPStatusPollerRetriesAckWithoutResignalingAfterLeaseExpiry(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerPSPPollerTestActivities(env)
	start := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	env.SetStartTime(start)

	signal := walletstore.PSPWorkflowSignal{
		ProviderTxID: "provider-id",
		Amount:       2500,
		Currency:     "AED",
		Status:       walletstore.PSPStatusSuccess,
		RawResponse:  walletstore.RawJSON(`{"status":"success"}`),
	}
	payload, err := json.Marshal(signal)
	if err != nil {
		t.Fatalf("encode signal: %v", err)
	}
	txn := walletstore.PSPTransaction{
		TenantID:              "tenant",
		ClientReference:       "webhook-terminal-ref",
		Status:                walletstore.PSPStatusSuccess,
		WorkflowID:            sql.NullString{String: "target-workflow", Valid: true},
		WorkflowSignalPayload: walletstore.RawJSON(payload),
	}

	env.OnActivity(string(walletactivity.ActivityExpireHolds), mock.Anything, txn.TenantID, 10).Return(0, nil).Once()
	env.OnActivity(string(walletactivity.ActivityListPSPTransactionsForPolling), mock.Anything, txn.TenantID, 10).Return([]walletstore.PSPTransaction{txn}, nil).Once()
	var lockToken string
	env.OnActivity(string(walletactivity.ActivityTryAcquirePSPTransactionLock), mock.Anything, mock.MatchedBy(func(params walletactivity.TryAcquirePSPTransactionLockParams) bool {
		lockToken = params.LockToken
		return params.TenantID == txn.TenantID && params.ClientReference == txn.ClientReference && params.LockExpiresAt.Equal(start.Add(time.Second))
	})).Return(true, nil).Once()
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, txn.TenantID, txn.ClientReference).Return(&txn, nil).Once()
	env.OnSignalExternalWorkflow(mock.Anything, txn.WorkflowID.String, "", PSPStatusUpdateSignal, signal).Return(nil).Once()
	ackMatches := func(params walletactivity.AcknowledgePSPWorkflowSignalParams) bool {
		return params.TenantID == txn.TenantID && params.ClientReference == txn.ClientReference && params.LockToken == lockToken && params.DeliveredAt.Equal(start)
	}
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.MatchedBy(ackMatches)).Return(nil, errors.New("database unavailable")).Once()
	acked := txn
	acked.WorkflowSignalDeliveredAt = sql.NullTime{Time: start, Valid: true}
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.MatchedBy(ackMatches)).Return(&acked, nil).Once()

	env.ExecuteWorkflow(PSPStatusPoller, PSPStatusPollerParams{TenantID: txn.TenantID, Limit: 10, PollIntervalSeconds: 1})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("poller workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestPSPStatusPollerResolvesMissingProviderIDAndQueuesTerminalStatusBeforeDelivery(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerPSPPollerTestActivities(env)
	start := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	env.SetStartTime(start)

	txn := walletstore.PSPTransaction{
		TenantID:        "tenant",
		PSPProvider:     "provider",
		IdempotencyKey:  "polled-terminal-idem",
		ClientReference: "polled-terminal-ref",
		Direction:       "inbound",
		Amount:          2500,
		Currency:        "AED",
		CurrencyUnitID:  101,
		Status:          walletstore.PSPStatusHeld,
		WorkflowID:      sql.NullString{String: "target-workflow", Valid: true},
	}
	providerStatus := walletpsp.TxStatus{
		ProviderTxID: "provider-id",
		Amount:       2500,
		Currency:     "AED",
		Status:       walletstore.PSPStatusSuccess,
		RawResponse:  map[string]any{"status": "success"},
	}
	expectedSignal, err := pspWorkflowSignalFromStatus(providerStatus)
	if err != nil {
		t.Fatalf("build expected signal: %v", err)
	}
	payload, err := json.Marshal(expectedSignal)
	if err != nil {
		t.Fatalf("encode expected signal: %v", err)
	}

	env.OnActivity(string(walletactivity.ActivityExpireHolds), mock.Anything, txn.TenantID, 10).Return(0, nil).Once()
	env.OnActivity(string(walletactivity.ActivityListPSPTransactionsForPolling), mock.Anything, txn.TenantID, 10).Return([]walletstore.PSPTransaction{txn}, nil).Once()
	var lockToken string
	env.OnActivity(string(walletactivity.ActivityTryAcquirePSPTransactionLock), mock.Anything, mock.MatchedBy(func(params walletactivity.TryAcquirePSPTransactionLockParams) bool {
		lockToken = params.LockToken
		return params.TenantID == txn.TenantID && params.ClientReference == txn.ClientReference
	})).Return(true, nil).Once()
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, txn.TenantID, txn.ClientReference).Return(&txn, nil).Once()
	env.OnActivity(string(walletactivity.ActivityGetTransactionStatus), mock.Anything, mock.MatchedBy(func(params walletactivity.GetStatusParams) bool {
		return params.TransactionID == "" && params.IdempotencyKey == txn.IdempotencyKey &&
			params.ClientReference == txn.ClientReference && params.Amount == txn.Amount &&
			params.Currency == txn.Currency && params.CurrencyUnitID == txn.CurrencyUnitID
	})).Return(&providerStatus, nil).Once()
	stored := txn
	stored.Status = walletstore.PSPStatusSuccess
	stored.WorkflowSignalPayload = walletstore.RawJSON(payload)
	env.OnActivity(string(walletactivity.ActivityUpdatePSPTransactionStatus), mock.Anything, mock.MatchedBy(func(params walletactivity.UpdatePSPTransactionStatusParams) bool {
		return params.TenantID == txn.TenantID &&
			params.ClientReference == txn.ClientReference &&
			params.Update.Status == walletstore.PSPStatusSuccess &&
			params.Update.LockToken.Valid && params.Update.LockToken.String == lockToken &&
			reflect.DeepEqual(params.WorkflowSignal, expectedSignal)
	})).Return(&stored, nil).Once()
	env.OnSignalExternalWorkflow(mock.Anything, txn.WorkflowID.String, "", PSPStatusUpdateSignal, *expectedSignal).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.MatchedBy(func(params walletactivity.AcknowledgePSPWorkflowSignalParams) bool {
		return params.TenantID == txn.TenantID && params.ClientReference == txn.ClientReference && params.LockToken == lockToken
	})).Return(&stored, nil).Once()

	env.ExecuteWorkflow(PSPStatusPoller, PSPStatusPollerParams{TenantID: txn.TenantID, Limit: 10, PollIntervalSeconds: 30})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("poller workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestPSPStatusPollerIsolatesUndeliverableTarget(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerPSPPollerTestActivities(env)

	makePending := func(clientReference, workflowID string) walletstore.PSPTransaction {
		signal := walletstore.PSPWorkflowSignal{Status: walletstore.PSPStatusFailed}
		payload, err := json.Marshal(signal)
		if err != nil {
			t.Fatalf("encode %s signal: %v", clientReference, err)
		}
		return walletstore.PSPTransaction{
			TenantID:              "tenant",
			ClientReference:       clientReference,
			Status:                walletstore.PSPStatusFailed,
			WorkflowID:            sql.NullString{String: workflowID, Valid: true},
			WorkflowSignalPayload: walletstore.RawJSON(payload),
		}
	}
	poison := makePending("poison-ref", "closed-workflow")
	healthy := makePending("healthy-ref", "waiting-workflow")
	poisonSignal, err := walletstore.ParsePSPWorkflowSignal(poison.WorkflowSignalPayload)
	if err != nil {
		t.Fatalf("parse poison signal: %v", err)
	}
	healthySignal, err := walletstore.ParsePSPWorkflowSignal(healthy.WorkflowSignalPayload)
	if err != nil {
		t.Fatalf("parse healthy signal: %v", err)
	}

	env.OnActivity(string(walletactivity.ActivityExpireHolds), mock.Anything, "tenant", 10).Return(0, nil).Once()
	env.OnActivity(string(walletactivity.ActivityListPSPTransactionsForPolling), mock.Anything, "tenant", 10).Return([]walletstore.PSPTransaction{poison, healthy}, nil).Once()
	var healthyLockToken string
	env.OnActivity(string(walletactivity.ActivityTryAcquirePSPTransactionLock), mock.Anything, mock.MatchedBy(func(params walletactivity.TryAcquirePSPTransactionLockParams) bool {
		return params.ClientReference == poison.ClientReference
	})).Return(true, nil).Once()
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, poison.TenantID, poison.ClientReference).Return(&poison, nil).Twice()
	env.OnSignalExternalWorkflow(mock.Anything, poison.WorkflowID.String, "", PSPStatusUpdateSignal, poisonSignal).Return(errors.New("target closed")).Once()
	env.OnActivity(string(walletactivity.ActivityTryAcquirePSPTransactionLock), mock.Anything, mock.MatchedBy(func(params walletactivity.TryAcquirePSPTransactionLockParams) bool {
		healthyLockToken = params.LockToken
		return params.ClientReference == healthy.ClientReference
	})).Return(true, nil).Once()
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, healthy.TenantID, healthy.ClientReference).Return(&healthy, nil).Once()
	env.OnSignalExternalWorkflow(mock.Anything, healthy.WorkflowID.String, "", PSPStatusUpdateSignal, healthySignal).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.MatchedBy(func(params walletactivity.AcknowledgePSPWorkflowSignalParams) bool {
		return params.ClientReference == healthy.ClientReference && params.LockToken == healthyLockToken
	})).Return(&healthy, nil).Once()

	env.ExecuteWorkflow(PSPStatusPoller, PSPStatusPollerParams{TenantID: "tenant", Limit: 10, PollIntervalSeconds: 30})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("poller workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestPSPStatusPollerReloadsClaimBeforeSignaling(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerPSPPollerTestActivities(env)

	signal := walletstore.PSPWorkflowSignal{Status: walletstore.PSPStatusFailed}
	payload, err := json.Marshal(signal)
	if err != nil {
		t.Fatalf("encode signal: %v", err)
	}
	listed := walletstore.PSPTransaction{
		TenantID:              "tenant",
		ClientReference:       "stale-listed-ref",
		Status:                walletstore.PSPStatusFailed,
		WorkflowID:            sql.NullString{String: "completed-workflow", Valid: true},
		WorkflowSignalPayload: walletstore.RawJSON(payload),
	}
	acknowledged := listed
	acknowledged.WorkflowSignalDeliveredAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}

	env.OnActivity(string(walletactivity.ActivityExpireHolds), mock.Anything, listed.TenantID, 10).Return(0, nil).Once()
	env.OnActivity(string(walletactivity.ActivityListPSPTransactionsForPolling), mock.Anything, listed.TenantID, 10).Return([]walletstore.PSPTransaction{listed}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityTryAcquirePSPTransactionLock), mock.Anything, mock.Anything).Return(true, nil).Once()
	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, listed.TenantID, listed.ClientReference).Return(&acknowledged, nil).Once()

	env.ExecuteWorkflow(PSPStatusPoller, PSPStatusPollerParams{TenantID: listed.TenantID, Limit: 10, PollIntervalSeconds: 30})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("poller workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func registerPSPPollerTestActivities(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterActivityWithOptions(
		func(context.Context, string, string) (*walletstore.PSPTransaction, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityGetPSPTransactionByReference)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, int) (int, error) { return 0, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityExpireHolds)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, int) ([]walletstore.PSPTransaction, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityListPSPTransactionsForPolling)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.TryAcquirePSPTransactionLockParams) (bool, error) {
			return false, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityTryAcquirePSPTransactionLock)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.GetStatusParams) (*walletpsp.TxStatus, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityGetTransactionStatus)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.UpdatePSPTransactionStatusParams) (*walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityUpdatePSPTransactionStatus)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.AcknowledgePSPWorkflowSignalParams) (*walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityAcknowledgePSPWorkflowSignal)},
	)
	env.RegisterWorkflow(PSPStatusPoller)
}
