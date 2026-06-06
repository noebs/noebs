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

func TestValidateDestinationVerificationDecision(t *testing.T) {
	if err := validateDestinationVerificationDecision(DestinationVerificationDecision{Verified: false, Reason: " \t "}); err != walletstore.ErrMissingReason {
		t.Fatalf("validateDestinationVerificationDecision(rejected) error = %v, want %v", err, walletstore.ErrMissingReason)
	}
	if err := validateDestinationVerificationDecision(DestinationVerificationDecision{Verified: true}); err != nil {
		t.Fatalf("validateDestinationVerificationDecision(verified) error = %v, want nil", err)
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
