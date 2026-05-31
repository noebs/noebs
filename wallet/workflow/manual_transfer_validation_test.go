package workflow

import (
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestValidateManualTransferDecision(t *testing.T) {
	decision := ManualTransferDecision{ApproverID: 10}
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

func TestAwaitManualTransferDecisionIgnoresRequesterSignals(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitForManualTransferDecisionTestWorkflow)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ManualTransferDecisionSignal, ManualTransferDecision{
			Approved:       true,
			ApproverID:     0,
			ProofOfPayment: "missing approver",
		})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ManualTransferDecisionSignal, ManualTransferDecision{
			Approved:       true,
			ApproverID:     10,
			ProofOfPayment: "requester approval",
		})
	}, 2*time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ManualTransferDecisionSignal, ManualTransferDecision{
			Approved:       true,
			ApproverID:     11,
			ProofOfPayment: "maker-checker approval",
		})
	}, 3*time.Second)

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
	if decision.ApproverID != 11 || decision.ProofOfPayment != "maker-checker approval" {
		t.Fatalf("unexpected manual transfer decision: %+v", decision)
	}
}

func waitForManualTransferDecisionTestWorkflow(ctx workflow.Context) (ManualTransferDecision, error) {
	return awaitManualTransferDecision(ctx, ManualTransferParams{
		RequestedBy:            10,
		ApprovalTimeoutSeconds: 30,
	})
}
