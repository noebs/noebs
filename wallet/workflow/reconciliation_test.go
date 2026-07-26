package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestReconciliationAuditUsesEffectiveLookbackRange(t *testing.T) {
	end := time.Date(2026, 7, 26, 9, 30, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartTime(end)
	env.RegisterWorkflow(Reconciliation)
	env.RegisterActivityWithOptions(
		func(context.Context, walletactivity.ListPSPTransactionsByStatusParams) ([]walletstore.PSPTransaction, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: string(walletactivity.ActivityListPSPTransactionsByStatus)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, string, string, string) (bool, error) { return false, nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityLedgerTransactionExistsByReference)},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, walletstore.AuditEvent) error { return nil },
		activity.RegisterOptions{Name: string(walletactivity.ActivityRecordAuditEvent)},
	)

	env.OnActivity(
		string(walletactivity.ActivityListPSPTransactionsByStatus),
		mock.Anything,
		mock.MatchedBy(func(params walletactivity.ListPSPTransactionsByStatusParams) bool {
			return params.TenantID == "tenant-a" &&
				params.Status == "confirmed" &&
				params.Start.Equal(start) &&
				params.End.Equal(end) &&
				params.Limit == 10
		}),
	).Return([]walletstore.PSPTransaction{{
		TenantID:        "tenant-a",
		ClientReference: "deposit-1",
		Direction:       "inbound",
	}}, nil).Once()
	env.OnActivity(
		string(walletactivity.ActivityLedgerTransactionExistsByReference),
		mock.Anything,
		"tenant-a",
		"deposit",
		"deposit-1",
	).Return(false, nil).Once()

	var audited walletstore.AuditEvent
	env.OnActivity(
		string(walletactivity.ActivityRecordAuditEvent),
		mock.Anything,
		mock.MatchedBy(func(event walletstore.AuditEvent) bool {
			return event.EventType == "wallet.reconciliation" && event.Action == "mismatch"
		}),
	).Run(func(arguments mock.Arguments) {
		audited = arguments.Get(1).(walletstore.AuditEvent)
	}).Return(nil).Once()

	env.ExecuteWorkflow(Reconciliation, ReconciliationParams{
		TenantID:      "tenant-a",
		Status:        "confirmed",
		Limit:         10,
		LookbackHours: 24,
	})
	workflowErr := env.GetWorkflowError()
	if workflowErr == nil || !strings.Contains(workflowErr.Error(), "reconciliation mismatch") {
		t.Fatalf("workflow error = %v, want reconciliation mismatch", workflowErr)
	}

	var metadata struct {
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
	}
	if err := json.Unmarshal(audited.Metadata, &metadata); err != nil {
		t.Fatalf("decode audit metadata: %v", err)
	}
	if metadata.StartTime != start.Format(time.RFC3339) || metadata.EndTime != end.Format(time.RFC3339) {
		t.Fatalf("audit range = %q to %q, want %q to %q", metadata.StartTime, metadata.EndTime, start.Format(time.RFC3339), end.Format(time.RFC3339))
	}
	env.AssertExpectations(t)
}
