package walletgrpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet"
	walletgrpcmock "github.com/adonese/noebs/wallet/grpc/mock"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type stubWorkflowRun struct {
	id    string
	runID string
}

func (s stubWorkflowRun) GetID() string {
	return s.id
}

func (s stubWorkflowRun) GetRunID() string {
	return s.runID
}

func (s stubWorkflowRun) Get(ctx context.Context, valuePtr interface{}) error {
	return nil
}

func (s stubWorkflowRun) GetWithOptions(ctx context.Context, valuePtr interface{}, options client.WorkflowRunGetOptions) error {
	return nil
}

func TestSignalWithdrawalApprovalValidatesCommand(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	tests := []struct {
		name    string
		command withdrawalApprovalCommand
		want    error
	}{
		{name: "workflow", command: withdrawalApprovalCommand{OperatorID: 1}, want: walletstore.ErrMissingWorkflowID},
		{name: "operator", command: withdrawalApprovalCommand{WorkflowID: "wallet-withdrawal-tenant-ref"}, want: walletstore.ErrMissingApproverID},
		{name: "approval proof", command: withdrawalApprovalCommand{WorkflowID: "wallet-withdrawal-tenant-ref", OperatorID: 1, Approved: true}, want: walletstore.ErrMissingProofOfPayment},
		{name: "rejection reason", command: withdrawalApprovalCommand{WorkflowID: "wallet-withdrawal-tenant-ref", OperatorID: 1}, want: walletstore.ErrMissingApprovalReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := server.signalWithdrawalApproval(t.Context(), tt.command)
			if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != tt.want.Error() {
				t.Fatalf("error = %v, want invalid argument %q", err, tt.want)
			}
		})
	}
}

func TestRequestWithdrawalStartsWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres container: %v", err)
	}
	defer func() {
		_ = container.Terminate(context.Background())
	}()

	const dbName = "wallet_ledger"
	dbURL, err := container.CreateDatabaseForRole(ctx, dbName, "wallet_ledger_migrate")
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	db, err := store.OpenFromConfig(dbURL, store.DriverPostgres)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	}()

	tenantID := "tenant"
	if err := store.MigrateScope(ctx, db, store.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate wallet-ledger db: %v", err)
	}

	cfg := ebs_fields.NoebsConfig{
		WalletApprovalThreshold:      100,
		WalletApprovalTimeoutSeconds: 120,
		WalletHoldExpirySeconds:      3600,
	}
	svc := wallet.NewService(db, cfg)
	server := NewServer(svc)
	provisionWalletGRPCTestTenant(t, ctx, db, tenantID, "Withdrawal Tenant")
	walletRow, err := ensureUserWalletForTest(t, ctx, svc, tenantID, 42, "USD")
	if err != nil {
		t.Fatalf("ensure user wallet: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, walletRow.ID, 10_000, 10_000)
	operatorID := resolveWalletGRPCTestOperator(t, ctx, db, "withdrawal-validation")
	seedWalletValidationRules(t, ctx, db, tenantID, "noop", "USD", operatorID, true, true)

	ctrl := gomock.NewController(t)
	mockTemporal := walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalClient = mockTemporal
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}

	run := stubWorkflowRun{id: withdrawalWorkflowID(tenantID, "ref-2"), runID: "run-id"}
	workflowStarts := 0
	mockTemporal.EXPECT().
		ExecuteWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, opts client.StartWorkflowOptions, wf interface{}, args ...interface{}) (client.WorkflowRun, error) {
			workflowStarts++
			if opts.ID == "" {
				t.Fatalf("expected workflow id")
			}
			if opts.TaskQueue == "" {
				t.Fatalf("expected task queue")
			}
			if len(args) != 1 {
				t.Fatalf("expected params")
			}
			params, ok := args[0].(walletworkflow.WithdrawalParams)
			if !ok {
				t.Fatalf("expected withdrawal params")
			}
			if params.TenantID != tenantID || params.ClientReference != "ref-2" {
				t.Fatalf("workflow authority params = %+v", params)
			}
			if workflowStarts == 1 {
				return run, nil
			}
			return nil, serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "", run.runID)
		}).
		Times(2)

	allowReturn := true
	approvalRequired := true
	req := &walletv1.RequestWithdrawalRequest{
		TenantId:               "tenant",
		IdempotencyKey:         "withdrawal-ref-2",
		ClientReference:        "ref-2",
		ProviderCode:           "noop",
		WalletId:               walletRow.ID.String(),
		Amount:                 500,
		Currency:               "USD",
		OwnerType:              "user",
		OwnerId:                "42",
		AllowReturnToSource:    &allowReturn,
		HoldExpirySeconds:      int32(cfg.WalletHoldExpirySeconds),
		ApprovalRequired:       &approvalRequired,
		ApprovalTimeoutSeconds: int32(cfg.WalletApprovalTimeoutSeconds),
	}

	var before time.Time
	if err := db.GetContext(ctx, &before, "SELECT clock_timestamp()"); err != nil {
		t.Fatalf("read DB clock before withdrawal: %v", err)
	}
	resp, err := server.RequestWithdrawal(walletGatewayIdentityContext(42, "tenant"), req)
	if err != nil {
		t.Fatalf("request withdrawal: %v", err)
	}
	if resp.WorkflowId == "" {
		t.Fatalf("expected workflow id")
	}
	setWalletBalances(t, ctx, db, tenantID, walletRow.ID, 0, 0)
	replayed, err := server.RequestWithdrawal(walletGatewayIdentityContext(42, tenantID), proto.Clone(req).(*walletv1.RequestWithdrawalRequest))
	if err != nil {
		t.Fatalf("replay withdrawal after balance changed: %v", err)
	}
	if replayed.GetWorkflowId() != resp.GetWorkflowId() || replayed.GetRunId() != resp.GetRunId() {
		t.Fatalf("replayed workflow run = %q/%q, want %q/%q", replayed.GetWorkflowId(), replayed.GetRunId(), resp.GetWorkflowId(), resp.GetRunId())
	}

	stored, err := svc.Store.GetPSPTransactionByReference(ctx, req.TenantId, req.ClientReference)
	if err != nil {
		t.Fatalf("lookup transaction: %v", err)
	}
	if stored.Status != "initiated" {
		t.Fatalf("expected initiated status, got %s", stored.Status)
	}
	if stored.IdempotencyKey != req.IdempotencyKey {
		t.Fatalf("idempotency key = %q, want %q", stored.IdempotencyKey, req.IdempotencyKey)
	}
	if !stored.WorkflowID.Valid || stored.WorkflowID.String == "" {
		t.Fatalf("expected workflow id persisted")
	}
	var after time.Time
	if err := db.GetContext(ctx, &after, "SELECT clock_timestamp()"); err != nil {
		t.Fatalf("read DB clock after withdrawal: %v", err)
	}
	decisionWindow := time.Duration(cfg.WalletApprovalTimeoutSeconds) * time.Second
	if !stored.ApprovalTimeoutSeconds.Valid || stored.ApprovalTimeoutSeconds.Int64 != int64(cfg.WalletApprovalTimeoutSeconds) {
		t.Fatalf("approval timeout = %+v, want %d", stored.ApprovalTimeoutSeconds, cfg.WalletApprovalTimeoutSeconds)
	}
	if !stored.DecisionDeadlineAt.Valid || stored.DecisionDeadlineAt.Time.Before(before.Add(decisionWindow)) || stored.DecisionDeadlineAt.Time.After(after.Add(decisionWindow)) {
		t.Fatalf("decision deadline = %+v, want DB-created deadline in [%s, %s]", stored.DecisionDeadlineAt, before.Add(decisionWindow), after.Add(decisionWindow))
	}

	var raw map[string]any
	if err := json.Unmarshal(stored.RawRequest, &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	if approvalRequired, ok := raw["approval_required"].(bool); !ok || !approvalRequired {
		t.Fatalf("approval_required = %#v, want true", raw["approval_required"])
	}
}

func TestRequestWithdrawalRejectsUnresolvedPoliciesAndInvalidIdentifiersBeforeTemporal(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	allowReturn := true
	approvalRequired := false
	base := &walletv1.RequestWithdrawalRequest{
		TenantId:            "tenant",
		IdempotencyKey:      "withdrawal-ref",
		ClientReference:     "withdrawal-ref",
		ProviderCode:        "noop",
		WalletId:            "550e8400-e29b-41d4-a716-446655440000",
		Amount:              100,
		Currency:            "USD",
		OwnerType:           "user",
		OwnerId:             "42",
		AllowReturnToSource: &allowReturn,
		HoldExpirySeconds:   60,
		ApprovalRequired:    &approvalRequired,
	}
	tests := []struct {
		name    string
		mutate  func(*walletv1.RequestWithdrawalRequest)
		wantErr error
	}{
		{
			name: "missing return-to-source policy",
			mutate: func(request *walletv1.RequestWithdrawalRequest) {
				request.AllowReturnToSource = nil
			},
			wantErr: walletstore.ErrMissingReturnToSourcePolicy,
		},
		{
			name: "missing approval policy",
			mutate: func(request *walletv1.RequestWithdrawalRequest) {
				request.ApprovalRequired = nil
			},
			wantErr: walletstore.ErrMissingApprovalPolicy,
		},
		{
			name: "negative destination",
			mutate: func(request *walletv1.RequestWithdrawalRequest) {
				request.DestinationId = -1
			},
			wantErr: walletstore.ErrInvalidDestinationID,
		},
		{
			name: "zero wallet",
			mutate: func(request *walletv1.RequestWithdrawalRequest) {
				request.WalletId = "00000000-0000-0000-0000-000000000000"
			},
			wantErr: walletstore.ErrMissingWalletID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := proto.Clone(base).(*walletv1.RequestWithdrawalRequest)
			test.mutate(request)
			_, err := server.RequestWithdrawal(context.Background(), request)
			if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != test.wantErr.Error() {
				t.Fatalf("error = %v, want invalid argument %q", err, test.wantErr)
			}
		})
	}
}

func TestWithdrawalTimeoutsParticipateInReplayEquality(t *testing.T) {
	allowReturn := false
	approvalRequired := true
	request := &walletv1.RequestWithdrawalRequest{
		TenantId:               "tenant",
		IdempotencyKey:         "withdrawal-timeouts",
		ClientReference:        "withdrawal-timeouts",
		ProviderCode:           "bank",
		WalletId:               "550e8400-e29b-41d4-a716-446655440000",
		Amount:                 100,
		Currency:               "USD",
		OwnerType:              walletstore.OwnerTypeUser,
		OwnerId:                "42",
		DestinationId:          10,
		AllowReturnToSource:    &allowReturn,
		HoldExpirySeconds:      3600,
		ApprovalRequired:       &approvalRequired,
		ApprovalTimeoutSeconds: 120,
	}
	rawRequest, err := withdrawalRawRequest(request, allowReturn, approvalRequired, nil)
	if err != nil {
		t.Fatalf("marshal withdrawal raw request: %v", err)
	}
	existing := walletstore.PSPTransaction{
		TenantID:        request.TenantId,
		PSPProvider:     request.ProviderCode,
		IdempotencyKey:  request.IdempotencyKey,
		ClientReference: request.ClientReference,
		Direction:       "outbound",
		Amount:          request.Amount,
		Currency:        request.Currency,
		WorkflowID:      sql.NullString{String: withdrawalWorkflowID(request.TenantId, request.ClientReference), Valid: true},
		RawRequest:      walletstore.RawJSON(rawRequest),
	}

	for _, test := range []struct {
		name   string
		mutate func(*walletv1.RequestWithdrawalRequest)
	}{
		{name: "hold expiry", mutate: func(request *walletv1.RequestWithdrawalRequest) { request.HoldExpirySeconds++ }},
		{name: "approval timeout", mutate: func(request *walletv1.RequestWithdrawalRequest) { request.ApprovalTimeoutSeconds++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := proto.Clone(request).(*walletv1.RequestWithdrawalRequest)
			test.mutate(changed)
			changedRaw, err := withdrawalRawRequest(changed, allowReturn, approvalRequired, nil)
			if err != nil {
				t.Fatalf("marshal changed withdrawal: %v", err)
			}
			requested := existing
			requested.RawRequest = walletstore.RawJSON(changedRaw)
			if err := walletstore.ValidatePSPTransactionCreateReplay(&existing, requested); !errors.Is(err, walletstore.ErrDuplicateTransaction) {
				t.Fatalf("timeout mismatch error = %v, want %v", err, walletstore.ErrDuplicateTransaction)
			}
		})
	}
}

func TestRequestWithdrawalRetriesDurableReservationAfterWorkflowStartFailure(t *testing.T) {
	ctx := context.Background()
	server, tenantID, db := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})
	provisionWalletGRPCTestTenant(t, ctx, db, tenantID, "Withdrawal Retry Tenant")
	walletRow, err := ensureUserWalletForTest(t, ctx, server.Service, tenantID, 42, "USD")
	if err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, walletRow.ID, 10_000, 10_000)
	operatorID := resolveWalletGRPCTestOperator(t, ctx, db, "withdrawal-retry")
	seedWalletValidationRules(t, ctx, db, tenantID, "noop", "USD", operatorID, true, true)

	ctrl := gomock.NewController(t)
	temporal := walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalClient = temporal
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}
	wantWorkflowID := withdrawalWorkflowID(tenantID, "withdrawal-retry")
	workflowStarts := 0
	temporal.EXPECT().
		ExecuteWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, options client.StartWorkflowOptions, _ interface{}, _ ...interface{}) (client.WorkflowRun, error) {
			workflowStarts++
			if options.ID != wantWorkflowID {
				t.Fatalf("workflow ID = %q, want %q", options.ID, wantWorkflowID)
			}
			if workflowStarts == 1 {
				return nil, errors.New("temporal unavailable")
			}
			return stubWorkflowRun{id: wantWorkflowID, runID: "withdrawal-retry-run"}, nil
		}).
		Times(2)

	allowReturn := true
	approvalRequired := false
	request := &walletv1.RequestWithdrawalRequest{
		TenantId:            tenantID,
		IdempotencyKey:      "withdrawal-retry",
		ClientReference:     "withdrawal-retry",
		ProviderCode:        "noop",
		WalletId:            walletRow.ID.String(),
		Amount:              100,
		Currency:            "USD",
		OwnerType:           walletstore.OwnerTypeUser,
		OwnerId:             "42",
		AllowReturnToSource: &allowReturn,
		HoldExpirySeconds:   60,
		ApprovalRequired:    &approvalRequired,
	}
	if _, err := server.RequestWithdrawal(walletGatewayIdentityContext(42, tenantID), proto.Clone(request).(*walletv1.RequestWithdrawalRequest)); status.Code(err) != codes.Internal {
		t.Fatalf("first start error = %v, want internal", err)
	}
	reserved, err := server.Service.Store.GetPSPTransactionByReference(ctx, tenantID, request.ClientReference)
	if err != nil {
		t.Fatalf("get failed-start reservation: %v", err)
	}
	if reserved.Status != walletstore.PSPStatusInitiated {
		t.Fatalf("failed-start status = %q, want %q", reserved.Status, walletstore.PSPStatusInitiated)
	}
	if reserved.LastErrorType.Valid || reserved.LastErrorAt.Valid || reserved.ResponseMessage.Valid {
		t.Fatalf("workflow start failure mutated reservation: type=%+v at=%+v message=%+v", reserved.LastErrorType, reserved.LastErrorAt, reserved.ResponseMessage)
	}

	setWalletBalances(t, ctx, db, tenantID, walletRow.ID, 0, 0)
	run, err := server.RequestWithdrawal(walletGatewayIdentityContext(42, tenantID), proto.Clone(request).(*walletv1.RequestWithdrawalRequest))
	if err != nil {
		t.Fatalf("retry reserved withdrawal: %v", err)
	}
	if run.GetWorkflowId() != wantWorkflowID || run.GetRunId() != "withdrawal-retry-run" {
		t.Fatalf("retried run = %q/%q, want %q/withdrawal-retry-run", run.GetWorkflowId(), run.GetRunId(), wantWorkflowID)
	}
	repaired, err := server.Service.Store.GetPSPTransactionByReference(ctx, tenantID, request.ClientReference)
	if err != nil {
		t.Fatalf("get repaired reservation: %v", err)
	}
	if repaired.Status != walletstore.PSPStatusInitiated {
		t.Fatalf("repaired status = %q, want %q", repaired.Status, walletstore.PSPStatusInitiated)
	}
	if repaired.LastErrorType.Valid || repaired.LastErrorAt.Valid || repaired.ResponseMessage.Valid {
		t.Fatalf("workflow start retry mutated reservation: type=%+v at=%+v message=%+v", repaired.LastErrorType, repaired.LastErrorAt, repaired.ResponseMessage)
	}
}

func TestSignalWithdrawalApprovalRejectsForeignTenantAndDepositWorkflow(t *testing.T) {
	server, _, db := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})
	ctx := context.Background()
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{
		{ID: "a", Name: "Approval Boundary A"},
		{ID: "a-b", Name: "Approval Boundary A-B"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.New(db).ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatalf("provision approval boundary tenants: %v", err)
	}
	for _, tenant := range []string{"a", "a-b"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO psp_configs(
			tenant_id, provider_code, provider_name, api_base_url, idempotency_header_name,
			enabled_currencies, supports_withdrawal, deposit_response_mapping
		) VALUES($1, 'bank', 'Bank', 'https://bank.invalid', 'Idempotency-Key', ARRAY['USD'], TRUE, '{}')`, tenant); err != nil {
			t.Fatalf("seed %s provider: %v", tenant, err)
		}
	}
	usdUnit, err := server.Service.Store.GetCurrencyUnit(ctx, "USD", time.Now().UTC())
	if err != nil {
		t.Fatalf("resolve USD unit: %v", err)
	}
	foreignWallet, err := server.Service.Store.EnsureWallet(ctx, walletstore.EnsureWalletParams{
		TenantID: "a-b", OwnerType: walletstore.OwnerTypeMerchant, OwnerID: "foreign-owner",
		Currency: "USD", CurrencyUnitID: usdUnit.ID, KYCTier: walletstore.KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure foreign wallet: %v", err)
	}
	depositNamedWallet, err := server.Service.Store.EnsureWallet(ctx, walletstore.EnsureWalletParams{
		TenantID: "a", OwnerType: walletstore.OwnerTypeMerchant, OwnerID: "deposit-named-owner",
		Currency: "USD", CurrencyUnitID: usdUnit.ID, KYCTier: walletstore.KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure deposit-named wallet: %v", err)
	}
	foreignWorkflowID := withdrawalWorkflowID("a-b", "c")
	if _, err := server.Service.Store.CreatePSPTransaction(ctx, walletstore.PSPTransaction{
		TenantID:            "a-b",
		PSPProvider:         "bank",
		IdempotencyKey:      "c",
		ClientReference:     "c",
		Direction:           "outbound",
		WalletID:            uuid.NullUUID{UUID: foreignWallet.ID, Valid: true},
		OwnerType:           sql.NullString{String: foreignWallet.OwnerType, Valid: true},
		OwnerID:             sql.NullString{String: foreignWallet.OwnerID, Valid: true},
		AllowReturnToSource: sql.NullBool{Bool: true, Valid: true},
		Amount:              100,
		Currency:            "USD",
		CurrencyUnitID:      foreignWallet.CurrencyUnitID,
		Status:              walletstore.PSPStatusInitiated,
		WorkflowID:          sql.NullString{String: foreignWorkflowID, Valid: true},
		RawRequest:          walletstore.RawJSON(`{}`),
	}); err != nil {
		t.Fatalf("create foreign withdrawal transaction: %v", err)
	}
	depositID := depositWorkflowID("a", "deposit-1")
	if _, err := server.Service.Store.CreatePSPTransaction(ctx, walletstore.PSPTransaction{
		TenantID:            "a",
		PSPProvider:         "bank",
		IdempotencyKey:      "deposit-1",
		ClientReference:     "deposit-1",
		Direction:           "outbound",
		WalletID:            uuid.NullUUID{UUID: depositNamedWallet.ID, Valid: true},
		OwnerType:           sql.NullString{String: depositNamedWallet.OwnerType, Valid: true},
		OwnerID:             sql.NullString{String: depositNamedWallet.OwnerID, Valid: true},
		AllowReturnToSource: sql.NullBool{Bool: true, Valid: true},
		Amount:              100,
		Currency:            "USD",
		CurrencyUnitID:      depositNamedWallet.CurrencyUnitID,
		Status:              walletstore.PSPStatusInitiated,
		WorkflowID:          sql.NullString{String: depositID, Valid: true},
		RawRequest:          walletstore.RawJSON(`{}`),
	}); err != nil {
		t.Fatalf("create deposit transaction: %v", err)
	}

	for _, test := range []struct {
		name       string
		workflowID string
	}{
		{name: "foreign tenant prefix collision", workflowID: foreignWorkflowID},
		{name: "deposit", workflowID: depositID},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := server.signalWithdrawalApproval(walletAdminTenantContext("a"), withdrawalApprovalCommand{
				WorkflowID:     test.workflowID,
				Approved:       true,
				OperatorID:     1,
				ProofOfPayment: "proof",
			})
			if status.Code(err) != codes.NotFound || status.Convert(err).Message() != walletstore.ErrPSPTransactionNotFound.Error() {
				t.Fatalf("error = %v, want not found %q", err, walletstore.ErrPSPTransactionNotFound)
			}
		})
	}
}
