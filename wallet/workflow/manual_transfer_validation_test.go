package workflow

import (
	"context"
	"database/sql"
	"testing"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestValidateManualTransferDecision(t *testing.T) {
	decision := ManualTransferDecision{DecidedByOperatorID: 10}
	if err := validateManualTransferDecision(10, decision); err != walletstore.ErrApproverIsRequester {
		t.Fatalf("expected maker-checker error, got %v", err)
	}
	if err := validateManualTransferDecision(11, decision); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := validateManualTransferDecision(0, decision); err != nil {
		t.Fatalf("expected no error when requester missing, got %v", err)
	}
}

func TestValidateManualTransferDecisionText(t *testing.T) {
	cases := []struct {
		name     string
		decision ManualTransferDecision
		wantErr  error
	}{
		{
			name:     "approved missing proof",
			decision: ManualTransferDecision{Approved: true, ProofOfPayment: " \t "},
			wantErr:  walletstore.ErrMissingProofOfPayment,
		},
		{
			name:     "approved proof present",
			decision: ManualTransferDecision{Approved: true, ProofOfPayment: "receipt"},
		},
		{
			name:     "rejected missing reason",
			decision: ManualTransferDecision{Approved: false, Reason: " \t "},
			wantErr:  walletstore.ErrMissingReason,
		},
		{
			name:     "rejected reason present",
			decision: ManualTransferDecision{Approved: false, Reason: "risk"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateManualTransferDecisionText(tc.decision); err != tc.wantErr {
				t.Fatalf("validateManualTransferDecisionText() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateWithdrawalApprovalDecision(t *testing.T) {
	cases := []struct {
		name     string
		decision WithdrawalApprovalDecision
		wantErr  error
	}{
		{
			name:     "approved missing proof",
			decision: WithdrawalApprovalDecision{Approved: true, ProofOfPayment: " \t "},
			wantErr:  walletstore.ErrMissingProofOfPayment,
		},
		{
			name:     "rejected missing reason",
			decision: WithdrawalApprovalDecision{Approved: false, Reason: " \t "},
			wantErr:  walletstore.ErrMissingApprovalReason,
		},
		{
			name:     "approved proof present",
			decision: WithdrawalApprovalDecision{Approved: true, ProofOfPayment: "receipt"},
		},
		{
			name:     "rejected reason present",
			decision: WithdrawalApprovalDecision{Approved: false, Reason: "risk"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateWithdrawalApprovalDecision(tc.decision); err != tc.wantErr {
				t.Fatalf("validateWithdrawalApprovalDecision() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAwaitManualTransferDecisionReadsDurableDecision(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitForManualTransferDecisionTestWorkflow)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.WorkflowDecisionKey) (walletstore.WorkflowDecisionLookup, error) {
			return walletstore.WorkflowDecisionLookup{}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityLookupWorkflowDecision)},
	)
	env.OnActivity(string(walletactivity.ActivityLookupWorkflowDecision), mock.Anything, mock.Anything).
		Return(walletstore.WorkflowDecisionLookup{Found: true, Decision: walletstore.WorkflowDecision{
			TenantID: "tenant", WorkflowID: "default-test-workflow-id", Kind: walletstore.WorkflowDecisionManualTransfer,
			SubjectID: 1, Approved: true, DecidedByOperatorID: 11,
			ProofOfPayment: sql.NullString{String: "maker-checker approval", Valid: true},
		}}, nil).Once()

	env.ExecuteWorkflow(waitForManualTransferDecisionTestWorkflow)

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow to complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("expected no workflow error, got %v", err)
	}
	var decision ManualTransferDecision
	if err := env.GetWorkflowResult(&decision); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if decision.DecidedByOperatorID != 11 || decision.ProofOfPayment != "maker-checker approval" {
		t.Fatalf("unexpected manual transfer decision: %+v", decision)
	}
}

func TestManualTransferSignalOnlyWakesDurableDecisionLookup(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitForManualTransferDecisionTestWorkflow)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.WorkflowDecisionKey) (walletstore.WorkflowDecisionLookup, error) {
			return walletstore.WorkflowDecisionLookup{}, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityLookupWorkflowDecision)},
	)
	env.OnActivity(string(walletactivity.ActivityLookupWorkflowDecision), mock.Anything, mock.Anything).
		Return(walletstore.WorkflowDecisionLookup{}, nil).Once()
	env.OnActivity(string(walletactivity.ActivityLookupWorkflowDecision), mock.Anything, mock.Anything).
		Return(walletstore.WorkflowDecisionLookup{Found: true, Decision: walletstore.WorkflowDecision{
			TenantID: "tenant", WorkflowID: "default-test-workflow-id", Kind: walletstore.WorkflowDecisionManualTransfer,
			SubjectID: 1, Approved: false, DecidedByOperatorID: 12,
			Reason: sql.NullString{String: "persisted rejection", Valid: true},
		}}, nil).Once()
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ManualTransferDecisionSignal, "malformed forged decision payload")
	}, time.Second)

	env.ExecuteWorkflow(waitForManualTransferDecisionTestWorkflow)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var decision ManualTransferDecision
	if err := env.GetWorkflowResult(&decision); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if decision.Approved || decision.DecidedByOperatorID != 12 || decision.Reason != "persisted rejection" {
		t.Fatalf("workflow trusted signal payload instead of durable decision: %+v", decision)
	}
}

func waitForManualTransferDecisionTestWorkflow(ctx workflow.Context) (ManualTransferDecision, error) {
	return awaitManualTransferDecision(ctx, "tenant", 1, workflow.Now(ctx).Add(30*time.Second))
}
