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
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestWithdrawalApprovalRejectionReturnsHoldReleaseError(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(Withdrawal)
	registerWithdrawalCompensationTestActivities(env)

	tenantID := "tenant"
	walletID := uuid.New()
	clientReference := "withdrawal-ref"
	holdID := int64(77)

	env.OnActivity(string(walletactivity.ActivityGetPSPTransactionByReference), mock.Anything, tenantID, clientReference).
		Return(&walletstore.PSPTransaction{
			ID:              10,
			TenantID:        tenantID,
			PSPProvider:     "pay",
			ClientReference: clientReference,
			Direction:       "outbound",
			Amount:          100,
			Currency:        "AED",
			Status:          "pending",
		}, nil)
	env.OnActivity(string(walletactivity.ActivityValidateWithdrawal), mock.Anything, mock.Anything).
		Return(&walletvalidation.WithdrawalValidationResult{
			WalletID:          walletID,
			Currency:          "AED",
			Amount:            100,
			TotalDebit:        100,
			PayoutAmount:      100,
			PayoutCurrency:    "AED",
			WalletDebitAmount: 100,
			WalletCurrency:    "AED",
		}, nil)
	env.OnActivity(string(walletactivity.ActivityResolveWithdrawalDestination), mock.Anything, tenantID, int64(55)).
		Return(&walletstore.WithdrawalDestination{
			ID:                 55,
			TenantID:           tenantID,
			WalletID:           walletID,
			DestinationType:    "bank_account",
			PSPProvider:        sql.NullString{String: "pay", Valid: true},
			DestinationDetails: []byte(`{"iban":"AE000000000000000000000"}`),
			Currency:           "AED",
			OwnershipStatus:    "verified",
			IsActive:           true,
		}, nil)
	env.OnActivity(string(walletactivity.ActivityValidateHold), mock.Anything, mock.Anything).
		Return(struct{}{}, nil)
	env.OnActivity(string(walletactivity.ActivityCreateHold), mock.Anything, mock.Anything).
		Return(&walletstore.BalanceHold{ID: holdID, TenantID: tenantID, WalletID: walletID, Status: "active"}, nil)
	env.OnActivity(string(walletactivity.ActivityRecordAuditEvent), mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(string(walletactivity.ActivityValidateReleaseHold), mock.Anything, tenantID, holdID).
		Return(struct{}{}, nil)
	env.OnActivity(string(walletactivity.ActivityReleaseHold), mock.Anything, tenantID, holdID).
		Return(temporal.NewNonRetryableApplicationError("release hold failed", "release_hold_failed", nil))

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(WithdrawalApprovalSignal, WithdrawalApprovalDecision{
			Approved:   false,
			ApproverID: 42,
			Reason:     "risk",
		})
	}, time.Second)

	env.ExecuteWorkflow(Withdrawal, WithdrawalParams{
		TenantID:               tenantID,
		ProviderCode:           "pay",
		WalletID:               walletID.String(),
		OwnerType:              "consumer",
		OwnerID:                "user-1",
		DestinationID:          55,
		ApprovalRequired:       true,
		ApprovalTimeoutSeconds: 60,
		HoldExpirySeconds:      3600,
		Request: walletpsp.PayoutRequest{
			ClientReference: clientReference,
			Amount:          100,
			Currency:        "AED",
		},
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
}
