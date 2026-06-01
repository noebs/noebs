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
				executeWorkflowExpectError(t, tc.workflow, tc.params(tenantCase.tenantID), tenantCase.wantErr)
			})
		}
	}
}

func TestDepositWorkflowValidatesRequestBeforeActivities(t *testing.T) {
	baseParams := func() DepositParams {
		return DepositParams{
			TenantID:        "tenant",
			ClientReference: "deposit-ref",
			WalletID:        uuid.NewString(),
			OwnerType:       "user",
			OwnerID:         "42",
		}
	}

	cases := []struct {
		name    string
		mutate  func(*DepositParams)
		wantErr error
	}{
		{
			name:    "missing-client-reference",
			mutate:  func(params *DepositParams) { params.ClientReference = "" },
			wantErr: walletstore.ErrMissingClientReference,
		},
		{
			name:    "missing-wallet-id",
			mutate:  func(params *DepositParams) { params.WalletID = "" },
			wantErr: walletstore.ErrMissingWalletID,
		},
		{
			name:    "invalid-wallet-id",
			mutate:  func(params *DepositParams) { params.WalletID = "not-a-uuid" },
			wantErr: walletstore.ErrMissingWalletID,
		},
		{
			name:    "missing-owner-type",
			mutate:  func(params *DepositParams) { params.OwnerType = "" },
			wantErr: walletstore.ErrMissingOwnerType,
		},
		{
			name:    "missing-owner-id",
			mutate:  func(params *DepositParams) { params.OwnerID = "" },
			wantErr: walletstore.ErrMissingOwnerID,
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

func TestP2PWorkflowValidatesRequestBeforeActivities(t *testing.T) {
	baseParams := func() P2PParams {
		return P2PParams{
			TenantID:       "tenant",
			IdempotencyKey: "p2p-idem",
			ReferenceID:    "p2p-ref",
			Currency:       "USD",
			FromWalletID:   uuid.NewString(),
			ToWalletID:     uuid.NewString(),
			Amount:         100,
			UserID:         42,
			WalletPIN:      "1234",
			TwoFACode:      "123456",
			FromOwnerType:  "user",
			FromOwnerID:    "1",
			ToOwnerType:    "user",
			ToOwnerID:      "2",
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
			name:    "missing-reference-id",
			mutate:  func(params *P2PParams) { params.ReferenceID = "" },
			wantErr: walletstore.ErrMissingReferenceID,
		},
		{
			name:    "missing-currency",
			mutate:  func(params *P2PParams) { params.Currency = "" },
			wantErr: walletstore.ErrMissingCurrency,
		},
		{
			name:    "missing-from-wallet-id",
			mutate:  func(params *P2PParams) { params.FromWalletID = "" },
			wantErr: walletstore.ErrMissingWalletID,
		},
		{
			name:    "invalid-to-wallet-id",
			mutate:  func(params *P2PParams) { params.ToWalletID = "not-a-uuid" },
			wantErr: walletstore.ErrMissingWalletID,
		},
		{
			name: "same-wallet",
			mutate: func(params *P2PParams) {
				walletID := uuid.NewString()
				params.FromWalletID = walletID
				params.ToWalletID = walletID
			},
			wantErr: walletstore.ErrInvalidWalletPair,
		},
		{
			name:    "invalid-amount",
			mutate:  func(params *P2PParams) { params.Amount = 0 },
			wantErr: walletstore.ErrInvalidAmount,
		},
		{
			name:    "missing-from-owner-type",
			mutate:  func(params *P2PParams) { params.FromOwnerType = "" },
			wantErr: walletstore.ErrMissingOwnerType,
		},
		{
			name:    "missing-to-owner-id",
			mutate:  func(params *P2PParams) { params.ToOwnerID = "" },
			wantErr: walletstore.ErrMissingOwnerID,
		},
		{
			name: "missing-pin",
			mutate: func(params *P2PParams) {
				params.RequirePIN = true
				params.WalletPIN = ""
			},
			wantErr: walletstore.ErrMissingWalletPIN,
		},
		{
			name: "missing-2fa-user",
			mutate: func(params *P2PParams) {
				params.Require2FA = true
				params.UserID = 0
			},
			wantErr: walletstore.ErrInvalidUserID,
		},
		{
			name: "missing-2fa-code",
			mutate: func(params *P2PParams) {
				params.Require2FA = true
				params.TwoFACode = ""
			},
			wantErr: walletstore.ErrMissingTwoFACode,
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
