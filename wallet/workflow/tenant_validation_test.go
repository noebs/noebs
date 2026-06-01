package workflow

import (
	"strings"
	"testing"
	"time"

	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"go.temporal.io/sdk/testsuite"
)

func TestWalletWorkflowsValidateTenantBeforeActivities(t *testing.T) {
	start := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	cases := []struct {
		name     string
		workflow any
		params   func(string) any
	}{
		{
			name:     "deposit",
			workflow: Deposit,
			params: func(tenantID string) any {
				return DepositParams{
					TenantID:        tenantID,
					ClientReference: "deposit-ref",
					WalletID:        uuid.NewString(),
					OwnerType:       "user",
					OwnerID:         "42",
				}
			},
		},
		{
			name:     "withdrawal",
			workflow: Withdrawal,
			params: func(tenantID string) any {
				return WithdrawalParams{
					TenantID:          tenantID,
					WalletID:          uuid.NewString(),
					OwnerType:         "user",
					OwnerID:           "42",
					DestinationID:     1,
					HoldExpirySeconds: 60,
					Request: walletpsp.PayoutRequest{
						ClientReference: "withdrawal-ref",
						Amount:          100,
						Currency:        "USD",
					},
				}
			},
		},
		{
			name:     "p2p",
			workflow: P2P,
			params: func(tenantID string) any {
				return P2PParams{
					TenantID:       tenantID,
					IdempotencyKey: "p2p-ref",
					Currency:       "USD",
					FromWalletID:   uuid.NewString(),
					ToWalletID:     uuid.NewString(),
					Amount:         100,
					ReferenceID:    "p2p-ref",
					FromOwnerType:  "user",
					FromOwnerID:    "1",
					ToOwnerType:    "user",
					ToOwnerID:      "2",
				}
			},
		},
		{
			name:     "manual-transfer",
			workflow: ManualTransfer,
			params: func(tenantID string) any {
				return ManualTransferParams{
					TenantID:               tenantID,
					IdempotencyKey:         "manual-ref",
					TransferType:           "manual_debit",
					WalletID:               uuid.NewString(),
					Amount:                 100,
					Currency:               "USD",
					Reason:                 "test",
					RequestedBy:            10,
					ApprovalTimeoutSeconds: 60,
				}
			},
		},
		{
			name:     "reconciliation",
			workflow: Reconciliation,
			params: func(tenantID string) any {
				return ReconciliationParams{
					TenantID:  tenantID,
					Status:    "pending",
					StartTime: start,
					EndTime:   end,
					Limit:     10,
				}
			},
		},
		{
			name:     "psp-status-poller",
			workflow: PSPStatusPoller,
			params: func(tenantID string) any {
				return PSPStatusPollerParams{
					TenantID: tenantID,
					Limit:    10,
				}
			},
		},
	}

	tenantCases := []struct {
		name     string
		tenantID string
		wantErr  error
	}{
		{name: "missing", tenantID: "", wantErr: walletstore.ErrMissingTenantID},
		{name: "blank", tenantID: "   ", wantErr: walletstore.ErrMissingTenantID},
		{name: "reserved", tenantID: "default", wantErr: walletstore.ErrInvalidTenantID},
	}

	for _, tc := range cases {
		for _, tenantCase := range tenantCases {
			t.Run(tc.name+"/"+tenantCase.name, func(t *testing.T) {
				var suite testsuite.WorkflowTestSuite
				env := suite.NewTestWorkflowEnvironment()
				env.RegisterWorkflow(tc.workflow)

				env.ExecuteWorkflow(tc.workflow, tc.params(tenantCase.tenantID))

				if !env.IsWorkflowCompleted() {
					t.Fatal("expected workflow to complete")
				}
				err := env.GetWorkflowError()
				if err == nil {
					t.Fatalf("workflow error = nil, want %v", tenantCase.wantErr)
				}
				if !strings.Contains(err.Error(), tenantCase.wantErr.Error()) {
					t.Fatalf("workflow error = %v, want %v", err, tenantCase.wantErr)
				}
			})
		}
	}
}
