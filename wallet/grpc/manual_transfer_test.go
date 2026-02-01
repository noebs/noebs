package walletgrpc

import (
	"context"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRequestManualTransferRequiresTimeout(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)

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

	_, err := server.RequestManualTransfer(context.Background(), req)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", status.Code(err))
	}
}

func TestSignalManualTransferDecisionRequiresReason(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)

	req := &walletv1.ManualTransferDecisionRequest{
		WorkflowId: "wf-1",
		Approved:   false,
		ApproverId: 22,
	}

	_, err := server.SignalManualTransferDecision(context.Background(), req)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", status.Code(err))
	}
}
