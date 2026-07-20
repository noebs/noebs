package workflow

import (
	"strings"
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
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
					IntentReference: "deposit-ref",
				}
			},
		},
		{
			name:     "withdrawal",
			workflow: Withdrawal,
			params: func(tenantID string) any {
				return WithdrawalParams{
					TenantID:        tenantID,
					ClientReference: "withdrawal-ref",
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
				}
			},
		},
		{
			name:     "manual-transfer",
			workflow: ManualTransfer,
			params: func(tenantID string) any {
				return ManualTransferParams{
					TenantID:       tenantID,
					IdempotencyKey: "manual-ref",
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
					TenantID:            tenantID,
					Limit:               10,
					PollIntervalSeconds: 300,
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
		{name: "noncanonical", tenantID: "   ", wantErr: walletstore.ErrInvalidTenantID},
		{name: "reserved", tenantID: "default", wantErr: walletstore.ErrInvalidTenantID},
	}

	for _, tc := range cases {
		for _, tenantCase := range tenantCases {
			t.Run(tc.name+"/"+tenantCase.name, func(t *testing.T) {
				executeWorkflowExpectError(t, tc.workflow, tc.params(tenantCase.tenantID), tenantCase.wantErr)
			})
		}
	}
}

func TestDepositWorkflowValidatesRequestBeforeActivities(t *testing.T) {
	baseParams := func() DepositParams {
		return DepositParams{
			TenantID:        "tenant",
			IntentReference: "deposit-ref",
		}
	}

	cases := []struct {
		name    string
		mutate  func(*DepositParams)
		wantErr error
	}{
		{
			name:    "missing-client-reference",
			mutate:  func(params *DepositParams) { params.IntentReference = "" },
			wantErr: walletstore.ErrMissingClientReference,
		},
		{
			name:    "blank-client-reference",
			mutate:  func(params *DepositParams) { params.IntentReference = " \t " },
			wantErr: walletstore.ErrMissingClientReference,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := baseParams()
			tc.mutate(&params)
			executeWorkflowExpectError(t, Deposit, params, tc.wantErr)
		})
	}
}

func TestWithdrawalWorkflowValidatesRequestBeforeActivities(t *testing.T) {
	baseParams := func() WithdrawalParams {
		return WithdrawalParams{
			TenantID:        "tenant",
			ClientReference: "withdrawal-ref",
		}
	}

	cases := []struct {
		name    string
		mutate  func(*WithdrawalParams)
		wantErr error
	}{
		{
			name:    "missing-client-reference",
			mutate:  func(params *WithdrawalParams) { params.ClientReference = "" },
			wantErr: walletstore.ErrMissingClientReference,
		},
		{
			name:    "blank-client-reference",
			mutate:  func(params *WithdrawalParams) { params.ClientReference = " \t " },
			wantErr: walletstore.ErrMissingClientReference,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := baseParams()
			tc.mutate(&params)
			executeWorkflowExpectError(t, Withdrawal, params, tc.wantErr)
		})
	}
}

func TestP2PWorkflowValidatesRequestBeforeActivities(t *testing.T) {
	baseParams := func() P2PParams {
		return P2PParams{
			TenantID:       "tenant",
			IdempotencyKey: "p2p-idem",
		}
	}

	cases := []struct {
		name    string
		mutate  func(*P2PParams)
		wantErr error
	}{
		{
			name:    "missing-idempotency-key",
			mutate:  func(params *P2PParams) { params.IdempotencyKey = "" },
			wantErr: walletstore.ErrMissingIdempotencyKey,
		},
		{
			name:    "blank-idempotency-key",
			mutate:  func(params *P2PParams) { params.IdempotencyKey = " \t " },
			wantErr: walletstore.ErrMissingIdempotencyKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := baseParams()
			tc.mutate(&params)
			executeWorkflowExpectError(t, P2P, params, tc.wantErr)
		})
	}
}

func TestManualTransferWorkflowValidatesRequestBeforeActivities(t *testing.T) {
	baseParams := func() ManualTransferParams {
		return ManualTransferParams{
			TenantID:       "tenant",
			IdempotencyKey: "manual-ref",
		}
	}

	cases := []struct {
		name    string
		mutate  func(*ManualTransferParams)
		wantErr error
	}{
		{
			name:    "blank-idempotency-key",
			mutate:  func(params *ManualTransferParams) { params.IdempotencyKey = " \t " },
			wantErr: walletstore.ErrMissingIdempotencyKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := baseParams()
			tc.mutate(&params)
			executeWorkflowExpectError(t, ManualTransfer, params, tc.wantErr)
		})
	}
}

func TestReconciliationWorkflowRequiresExplicitRangeOrLookback(t *testing.T) {
	start := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	baseParams := func() ReconciliationParams {
		return ReconciliationParams{
			TenantID:  "tenant",
			Status:    "success",
			StartTime: start,
			EndTime:   end,
			Limit:     10,
		}
	}

	cases := []struct {
		name    string
		mutate  func(*ReconciliationParams)
		wantErr error
	}{
		{
			name: "missing-range-and-lookback",
			mutate: func(params *ReconciliationParams) {
				params.StartTime = time.Time{}
				params.EndTime = time.Time{}
			},
			wantErr: walletstore.ErrMissingStartTime,
		},
		{
			name:    "missing-start",
			mutate:  func(params *ReconciliationParams) { params.StartTime = time.Time{} },
			wantErr: walletstore.ErrMissingStartTime,
		},
		{
			name:    "missing-end",
			mutate:  func(params *ReconciliationParams) { params.EndTime = time.Time{} },
			wantErr: walletstore.ErrMissingEndTime,
		},
		{
			name:    "invalid-range",
			mutate:  func(params *ReconciliationParams) { params.StartTime = params.EndTime.Add(time.Hour) },
			wantErr: walletstore.ErrInvalidTimeRange,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := baseParams()
			tc.mutate(&params)
			executeWorkflowExpectError(t, Reconciliation, params, tc.wantErr)
		})
	}
}

func TestPSPStatusPollerRequiresPollIntervalBeforeActivities(t *testing.T) {
	executeWorkflowExpectError(t, PSPStatusPoller, PSPStatusPollerParams{
		TenantID: "tenant",
		Limit:    10,
	}, walletstore.ErrMissingStatusTimeout)
}

func executeWorkflowExpectError(t *testing.T, workflow any, params any, wantErr error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflow)

	env.ExecuteWorkflow(workflow, params)

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow to complete")
	}
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatalf("workflow error = nil, want %v", wantErr)
	}
	if !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("workflow error = %v, want %v", err, wantErr)
	}
}
