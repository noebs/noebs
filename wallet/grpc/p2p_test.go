package walletgrpc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	basestore "github.com/adonese/noebs/store"
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

func TestRequestP2PTransferRequiresIdempotency(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)

	req := &walletv1.RequestP2PTransferRequest{
		TenantId:      "tenant",
		ReferenceId:   "p2p-1",
		Currency:      "USD",
		FromWalletId:  uuid.NewString(),
		ToWalletId:    uuid.NewString(),
		Amount:        100,
		FromOwnerType: "user",
		FromOwnerId:   "1",
		ToOwnerType:   "user",
		ToOwnerId:     "2",
	}

	_, err := server.RequestP2PTransfer(walletGatewayIdentityContext(1, "tenant"), req)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", status.Code(err))
	}
}

func TestRequestP2PTransferPublicRequiresGatewayIdentity(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)
	ctx := walletServerMethodContext(context.Background(), walletv1.WalletPublicService_RequestP2PTransfer_FullMethodName)

	req := &walletv1.RequestP2PTransferRequest{
		TenantId:       "tenant",
		IdempotencyKey: "p2p-1",
		ReferenceId:    "p2p-1",
		Currency:       "USD",
		FromWalletId:   uuid.NewString(),
		ToWalletId:     uuid.NewString(),
		Amount:         100,
		FromOwnerType:  "user",
		FromOwnerId:    "1",
		ToOwnerType:    "user",
		ToOwnerId:      "2",
	}

	_, err := server.RequestP2PTransfer(ctx, req)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}

func TestRequestP2PTransferRejectsZeroWalletIDsBeforeStoreAccess(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	base := &walletv1.RequestP2PTransferRequest{
		TenantId:       "tenant",
		IdempotencyKey: "p2p-zero-wallet",
		ReferenceId:    "p2p-zero-wallet",
		Currency:       "USD",
		FromWalletId:   "550e8400-e29b-41d4-a716-446655440000",
		ToWalletId:     "550e8400-e29b-41d4-a716-446655440001",
		Amount:         100,
		FromOwnerType:  "user",
		FromOwnerId:    "42",
		ToOwnerType:    "user",
		ToOwnerId:      "2",
	}
	for _, field := range []string{"source", "destination"} {
		t.Run(field, func(t *testing.T) {
			request := proto.Clone(base).(*walletv1.RequestP2PTransferRequest)
			if field == "source" {
				request.FromWalletId = "00000000-0000-0000-0000-000000000000"
			} else {
				request.ToWalletId = "00000000-0000-0000-0000-000000000000"
			}
			_, err := server.RequestP2PTransfer(walletGatewayIdentityContext(42, "tenant"), request)
			if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != walletstore.ErrMissingWalletID.Error() {
				t.Fatalf("error = %v, want invalid argument %q", err, walletstore.ErrMissingWalletID)
			}
		})
	}
}

func TestRequestP2PTransferValidatesWalletsBeforeTemporal(t *testing.T) {
	server, tenantID, _ := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})

	ctrl := gomock.NewController(t)
	server.TemporalClient = walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}

	req := &walletv1.RequestP2PTransferRequest{
		TenantId:       tenantID,
		IdempotencyKey: "p2p-missing-wallet",
		ReferenceId:    "p2p-missing-wallet",
		Currency:       "USD",
		FromWalletId:   uuid.NewString(),
		ToWalletId:     uuid.NewString(),
		Amount:         100,
		FromOwnerType:  walletstore.OwnerTypeUser,
		FromOwnerId:    "1",
		ToOwnerType:    walletstore.OwnerTypeUser,
		ToOwnerId:      "2",
	}

	_, err := server.RequestP2PTransfer(walletGatewayIdentityContext(1, tenantID), req)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.NotFound)
	}
	if status.Convert(err).Message() != walletstore.ErrWalletNotFound.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrWalletNotFound.Error())
	}
}

func TestRequestP2PTransferRejectsInsufficientFundsBeforeTemporal(t *testing.T) {
	ctx := context.Background()
	server, tenantID, db := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})
	provisionWalletGRPCTestTenant(t, ctx, db, tenantID, "P2P Tenant")
	fromWallet, err := ensureUserWalletForTest(t, ctx, server.Service, tenantID, 1, "USD")
	if err != nil {
		t.Fatalf("ensure from wallet: %v", err)
	}
	toWallet, err := ensureUserWalletForTest(t, ctx, server.Service, tenantID, 2, "USD")
	if err != nil {
		t.Fatalf("ensure to wallet: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, fromWallet.ID, 50, 50)
	operatorID := resolveWalletGRPCTestOperator(t, ctx, db, "p2p-validation")
	seedP2PValidationRules(t, ctx, db, tenantID, "USD", operatorID)

	ctrl := gomock.NewController(t)
	server.TemporalClient = walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}

	req := &walletv1.RequestP2PTransferRequest{
		TenantId:       tenantID,
		IdempotencyKey: "p2p-insufficient-funds",
		ReferenceId:    "p2p-insufficient-funds",
		Currency:       "USD",
		FromWalletId:   fromWallet.ID.String(),
		ToWalletId:     toWallet.ID.String(),
		Amount:         100,
		FromOwnerType:  walletstore.OwnerTypeUser,
		FromOwnerId:    "1",
		ToOwnerType:    walletstore.OwnerTypeUser,
		ToOwnerId:      "2",
	}

	_, err = server.RequestP2PTransfer(walletGatewayIdentityContext(1, tenantID), req)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.FailedPrecondition)
	}
	if status.Convert(err).Message() != walletstore.ErrInsufficientFunds.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrInsufficientFunds.Error())
	}
}

func TestRequestP2PTransferStartsWorkflowAfterValidation(t *testing.T) {
	ctx := context.Background()
	server, tenantID, db := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})
	provisionWalletGRPCTestTenant(t, ctx, db, tenantID, "P2P Tenant")
	fromWallet, err := ensureUserWalletForTest(t, ctx, server.Service, tenantID, 1, "USD")
	if err != nil {
		t.Fatalf("ensure from wallet: %v", err)
	}
	toWallet, err := ensureUserWalletForTest(t, ctx, server.Service, tenantID, 2, "USD")
	if err != nil {
		t.Fatalf("ensure to wallet: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, fromWallet.ID, 10_000, 10_000)
	operatorID := resolveWalletGRPCTestOperator(t, ctx, db, "p2p-validation")
	seedP2PValidationRules(t, ctx, db, tenantID, "USD", operatorID)

	ctrl := gomock.NewController(t)
	mockTemporal := walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalClient = mockTemporal
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}

	run := stubWorkflowRun{id: p2pWorkflowID(tenantID, "p2p-valid"), runID: "p2p-run-id"}
	mockTemporal.EXPECT().
		ExecuteWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, opts client.StartWorkflowOptions, wf interface{}, args ...interface{}) (client.WorkflowRun, error) {
			if opts.ID != p2pWorkflowID(tenantID, "p2p-valid") {
				t.Fatalf("workflow ID = %q, want %q", opts.ID, p2pWorkflowID(tenantID, "p2p-valid"))
			}
			if opts.TaskQueue == "" {
				t.Fatalf("expected task queue")
			}
			if len(args) != 1 {
				t.Fatalf("expected one workflow argument")
			}
			params, ok := args[0].(walletworkflow.P2PParams)
			if !ok {
				t.Fatalf("workflow params type = %T, want %T", args[0], walletworkflow.P2PParams{})
			}
			if params.TenantID != tenantID || params.IdempotencyKey != "p2p-valid" {
				t.Fatalf("workflow command key = %+v, want tenant and p2p-valid only", params)
			}
			return run, nil
		})

	req := &walletv1.RequestP2PTransferRequest{
		TenantId:       tenantID,
		IdempotencyKey: "p2p-valid",
		ReferenceId:    "p2p-valid",
		Currency:       "USD",
		FromWalletId:   fromWallet.ID.String(),
		ToWalletId:     toWallet.ID.String(),
		Amount:         100,
		FromOwnerType:  walletstore.OwnerTypeUser,
		FromOwnerId:    "1",
		ToOwnerType:    walletstore.OwnerTypeUser,
		ToOwnerId:      "2",
	}

	resp, err := server.RequestP2PTransfer(walletGatewayIdentityContext(1, tenantID), req)
	if err != nil {
		t.Fatalf("request P2P transfer: %v", err)
	}
	if resp.GetWorkflowId() != run.id || resp.GetRunId() != run.runID {
		t.Fatalf("workflow run = %q/%q, want %q/%q", resp.GetWorkflowId(), resp.GetRunId(), run.id, run.runID)
	}
	storedCommand, err := server.Service.Store.GetP2PCommand(ctx, tenantID, req.IdempotencyKey)
	if err != nil {
		t.Fatalf("get stored P2P command: %v", err)
	}
	payload, err := walletstore.DecodeP2PCommand(storedCommand, tenantID, req.IdempotencyKey, run.id)
	if err != nil {
		t.Fatalf("decode stored P2P command: %v", err)
	}
	if payload.Amount != req.Amount || payload.Currency != req.Currency ||
		payload.FromWalletID != fromWallet.ID.String() || payload.ToWalletID != toWallet.ID.String() {
		t.Fatalf("stored P2P authority = %+v, want request facts", payload)
	}

	setWalletBalances(t, ctx, db, tenantID, fromWallet.ID, 0, 0)
	replayed, err := server.RequestP2PTransfer(walletGatewayIdentityContext(1, tenantID), proto.Clone(req).(*walletv1.RequestP2PTransferRequest))
	if err != nil {
		t.Fatalf("replay P2P transfer after balance changed: %v", err)
	}
	if replayed.GetWorkflowId() != resp.GetWorkflowId() || replayed.GetRunId() != resp.GetRunId() {
		t.Fatalf("replayed workflow run = %q/%q, want %q/%q", replayed.GetWorkflowId(), replayed.GetRunId(), resp.GetWorkflowId(), resp.GetRunId())
	}

	mismatches := []struct {
		name   string
		mutate func(*walletv1.RequestP2PTransferRequest)
	}{
		{name: "destination wallet", mutate: func(request *walletv1.RequestP2PTransferRequest) { request.ToWalletId = uuid.NewString() }},
		{name: "amount", mutate: func(request *walletv1.RequestP2PTransferRequest) { request.Amount++ }},
		{name: "currency", mutate: func(request *walletv1.RequestP2PTransferRequest) { request.Currency = "AED" }},
		{name: "description", mutate: func(request *walletv1.RequestP2PTransferRequest) { request.Description = "changed" }},
		{name: "reference", mutate: func(request *walletv1.RequestP2PTransferRequest) { request.ReferenceId = "changed" }},
		{name: "destination owner type", mutate: func(request *walletv1.RequestP2PTransferRequest) { request.ToOwnerType = walletstore.OwnerTypeMerchant }},
		{name: "destination owner", mutate: func(request *walletv1.RequestP2PTransferRequest) { request.ToOwnerId = "changed" }},
	}
	for _, mismatch := range mismatches {
		t.Run("replay mismatch "+mismatch.name, func(t *testing.T) {
			changed := proto.Clone(req).(*walletv1.RequestP2PTransferRequest)
			mismatch.mutate(changed)
			_, err := server.RequestP2PTransfer(walletGatewayIdentityContext(1, tenantID), changed)
			if status.Code(err) != codes.AlreadyExists || status.Convert(err).Message() != walletstore.ErrDuplicateP2PCommand.Error() {
				t.Fatalf("error = %v, want already exists %q", err, walletstore.ErrDuplicateP2PCommand)
			}
		})
	}
}

func TestRequestP2PTransferConcurrentReplaysConvergeOnOneRun(t *testing.T) {
	ctx := context.Background()
	server, tenantID, db := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})
	provisionWalletGRPCTestTenant(t, ctx, db, tenantID, "P2P Concurrent Tenant")
	fromWallet, err := ensureUserWalletForTest(t, ctx, server.Service, tenantID, 1, "USD")
	if err != nil {
		t.Fatalf("ensure from wallet: %v", err)
	}
	toWallet, err := ensureUserWalletForTest(t, ctx, server.Service, tenantID, 2, "USD")
	if err != nil {
		t.Fatalf("ensure to wallet: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, fromWallet.ID, 10_000, 10_000)
	operatorID := resolveWalletGRPCTestOperator(t, ctx, db, "p2p-concurrent")
	seedP2PValidationRules(t, ctx, db, tenantID, "USD", operatorID)

	temporal := &convergingP2PTemporalClient{runID: "p2p-concurrent-run"}
	server.TemporalClient = temporal
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}
	request := &walletv1.RequestP2PTransferRequest{
		TenantId:       tenantID,
		IdempotencyKey: "p2p-concurrent",
		ReferenceId:    "p2p-concurrent",
		Currency:       "USD",
		FromWalletId:   fromWallet.ID.String(),
		ToWalletId:     toWallet.ID.String(),
		Amount:         100,
		FromOwnerType:  walletstore.OwnerTypeUser,
		FromOwnerId:    "1",
		ToOwnerType:    walletstore.OwnerTypeUser,
		ToOwnerId:      "2",
	}

	const callers = 24
	start := make(chan struct{})
	runs := make(chan *walletv1.RequestP2PTransferResponse, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run, err := server.RequestP2PTransfer(
				walletGatewayIdentityContext(1, tenantID),
				proto.Clone(request).(*walletv1.RequestP2PTransferRequest),
			)
			if err != nil {
				errs <- err
				return
			}
			runs <- run
		}()
	}
	close(start)
	wg.Wait()
	close(runs)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent P2P replay: %v", err)
	}
	wantWorkflowID := p2pWorkflowID(tenantID, request.IdempotencyKey)
	for run := range runs {
		if run.GetWorkflowId() != wantWorkflowID || run.GetRunId() != temporal.runID {
			t.Errorf("workflow run = %q/%q, want %q/%q", run.GetWorkflowId(), run.GetRunId(), wantWorkflowID, temporal.runID)
		}
	}
	stored, err := server.Service.Store.GetP2PCommand(ctx, tenantID, request.IdempotencyKey)
	if err != nil {
		t.Fatalf("get P2P command: %v", err)
	}
	if !stored.RunID.Valid || stored.RunID.String != temporal.runID {
		t.Fatalf("stored run = %+v, want %q", stored.RunID, temporal.runID)
	}
}

func TestRequestP2PTransferRepairsRunAfterTemporalAlreadyStarted(t *testing.T) {
	ctx := context.Background()
	server, tenantID, db := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})
	provisionWalletGRPCTestTenant(t, ctx, db, tenantID, "P2P Repair Tenant")
	fromWallet, err := ensureUserWalletForTest(t, ctx, server.Service, tenantID, 1, "USD")
	if err != nil {
		t.Fatalf("ensure from wallet: %v", err)
	}
	toWallet, err := ensureUserWalletForTest(t, ctx, server.Service, tenantID, 2, "USD")
	if err != nil {
		t.Fatalf("ensure to wallet: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, fromWallet.ID, 10_000, 10_000)
	operatorID := resolveWalletGRPCTestOperator(t, ctx, db, "p2p-repair")
	seedP2PValidationRules(t, ctx, db, tenantID, "USD", operatorID)

	temporal := &convergingP2PTemporalClient{runID: "p2p-repaired-run", alreadyStarted: true}
	server.TemporalClient = temporal
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}
	request := &walletv1.RequestP2PTransferRequest{
		TenantId:       tenantID,
		IdempotencyKey: "p2p-repair",
		ReferenceId:    "p2p-repair",
		Currency:       "USD",
		FromWalletId:   fromWallet.ID.String(),
		ToWalletId:     toWallet.ID.String(),
		Amount:         100,
		FromOwnerType:  walletstore.OwnerTypeUser,
		FromOwnerId:    "1",
		ToOwnerType:    walletstore.OwnerTypeUser,
		ToOwnerId:      "2",
	}
	commandDocument, err := json.Marshal(walletstore.P2PCommandPayload{
		Currency:      request.Currency,
		FromWalletID:  request.FromWalletId,
		ToWalletID:    request.ToWalletId,
		Amount:        request.Amount,
		Description:   request.Description,
		ReferenceID:   request.ReferenceId,
		FromOwnerType: request.FromOwnerType,
		FromOwnerID:   request.FromOwnerId,
		ToOwnerType:   request.ToOwnerType,
		ToOwnerID:     request.ToOwnerId,
	})
	if err != nil {
		t.Fatalf("marshal pending P2P command: %v", err)
	}
	if _, err := server.Service.Store.ReserveP2PCommand(ctx, walletstore.P2PCommandReservation{
		TenantID:       tenantID,
		IdempotencyKey: request.IdempotencyKey,
		WorkflowID:     p2pWorkflowID(tenantID, request.IdempotencyKey),
		FromWalletID:   fromWallet.ID,
		ToWalletID:     toWallet.ID,
		FromOwnerType:  request.FromOwnerType,
		FromOwnerID:    request.FromOwnerId,
		ToOwnerType:    request.ToOwnerType,
		ToOwnerID:      request.ToOwnerId,
		Command:        walletstore.RawJSON(commandDocument),
	}); err != nil {
		t.Fatalf("reserve pending P2P command: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, fromWallet.ID, 0, 0)

	run, err := server.RequestP2PTransfer(walletGatewayIdentityContext(1, tenantID), request)
	if err != nil {
		t.Fatalf("repair P2P command run: %v", err)
	}
	if run.GetWorkflowId() != p2pWorkflowID(tenantID, request.IdempotencyKey) || run.GetRunId() != temporal.runID {
		t.Fatalf("repaired run = %q/%q", run.GetWorkflowId(), run.GetRunId())
	}
	stored, err := server.Service.Store.GetP2PCommand(ctx, tenantID, request.IdempotencyKey)
	if err != nil {
		t.Fatalf("get repaired P2P command: %v", err)
	}
	if !stored.RunID.Valid || stored.RunID.String != temporal.runID {
		t.Fatalf("stored repaired run = %+v, want %q", stored.RunID, temporal.runID)
	}
}

type convergingP2PTemporalClient struct {
	mu             sync.Mutex
	runID          string
	workflowID     string
	alreadyStarted bool
}

func (c *convergingP2PTemporalClient) ExecuteWorkflow(_ context.Context, options client.StartWorkflowOptions, _ interface{}, _ ...interface{}) (client.WorkflowRun, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.alreadyStarted || c.workflowID != "" {
		return nil, serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "", c.runID)
	}
	c.workflowID = options.ID
	return stubWorkflowRun{id: options.ID, runID: c.runID}, nil
}

func (c *convergingP2PTemporalClient) SignalWorkflow(context.Context, string, string, string, interface{}) error {
	return errors.New("unexpected signal")
}

func seedP2PValidationRules(t *testing.T, ctx context.Context, db *basestore.DB, tenantID, currency string, operatorID int64) {
	t.Helper()

	feeStmt := db.Rebind(`INSERT INTO fee_configs(
		tenant_id, transaction_type, currency, currency_unit_version_id,
		tier_min, percentage_fee, flat_fee, min_fee, is_active,
		created_by_operator_id
	) VALUES(?, 'p2p', ?, (SELECT id FROM currency_unit_versions WHERE currency_code = ? AND valid_to IS NULL), 0, 0, 0, 0, TRUE, ?)
	ON CONFLICT (tenant_id, transaction_type, currency, currency_unit_version_id, tier_min) DO NOTHING`)
	if _, err := db.ExecContext(ctx, feeStmt, tenantID, currency, currency, operatorID); err != nil {
		t.Fatalf("seed p2p fee config: %v", err)
	}

	limitStmt := db.Rebind(`INSERT INTO transaction_limits(
		tenant_id, kyc_tier, transaction_type, currency, currency_unit_version_id,
		daily_limit, monthly_limit, per_transaction_limit, is_active
	) VALUES(?, ?, 'p2p', ?, (SELECT id FROM currency_unit_versions WHERE currency_code = ? AND valid_to IS NULL), 1000000000, 1000000000, 1000000000, TRUE)
	ON CONFLICT (tenant_id, kyc_tier, transaction_type, currency, currency_unit_version_id) DO NOTHING`)
	if _, err := db.ExecContext(ctx, limitStmt, tenantID, walletstore.KYCTierUnverified, currency, currency); err != nil {
		t.Fatalf("seed p2p transaction limit: %v", err)
	}
}
