package workflow

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestPSPStatusPollerExpiresOrphanedHoldsBeforePolling(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PSPStatusPoller)
	env.RegisterActivityWithOptions(
		func(context.Context, string, int) (int, error) { return 0, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityExpireHolds)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, int) ([]walletstore.PSPTransaction, error) { return nil, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityListPSPTransactionsForPolling)},
	)

	env.OnActivity(string(walletactivity.ActivityExpireHolds), mock.Anything, "tenant-a", 25).Return(3, nil).Once()
	env.OnActivity(string(walletactivity.ActivityListPSPTransactionsForPolling), mock.Anything, "tenant-a", 25).
		Return([]walletstore.PSPTransaction{}, nil).Once()

	env.ExecuteWorkflow(PSPStatusPoller, PSPStatusPollerParams{
		TenantID: "tenant-a", Limit: 25, PollIntervalSeconds: 30,
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("PSPStatusPoller() error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestApprovalWaitsDoNotOutliveHoldDeadline(t *testing.T) {
	tests := []struct {
		name       string
		workflowFn any
		signalName string
		decision   any
		want       string
	}{
		{
			name: "manual transfer", workflowFn: awaitManualTransferPastHoldTestWorkflow,
			signalName: ManualTransferDecisionSignal,
			decision:   ManualTransferDecision{Approved: true, DecidedByOperatorID: 12, ProofOfPayment: "late"},
			want:       ErrManualTransferTimedOut.Error(),
		},
		{
			name: "withdrawal", workflowFn: awaitWithdrawalPastHoldTestWorkflow,
			signalName: WithdrawalApprovalSignal,
			decision:   WithdrawalApprovalDecision{Approved: true, DecidedByOperatorID: 12, ProofOfPayment: "late"},
			want:       ErrWithdrawalApprovalTimedOut.Error(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(tc.workflowFn)
			env.RegisterActivityWithOptions(
				func(context.Context, walletstore.WorkflowDecisionKey) (walletstore.WorkflowDecisionLookup, error) {
					return walletstore.WorkflowDecisionLookup{}, nil
				},
				activity.RegisterOptions{Name: string(walletactivity.ActivityLookupWorkflowDecision)},
			)
			env.RegisterActivityWithOptions(
				func(context.Context, walletstore.WorkflowDecisionWindowClose) (walletstore.WorkflowDecisionLookup, error) {
					return walletstore.WorkflowDecisionLookup{}, nil
				},
				activity.RegisterOptions{Name: string(walletactivity.ActivityCloseWorkflowDecisionWindow)},
			)
			env.OnActivity(string(walletactivity.ActivityLookupWorkflowDecision), mock.Anything, mock.Anything).
				Return(walletstore.WorkflowDecisionLookup{}, nil).Once()
			env.OnActivity(string(walletactivity.ActivityCloseWorkflowDecisionWindow), mock.Anything, mock.Anything).
				Return(walletstore.WorkflowDecisionLookup{}, nil).Once()
			env.RegisterDelayedCallback(func() { env.SignalWorkflow(tc.signalName, tc.decision) }, 3*time.Second)
			env.ExecuteWorkflow(tc.workflowFn)
			if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("workflow error = %v, want %q", err, tc.want)
			}
			env.AssertExpectations(t)
		})
	}
}

func TestManualDebitReservationUsesApprovalDeadlineAndExpiresOnTimeout(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ManualTransfer)
	env.RegisterActivityWithOptions(
		func(context.Context, string, string) (*walletstore.ManualTransfer, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityGetManualTransferByWorkflow)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.AuditEvent) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityRecordAuditEvent)},
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
		func(context.Context, walletstore.WorkflowDecisionKey) (walletstore.WorkflowDecisionLookup, error) {
			return walletstore.WorkflowDecisionLookup{}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityLookupWorkflowDecision)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.WorkflowDecisionWindowClose) (walletstore.WorkflowDecisionLookup, error) {
			return walletstore.WorkflowDecisionLookup{}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityCloseWorkflowDecisionWindow)},
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
		func(context.Context, string, string, walletstore.ManualTransferStatusUpdate) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityUpdateManualTransferStatus)},
	)

	const (
		tenantID       = "tenant-a"
		transferID     = int64(41)
		holdID         = int64(73)
		approvalWindow = 2 * time.Second
	)
	workflowStartedAt := env.Now()
	env.OnActivity(
		string(walletactivity.ActivityGetManualTransferByWorkflow),
		mock.Anything,
		tenantID,
		"default-test-workflow-id",
	).Return(&walletstore.ManualTransfer{
		ID: transferID, TenantID: tenantID, WorkflowID: "default-test-workflow-id", IdempotencyKey: "manual-timeout",
		TransferType: walletstore.ManualTransferTypeDebit,
		WalletID:     sql.NullString{String: "79ca6a75-cc21-4c32-a4a8-45c0c12efef9", Valid: true},
		Amount:       100, Currency: "AED", CurrencyUnitID: 11,
		Reason: "cash adjustment", RequestedByOperatorID: 11,
		DecisionDeadlineAt: workflowStartedAt.Add(approvalWindow),
	}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityRecordAuditEvent), mock.Anything, mock.Anything).Return(nil).Once()
	var holdParams walletstore.HoldParams
	env.OnActivity(string(walletactivity.ActivityValidateHold), mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { holdParams = args.Get(1).(walletstore.HoldParams) }).Return(struct{}{}, nil).Once()
	createdHold := &walletstore.BalanceHold{ID: holdID, TenantID: tenantID, Status: walletstore.HoldStatusActive}
	env.OnActivity(string(walletactivity.ActivityCreateHold), mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { createdHold.ExpiresAt = args.Get(1).(walletstore.HoldParams).ExpiresAt }).
		Return(createdHold, nil).Once()
	env.OnActivity(string(walletactivity.ActivityLookupWorkflowDecision), mock.Anything, mock.Anything).
		Return(walletstore.WorkflowDecisionLookup{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityCloseWorkflowDecisionWindow), mock.Anything, mock.Anything).
		Return(walletstore.WorkflowDecisionLookup{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityValidateReleaseHold), mock.Anything, tenantID, holdID).Return(struct{}{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityReleaseHold), mock.Anything, tenantID, holdID).Return(nil).Once()
	env.OnActivity(string(walletactivity.ActivityUpdateManualTransferStatus), mock.Anything, tenantID, mock.Anything, mock.MatchedBy(func(update walletstore.ManualTransferStatusUpdate) bool {
		return update.Status == ManualTransferStatusRejected && update.RejectionReason.Valid && strings.Contains(update.RejectionReason.String, ErrManualTransferTimedOut.Error())
	})).Return(nil).Once()

	env.ExecuteWorkflow(ManualTransfer, ManualTransferParams{
		TenantID: tenantID, IdempotencyKey: "manual-timeout",
	})
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), ErrManualTransferTimedOut.Error()) {
		t.Fatalf("ManualTransfer() error = %v, want %v", err, ErrManualTransferTimedOut)
	}
	if got := holdParams.ExpiresAt.Sub(workflowStartedAt); got != approvalWindow {
		t.Fatalf("manual debit hold lifetime = %s, want approval window %s", got, approvalWindow)
	}
	env.AssertExpectations(t)
}

func awaitManualTransferPastHoldTestWorkflow(ctx workflow.Context) (ManualTransferDecision, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 5 * time.Second})
	return awaitManualTransferDecision(ctx, "tenant-a", 1, workflow.Now(ctx).Add(2*time.Second))
}

func awaitWithdrawalPastHoldTestWorkflow(ctx workflow.Context) (WithdrawalApprovalDecision, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 5 * time.Second})
	return awaitWithdrawalApproval(ctx, withdrawalExecutionParams{
		TenantID: "tenant-a", ApprovalTimeoutSeconds: 30,
	}, 1, workflow.Now(ctx).Add(2*time.Second))
}
