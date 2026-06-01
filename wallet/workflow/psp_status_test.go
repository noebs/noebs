package workflow

import (
	"context"
	"database/sql"
	"strings"
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

func TestStatusFromPSPTransactionParsesRawResponse(t *testing.T) {
	txn := &walletstore.PSPTransaction{
		Status:           "processing",
		PSPTransactionID: sql.NullString{String: "stored-psp-id", Valid: true},
		RawResponse:      walletstore.RawJSON(`{"status":"SUCCESS","amount":2500,"currency":"AED","transaction_id":"provider-psp-id"}`),
	}

	status := statusFromPSPTransaction(txn)
	if status.Status != "success" {
		t.Fatalf("expected normalized success status, got %q", status.Status)
	}
	if status.ProviderTxID != "provider-psp-id" {
		t.Fatalf("expected provider transaction id from raw response, got %q", status.ProviderTxID)
	}
	if status.Amount != 2500 {
		t.Fatalf("expected amount 2500, got %d", status.Amount)
	}
	if status.Currency != "AED" {
		t.Fatalf("expected currency AED, got %q", status.Currency)
	}
}

func TestStatusFromPSPTransactionPreservesNumericTransactionID(t *testing.T) {
	txn := &walletstore.PSPTransaction{
		Status:      "processing",
		RawResponse: walletstore.RawJSON(`{"status":"SUCCESS","amount":2500,"currency":"AED","transaction_id":2500}`),
	}

	status := statusFromPSPTransaction(txn)
	if status.ProviderTxID != "2500" {
		t.Fatalf("provider transaction id = %q, want 2500", status.ProviderTxID)
	}
	if status.Amount != 2500 {
		t.Fatalf("amount = %d, want 2500", status.Amount)
	}
}

func TestStatusFromPSPTransactionIgnoresFractionalRawAmount(t *testing.T) {
	txn := &walletstore.PSPTransaction{
		Status:      "processing",
		RawResponse: walletstore.RawJSON(`{"status":"SUCCESS","amount":12.5,"currency":"AED","transaction_id":"provider-psp-id"}`),
	}

	status := statusFromPSPTransaction(txn)
	if status.Amount != 0 {
		t.Fatalf("amount = %d, want 0 for invalid fractional minor-unit amount", status.Amount)
	}
}

func TestProviderStatusConversionsDoNotFallBackToStoredStatus(t *testing.T) {
	deposit := statusFromDepositVerification(walletpsp.DepositVerification{
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
		Status:       "",
	})
	if payout.Status != "" {
		t.Fatalf("payout status = %q, want empty", payout.Status)
	}
	if payout.ProviderTxID != "provider-payout-id" {
		t.Fatalf("payout provider id = %q", payout.ProviderTxID)
	}
}

func TestDepositRejectsInvalidSuccessfulProviderStatusBeforeStoreUpdate(t *testing.T) {
	cases := []struct {
		name    string
		status  walletpsp.DepositVerification
		wantErr error
	}{
		{
			name: "zero-amount",
			status: walletpsp.DepositVerification{
				ProviderTxID: "provider-deposit-id",
				Status:       "success",
				Amount:       0,
				Currency:     "AED",
			},
			wantErr: walletstore.ErrInvalidAmount,
		},
		{
			name: "missing-currency",
			status: walletpsp.DepositVerification{
				ProviderTxID: "provider-deposit-id",
				Status:       "success",
				Amount:       2500,
				Currency:     "",
			},
			wantErr: walletstore.ErrMissingCurrency,
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
			env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).
				Return(&walletstore.PSPTransaction{
					ID:               10,
					TenantID:         tenantID,
					PSPProvider:      "pay",
					PSPTransactionID: sql.NullString{String: "provider-deposit-id", Valid: true},
					ClientReference:  clientReference,
					Direction:        "inbound",
					Amount:           2500,
					Currency:         "AED",
					Status:           "pending",
				}, nil)
			env.OnActivity(string(walletactivity.ActivityValidateDeposit), mock.Anything, mock.Anything).
				Return(&walletvalidation.DepositValidationResult{
					WalletID:  walletID,
					Currency:  "AED",
					Amount:    2500,
					NetAmount: 2500,
				}, nil)
			env.OnActivity(string(walletactivity.ActivityVerifyDeposit), mock.Anything, mock.Anything).
				Return(&tc.status, nil)

			env.ExecuteWorkflow(Deposit, DepositParams{
				TenantID:        tenantID,
				ProviderCode:    "pay",
				ClientReference: clientReference,
				WalletID:        walletID.String(),
				OwnerType:       "user",
				OwnerID:         "42",
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
		func(context.Context, walletactivity.VerifyDepositParams) (*walletpsp.DepositVerification, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityVerifyDeposit)},
	)
}

func TestAwaitTerminalPSPStatusReceivesSignal(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitForPSPStatusTestWorkflow)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(PSPStatusUpdateSignal, walletpsp.TxStatus{
			ProviderTxID: "provider-psp-id",
			Amount:       2500,
			Currency:     "AED",
			Status:       "success",
			RawResponse:  map[string]any{"status": "success"},
		})
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
	return awaitTerminalPSPStatus(ctx, walletpsp.TxStatus{Status: "pending"})
}

func TestAwaitDestinationVerificationDecisionIgnoresUnrelatedSignals(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitForDestinationVerificationDecisionTestWorkflow)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(WithdrawalVerificationSignal, DestinationVerificationDecision{
			VerificationID: 41,
			Verified:       false,
			Reason:         "wrong verification",
		})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(WithdrawalVerificationSignal, DestinationVerificationDecision{
			VerificationID: 42,
			Verified:       true,
		})
	}, 2*time.Second)

	env.ExecuteWorkflow(waitForDestinationVerificationDecisionTestWorkflow)

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow to complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("expected no workflow error, got %v", err)
	}
	var decision DestinationVerificationDecision
	if err := env.GetWorkflowResult(&decision); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if decision.VerificationID != 42 || !decision.Verified {
		t.Fatalf("unexpected verification decision: %+v", decision)
	}
}

func waitForDestinationVerificationDecisionTestWorkflow(ctx workflow.Context) (DestinationVerificationDecision, error) {
	return awaitDestinationVerificationDecision(ctx, 42, 30)
}
