package walletgrpc

import (
	"context"
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
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRequestP2PTransferRequiresIdempotency(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)

	req := &walletv1.P2PTransferRequest{
		TenantId:      "tenant",
		Currency:      "USD",
		FromWalletId:  uuid.NewString(),
		ToWalletId:    uuid.NewString(),
		Amount:        100,
		FromOwnerType: "user",
		FromOwnerId:   "1",
		ToOwnerType:   "user",
		ToOwnerId:     "2",
	}

	_, err := server.RequestP2PTransfer(context.Background(), req)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", status.Code(err))
	}
}

func TestRequestP2PTransferRequiresPIN(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{WalletPINRequired: true},
	}
	server := NewServer(svc)

	req := &walletv1.P2PTransferRequest{
		TenantId:       "tenant",
		IdempotencyKey: "p2p-1",
		Currency:       "USD",
		FromWalletId:   uuid.NewString(),
		ToWalletId:     uuid.NewString(),
		Amount:         100,
		FromOwnerType:  "user",
		FromOwnerId:    "1",
		ToOwnerType:    "user",
		ToOwnerId:      "2",
	}

	_, err := server.RequestP2PTransfer(context.Background(), req)
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

	req := &walletv1.P2PTransferRequest{
		TenantId:       "tenant",
		IdempotencyKey: "p2p-1",
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

func TestRequestP2PTransferValidatesWalletsBeforeTemporal(t *testing.T) {
	ctx := context.Background()
	server, tenantID, _ := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})

	ctrl := gomock.NewController(t)
	server.TemporalClient = walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}

	req := &walletv1.P2PTransferRequest{
		TenantId:       tenantID,
		IdempotencyKey: "p2p-missing-wallet",
		Currency:       "USD",
		FromWalletId:   uuid.NewString(),
		ToWalletId:     uuid.NewString(),
		Amount:         100,
		FromOwnerType:  walletstore.OwnerTypeUser,
		FromOwnerId:    "1",
		ToOwnerType:    walletstore.OwnerTypeUser,
		ToOwnerId:      "2",
	}

	_, err := server.RequestP2PTransfer(ctx, req)
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
	if err := basestore.New(db).EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	fromWallet, err := server.Service.EnsureUserWallet(ctx, tenantID, 1, "USD")
	if err != nil {
		t.Fatalf("ensure from wallet: %v", err)
	}
	toWallet, err := server.Service.EnsureUserWallet(ctx, tenantID, 2, "USD")
	if err != nil {
		t.Fatalf("ensure to wallet: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, fromWallet.ID, 50, 50)
	seedP2PValidationRules(t, ctx, db, tenantID, "USD")

	ctrl := gomock.NewController(t)
	server.TemporalClient = walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}

	req := &walletv1.P2PTransferRequest{
		TenantId:       tenantID,
		IdempotencyKey: "p2p-insufficient-funds",
		Currency:       "USD",
		FromWalletId:   fromWallet.ID.String(),
		ToWalletId:     toWallet.ID.String(),
		Amount:         100,
		FromOwnerType:  walletstore.OwnerTypeUser,
		FromOwnerId:    "1",
		ToOwnerType:    walletstore.OwnerTypeUser,
		ToOwnerId:      "2",
	}

	_, err = server.RequestP2PTransfer(ctx, req)
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
	if err := basestore.New(db).EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	fromWallet, err := server.Service.EnsureUserWallet(ctx, tenantID, 1, "USD")
	if err != nil {
		t.Fatalf("ensure from wallet: %v", err)
	}
	toWallet, err := server.Service.EnsureUserWallet(ctx, tenantID, 2, "USD")
	if err != nil {
		t.Fatalf("ensure to wallet: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, fromWallet.ID, 10_000, 10_000)
	seedP2PValidationRules(t, ctx, db, tenantID, "USD")

	ctrl := gomock.NewController(t)
	mockTemporal := walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalClient = mockTemporal
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}

	run := stubWorkflowRun{id: "p2p-wf-id", runID: "p2p-run-id"}
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
			if params.Amount != 100 || params.Currency != "USD" {
				t.Fatalf("unexpected P2P params: %+v", params)
			}
			if params.IdempotencyKey != "p2p-valid" || params.ReferenceID != "p2p-valid" {
				t.Fatalf("workflow idempotency/reference = %q/%q, want p2p-valid/p2p-valid", params.IdempotencyKey, params.ReferenceID)
			}
			if params.FromWalletID != fromWallet.ID.String() || params.ToWalletID != toWallet.ID.String() {
				t.Fatalf("workflow wallet params = %q/%q, want %q/%q", params.FromWalletID, params.ToWalletID, fromWallet.ID, toWallet.ID)
			}
			return run, nil
		})

	req := &walletv1.P2PTransferRequest{
		TenantId:       tenantID,
		IdempotencyKey: " \t ",
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

	resp, err := server.RequestP2PTransfer(ctx, req)
	if err != nil {
		t.Fatalf("request P2P transfer: %v", err)
	}
	if resp.GetWorkflowId() != run.id || resp.GetRunId() != run.runID {
		t.Fatalf("workflow run = %q/%q, want %q/%q", resp.GetWorkflowId(), resp.GetRunId(), run.id, run.runID)
	}
}

func seedP2PValidationRules(t *testing.T, ctx context.Context, db *basestore.DB, tenantID, currency string) {
	t.Helper()

	feeStmt := db.Rebind(`INSERT INTO fee_configs(
		tenant_id, transaction_type, currency, tier_min, percentage_fee, flat_fee, min_fee, is_active
	) VALUES(?, 'p2p', ?, 0, 0, 0, 0, TRUE)
	ON CONFLICT (tenant_id, transaction_type, currency, tier_min) DO NOTHING`)
	if _, err := db.ExecContext(ctx, feeStmt, tenantID, currency); err != nil {
		t.Fatalf("seed p2p fee config: %v", err)
	}

	limitStmt := db.Rebind(`INSERT INTO transaction_limits(
		tenant_id, kyc_tier, transaction_type, currency, daily_limit, monthly_limit, per_transaction_limit, is_active
	) VALUES(?, ?, 'p2p', ?, 1000000000, 1000000000, 1000000000, TRUE)
	ON CONFLICT (tenant_id, kyc_tier, transaction_type, currency) DO NOTHING`)
	if _, err := db.ExecContext(ctx, limitStmt, tenantID, walletstore.KYCTierUnverified, currency); err != nil {
		t.Fatalf("seed p2p transaction limit: %v", err)
	}
}
