package workflow

import (
	"database/sql"
	"testing"
	"time"

	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
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
