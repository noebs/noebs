package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestPSPStatusSignalOnlyWakesPersistedStateLookup(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitForPersistedPSPStatusTestWorkflow)
	registerPSPSignalAuthorityTestActivities(env)

	pending := walletstore.PSPTransaction{
		TenantID:        "tenant",
		ClientReference: "client-ref",
		Status:          walletstore.PSPStatusPending,
	}
	terminal := terminalPSPTransaction(t, walletstore.PSPWorkflowSignal{
		ProviderTxID: "provider-id",
		Status:       walletstore.PSPStatusFailed,
	})
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.Anything).
		Return(&pending, nil).Once()
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.Anything).
		Return(&terminal, nil).Once()

	forged := walletstore.PSPWorkflowSignal{ProviderTxID: "forged", Status: walletstore.PSPStatusSuccess}
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(PSPStatusUpdateSignal, forged) }, time.Second)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(PSPStatusUpdateSignal, forged) }, 2*time.Second)

	env.ExecuteWorkflow(waitForPersistedPSPStatusTestWorkflow)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var status walletpsp.TxStatus
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if status.Status != walletstore.PSPStatusFailed || status.ProviderTxID != "provider-id" {
		t.Fatalf("status = %+v, want persisted provider failure", status)
	}
	env.AssertExpectations(t)
}

func TestPSPStatusSignalBodyCannotOverridePersistedTerminalFailure(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(waitForPersistedPSPStatusTestWorkflow)
	registerPSPSignalAuthorityTestActivities(env)

	terminal := terminalPSPTransaction(t, walletstore.PSPWorkflowSignal{
		ProviderTxID: "provider-id",
		Amount:       2500,
		Currency:     "AED",
		Status:       walletstore.PSPStatusFailed,
	})
	env.OnActivity(string(walletactivity.ActivityAcknowledgePSPWorkflowSignal), mock.Anything, mock.MatchedBy(func(params walletactivity.AcknowledgePSPWorkflowSignalParams) bool {
		return params.TenantID == "tenant" && params.ClientReference == "client-ref" && params.LockToken == "" && !params.DeliveredAt.IsZero()
	})).Return(&terminal, nil).Once()

	forged := walletstore.PSPWorkflowSignal{
		ProviderTxID: "attacker-transaction",
		Amount:       999_999,
		Currency:     "USD",
		Status:       walletstore.PSPStatusSuccess,
	}
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(PSPStatusUpdateSignal, forged) }, time.Second)

	env.ExecuteWorkflow(waitForPersistedPSPStatusTestWorkflow)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var status walletpsp.TxStatus
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if status.Status != walletstore.PSPStatusFailed || status.ProviderTxID != "provider-id" || status.Amount != 2500 || status.Currency != "AED" {
		t.Fatalf("status = %+v, want persisted terminal fact", status)
	}
	env.AssertExpectations(t)
}

func waitForPersistedPSPStatusTestWorkflow(ctx workflow.Context) (walletpsp.TxStatus, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Second})
	return awaitTerminalPSPStatus(ctx, "tenant", "client-ref", walletpsp.TxStatus{Status: walletstore.PSPStatusPending}, false)
}

func registerPSPSignalAuthorityTestActivities(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.AcknowledgePSPWorkflowSignalParams) (*walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityAcknowledgePSPWorkflowSignal)},
	)
}

func terminalPSPTransaction(t *testing.T, signal walletstore.PSPWorkflowSignal) walletstore.PSPTransaction {
	t.Helper()
	payload, err := json.Marshal(signal)
	if err != nil {
		t.Fatalf("marshal workflow signal: %v", err)
	}
	return walletstore.PSPTransaction{
		TenantID:              "tenant",
		ClientReference:       "client-ref",
		Status:                signal.Status,
		PSPTransactionID:      sql.NullString{String: signal.ProviderTxID, Valid: signal.ProviderTxID != ""},
		WorkflowSignalPayload: walletstore.RawJSON(payload),
	}
}
