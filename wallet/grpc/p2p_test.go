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
