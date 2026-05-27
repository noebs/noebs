package walletgrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet"
	walletgrpcmock "github.com/adonese/noebs/wallet/grpc/mock"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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

func TestRequestWithdrawalRequiresPin(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{WalletPINRequired: true},
	}
	server := NewServer(svc)

	req := &walletv1.WithdrawalRequest{
		TenantId:                   "tenant",
		ClientReference:            "ref-1",
		ProviderCode:               "noop",
		WalletId:                   uuid.NewString(),
		Amount:                     500,
		Currency:                   "USD",
		OwnerType:                  "user",
		OwnerId:                    "42",
		DestinationId:              10,
		HoldExpirySeconds:          60,
		VerificationTimeoutSeconds: 60,
	}

	_, err := server.RequestWithdrawal(context.Background(), req)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", status.Code(err))
	}
	if err.Error() == "" {
		t.Fatalf("expected error message")
	}
}

func TestWithdrawalSignalsRequireAdminAuth(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{AdminKey: "test-admin-key"},
	}
	server := NewServer(svc)

	if _, err := server.SignalWithdrawalApproval(context.Background(), &walletv1.WithdrawalApprovalRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied for approval signal, got %v", status.Code(err))
	}

	if _, err := server.SignalWithdrawalVerification(context.Background(), &walletv1.WithdrawalDestinationVerificationRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied for verification signal, got %v", status.Code(err))
	}
}

func TestWithdrawalSignalsValidateAfterAdminAuth(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{AdminKey: "test-admin-key"},
	}
	server := NewServer(svc)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-admin-key", "test-admin-key"))

	if _, err := server.SignalWithdrawalApproval(ctx, &walletv1.WithdrawalApprovalRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument for approval signal, got %v", status.Code(err))
	}

	if _, err := server.SignalWithdrawalVerification(ctx, &walletv1.WithdrawalDestinationVerificationRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument for verification signal, got %v", status.Code(err))
	}
}

func TestRequestWithdrawalStartsWorkflow(t *testing.T) {
	if os.Getenv("DOCKER_HOST") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		t.Skip("docker host not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		t.Skipf("postgres container unavailable: %v", err)
	}
	defer func() {
		_ = container.Terminate(context.Background())
	}()

	dbName := fmt.Sprintf("noebs_wallet_withdrawal_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	db, err := store.OpenFromConfig(dbURL, "", store.DriverPostgres)
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
	if err := store.MigrateScope(ctx, db, tenantID, store.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate wallet-ledger db: %v", err)
	}
	if err := store.MigrateScope(ctx, db, tenantID, store.MigrationScopePSPWebhook); err != nil {
		t.Fatalf("migrate psp-webhook db: %v", err)
	}

	cfg := ebs_fields.NoebsConfig{
		WalletPINRequired:                true,
		Wallet2FAThreshold:               1,
		WalletApprovalThreshold:          0,
		WalletHoldExpirySeconds:          3600,
		WalletVerificationTimeoutSeconds: 300,
	}
	svc := wallet.NewService(db, cfg)
	server := NewServer(svc)

	ctrl := gomock.NewController(t)
	mockTemporal := walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalClient = mockTemporal
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}

	run := stubWorkflowRun{id: "wf-id", runID: "run-id"}
	mockTemporal.EXPECT().
		ExecuteWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, opts client.StartWorkflowOptions, wf interface{}, args ...interface{}) (client.WorkflowRun, error) {
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
			if params.Request.Amount != 500 || params.Request.Currency != "USD" {
				t.Fatalf("unexpected request params")
			}
			if params.HoldExpirySeconds != cfg.WalletHoldExpirySeconds {
				t.Fatalf("expected hold expiry %d, got %d", cfg.WalletHoldExpirySeconds, params.HoldExpirySeconds)
			}
			if params.VerificationTimeoutSeconds != cfg.WalletVerificationTimeoutSeconds {
				t.Fatalf("expected verification timeout %d, got %d", cfg.WalletVerificationTimeoutSeconds, params.VerificationTimeoutSeconds)
			}
			return run, nil
		})

	allowReturn := true
	req := &walletv1.WithdrawalRequest{
		TenantId:                   "tenant",
		ClientReference:            "ref-2",
		ProviderCode:               "noop",
		WalletId:                   uuid.NewString(),
		Amount:                     500,
		Currency:                   "USD",
		UserId:                     42,
		OwnerType:                  "user",
		OwnerId:                    "42",
		DestinationId:              10,
		AllowReturnToSource:        &allowReturn,
		WalletPin:                  "1234",
		TwoFaCode:                  "000000",
		HoldExpirySeconds:          0,
		ApprovalTimeoutSeconds:     0,
		VerificationTimeoutSeconds: 0,
	}

	resp, err := server.RequestWithdrawal(ctx, req)
	if err != nil {
		t.Fatalf("request withdrawal: %v", err)
	}
	if resp.WorkflowId == "" {
		t.Fatalf("expected workflow id")
	}

	stored, err := svc.Store.GetPSPTransactionByReference(ctx, req.TenantId, req.ClientReference)
	if err != nil {
		t.Fatalf("lookup transaction: %v", err)
	}
	if stored.Status != "initiated" {
		t.Fatalf("expected initiated status, got %s", stored.Status)
	}
	if !stored.WorkflowID.Valid || stored.WorkflowID.String == "" {
		t.Fatalf("expected workflow id persisted")
	}

	var raw map[string]any
	if err := json.Unmarshal(stored.RawRequest, &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	if _, ok := raw["wallet_pin"]; ok {
		t.Fatalf("wallet_pin should not be stored")
	}
	if _, ok := raw["two_fa_code"]; ok {
		t.Fatalf("two_fa_code should not be stored")
	}
}
