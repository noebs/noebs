package walletgrpc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	basestore "github.com/adonese/noebs/store"
	walletgrpcmock "github.com/adonese/noebs/wallet/grpc/mock"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestRequestDepositMintsImmutableIntentAndReplaysRecordedRun(t *testing.T) {
	ctx := context.Background()
	server, tenantID, _, walletRow := newDepositIdempotencyTestFixture(t, "Deposit Intent Tenant")
	request := depositIdempotencyTestRequest(tenantID, walletRow.ID, "mobile-request-1")

	ctrl := gomock.NewController(t)
	temporal := walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalClient = temporal
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}
	temporal.EXPECT().ExecuteWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, options client.StartWorkflowOptions, _ any, args ...any) (client.WorkflowRun, error) {
			if options.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE {
				t.Fatalf("workflow reuse policy = %v", options.WorkflowIDReusePolicy)
			}
			params, ok := args[0].(walletworkflow.DepositParams)
			if !ok || params.TenantID != tenantID || !strings.HasPrefix(params.IntentReference, "dep_") {
				t.Fatalf("deposit params = %#v", args)
			}
			if params.IntentReference == request.IdempotencyKey || options.ID != depositWorkflowID(tenantID, params.IntentReference) {
				t.Fatalf("caller controls deposit authority: params=%+v workflow=%q", params, options.ID)
			}
			return stubWorkflowRun{id: options.ID, runID: "deposit-run-1"}, nil
		}).Times(1)

	run, err := server.RequestDeposit(walletGatewayIdentityContext(42, tenantID), proto.Clone(request).(*walletv1.RequestDepositRequest))
	if err != nil {
		t.Fatal(err)
	}
	intent, err := server.Service.Store.GetDepositIntentByIdempotency(ctx, tenantID, request.ProviderCode, request.IdempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	if intent.IntentReference == request.IdempotencyKey || intent.WalletID != walletRow.ID || intent.OwnerType != walletstore.OwnerTypeUser || intent.OwnerID != "42" {
		t.Fatalf("stored intent = %+v", intent)
	}
	if run.GetWorkflowId() != intent.WorkflowID || run.GetRunId() != "deposit-run-1" || !intent.RunID.Valid || intent.RunID.String != run.GetRunId() {
		t.Fatalf("run=%+v intent=%+v", run, intent)
	}
	transaction, err := server.Service.Store.GetPSPTransactionByReference(ctx, tenantID, intent.IntentReference)
	if err != nil {
		t.Fatal(err)
	}
	if err := walletstore.ValidateDepositIntentTransaction(intent, transaction); err != nil {
		t.Fatalf("intent transaction link: %v", err)
	}

	replayed, err := server.RequestDeposit(walletGatewayIdentityContext(42, tenantID), proto.Clone(request).(*walletv1.RequestDepositRequest))
	if err != nil {
		t.Fatal(err)
	}
	if replayed.GetWorkflowId() != run.GetWorkflowId() || replayed.GetRunId() != run.GetRunId() {
		t.Fatalf("replayed run = %+v, want %+v", replayed, run)
	}

	mismatch := proto.Clone(request).(*walletv1.RequestDepositRequest)
	mismatch.Amount++
	_, err = server.RequestDeposit(walletGatewayIdentityContext(42, tenantID), mismatch)
	if status.Code(err) != codes.AlreadyExists || status.Convert(err).Message() != walletstore.ErrDuplicateDepositIntent.Error() {
		t.Fatalf("mismatched replay error = %v", err)
	}
}

func TestRequestDepositRetriesSameIntentAfterTemporalStartFailure(t *testing.T) {
	ctx := context.Background()
	server, tenantID, _, walletRow := newDepositIdempotencyTestFixture(t, "Deposit Retry Tenant")
	request := depositIdempotencyTestRequest(tenantID, walletRow.ID, "mobile-request-retry")

	ctrl := gomock.NewController(t)
	temporal := walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalClient = temporal
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}
	startErr := errors.New("temporal unavailable")
	var workflowID string
	temporal.EXPECT().ExecuteWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, options client.StartWorkflowOptions, _ any, _ ...any) (client.WorkflowRun, error) {
			workflowID = options.ID
			return nil, startErr
		}).Times(1)

	_, err := server.RequestDeposit(walletGatewayIdentityContext(42, tenantID), proto.Clone(request).(*walletv1.RequestDepositRequest))
	if status.Code(err) != codes.Internal {
		t.Fatalf("first start error = %v", err)
	}
	reserved, err := server.Service.Store.GetDepositIntentByIdempotency(ctx, tenantID, request.ProviderCode, request.IdempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	if reserved.WorkflowID != workflowID || reserved.RunID.Valid {
		t.Fatalf("reservation after failed start = %+v", reserved)
	}

	temporal.EXPECT().ExecuteWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, options client.StartWorkflowOptions, _ any, _ ...any) (client.WorkflowRun, error) {
			if options.ID != workflowID {
				t.Fatalf("retry workflow ID = %q, want %q", options.ID, workflowID)
			}
			return stubWorkflowRun{id: workflowID, runID: "deposit-retry-run"}, nil
		}).Times(1)
	run, err := server.RequestDeposit(walletGatewayIdentityContext(42, tenantID), proto.Clone(request).(*walletv1.RequestDepositRequest))
	if err != nil {
		t.Fatal(err)
	}
	if run.GetWorkflowId() != workflowID || run.GetRunId() != "deposit-retry-run" {
		t.Fatalf("retried run = %+v", run)
	}
}

func newDepositIdempotencyTestFixture(t *testing.T, tenantName string) (*Server, string, *basestore.DB, *walletstore.Wallet) {
	t.Helper()
	ctx := context.Background()
	server, tenantID, db := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})
	provisionWalletGRPCTestTenant(t, ctx, db, tenantID, tenantName)
	walletRow, err := server.Service.EnsureUserWallet(ctx, tenantID, 42, "USD")
	if err != nil {
		t.Fatal(err)
	}
	operatorID := resolveWalletGRPCTestOperator(t, ctx, db, "deposit-idempotency")
	seedWalletValidationRules(t, ctx, db, tenantID, "noop", "USD", operatorID, true, true)
	return server, tenantID, db, walletRow
}

func depositIdempotencyTestRequest(tenantID string, walletID uuid.UUID, idempotencyKey string) *walletv1.RequestDepositRequest {
	return &walletv1.RequestDepositRequest{
		TenantId:       tenantID,
		ProviderCode:   "noop",
		WalletId:       walletID.String(),
		Amount:         100,
		Currency:       "USD",
		IdempotencyKey: idempotencyKey,
	}
}
