package walletgrpc

import (
	"context"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
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

func TestRequestManualTransferRequiresTimeout(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)
	ctx := metadata.NewIncomingContext(context.Background(), adminMetadata())

	req := &walletv1.ManualTransferRequest{
		TenantId:       "tenant",
		IdempotencyKey: "manual-1",
		TransferType:   "manual_debit",
		WalletId:       uuid.NewString(),
		Amount:         100,
		Currency:       "USD",
		Reason:         "test",
		RequestedBy:    10,
	}

	_, err := server.RequestManualTransfer(ctx, req)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", status.Code(err))
	}
}

func TestRequestManualTransferRejectsInvalidTransferType(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{WalletManualTransferApprovalTimeoutSeconds: 60},
	}
	server := NewServer(svc)
	ctx := metadata.NewIncomingContext(context.Background(), adminMetadata())

	req := &walletv1.ManualTransferRequest{
		TenantId:       "tenant",
		IdempotencyKey: "manual-1",
		TransferType:   "bank_transfer",
		WalletId:       uuid.NewString(),
		Amount:         100,
		Currency:       "USD",
		Reason:         "test",
		RequestedBy:    10,
	}

	_, err := server.RequestManualTransfer(ctx, req)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", status.Code(err))
	}
	if status.Convert(err).Message() != walletstore.ErrInvalidTransferType.Error() {
		t.Fatalf("error = %v, want %v", err, walletstore.ErrInvalidTransferType)
	}
}

func TestRequestManualTransferPublicIdentityMustMatchRequester(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{WalletManualTransferApprovalTimeoutSeconds: 60},
	}
	server := NewServer(svc)

	md := metadata.Join(adminMetadata(), metadata.Pairs(
		"x-noebs-tenant-id", "tenant",
		"x-noebs-user-id", "42",
		"x-noebs-mobile", "0990000000",
	))
	ctx := walletServerMethodContext(metadata.NewIncomingContext(context.Background(), md), walletv1.WalletPublicService_RequestManualTransfer_FullMethodName)
	req := &walletv1.ManualTransferRequest{
		TenantId:       "tenant",
		IdempotencyKey: "manual-1",
		TransferType:   walletstore.ManualTransferTypeDebit,
		WalletId:       uuid.NewString(),
		Amount:         100,
		Currency:       "USD",
		Reason:         "test",
		RequestedBy:    7,
	}

	_, err := server.RequestManualTransfer(ctx, req)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
}

func TestRequestManualTransferRequiresAdminAuth(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{WalletManualTransferApprovalTimeoutSeconds: 60},
	}
	server := NewServer(svc)

	req := &walletv1.ManualTransferRequest{
		TenantId:       "tenant",
		IdempotencyKey: "manual-1",
		TransferType:   walletstore.ManualTransferTypeDebit,
		WalletId:       uuid.NewString(),
		Amount:         100,
		Currency:       "USD",
		Reason:         "test",
		RequestedBy:    7,
	}

	_, err := server.RequestManualTransfer(context.Background(), req)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
}

func TestSignalManualTransferDecisionRequiresReason(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)
	ctx := metadata.NewIncomingContext(context.Background(), adminMetadata())

	req := &walletv1.ManualTransferDecisionRequest{
		WorkflowId: "wf-1",
		Approved:   false,
		ApproverId: 22,
	}

	_, err := server.SignalManualTransferDecision(ctx, req)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", status.Code(err))
	}
}

func TestSignalManualTransferDecisionRequiresAdminAuth(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)

	req := &walletv1.ManualTransferDecisionRequest{
		WorkflowId:     "wf-1",
		Approved:       true,
		ApproverId:     22,
		ProofOfPayment: "proof",
	}

	_, err := server.SignalManualTransferDecision(context.Background(), req)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
}

func TestRequestManualTransferUsesDefaultTimeout(t *testing.T) {
	cfg := ebs_fields.NoebsConfig{
		WalletManualTransferApprovalTimeoutSeconds: 7200,
	}
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: cfg,
	}
	server := NewServer(svc)

	ctrl := gomock.NewController(t)
	mockTemporal := walletgrpcmock.NewMocktemporalClient(ctrl)
	server.TemporalClient = mockTemporal
	server.TemporalOptions = walletworker.Options{TaskQueue: walletworker.TaskQueueMain}
	ctx := metadata.NewIncomingContext(context.Background(), adminMetadata())

	run := stubWorkflowRun{id: "manual-wf", runID: "manual-run"}
	mockTemporal.EXPECT().
		ExecuteWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, opts client.StartWorkflowOptions, wf interface{}, args ...interface{}) (client.WorkflowRun, error) {
			params, ok := args[0].(walletworkflow.ManualTransferParams)
			if !ok {
				t.Fatalf("expected manual transfer params")
			}
			if params.ApprovalTimeoutSeconds != cfg.WalletManualTransferApprovalTimeoutSeconds {
				t.Fatalf("expected timeout %d, got %d", cfg.WalletManualTransferApprovalTimeoutSeconds, params.ApprovalTimeoutSeconds)
			}
			return run, nil
		})

	req := &walletv1.ManualTransferRequest{
		TenantId:       "tenant",
		IdempotencyKey: "manual-2",
		TransferType:   "manual_debit",
		WalletId:       uuid.NewString(),
		Amount:         100,
		Currency:       "USD",
		Reason:         "test",
		RequestedBy:    10,
	}

	if _, err := server.RequestManualTransfer(ctx, req); err != nil {
		t.Fatalf("request manual transfer: %v", err)
	}
}
