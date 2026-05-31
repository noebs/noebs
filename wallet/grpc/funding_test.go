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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestCreateFundingSourceRequiresSourceDetails(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)

	_, err := server.CreateFundingSource(context.Background(), &walletv1.CreateFundingSourceRequest{
		TenantId:   "tenant",
		WalletId:   uuid.NewString(),
		SourceType: "bank_account",
		Currency:   "AED",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if status.Convert(err).Message() != walletstore.ErrMissingSourceDetails.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrMissingSourceDetails.Error())
	}
}

func TestCreateReturnToSourceDestinationRequiresLinkedFundingSource(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)
	details, err := structpb.NewStruct(map[string]any{"account_last4": "4321"})
	if err != nil {
		t.Fatalf("new struct: %v", err)
	}

	_, err = server.CreateWithdrawalDestination(context.Background(), &walletv1.CreateWithdrawalDestinationRequest{
		TenantId:           "tenant",
		WalletId:           uuid.NewString(),
		DestinationType:    "bank_account",
		Currency:           "AED",
		DestinationDetails: details,
		IsReturnToSource:   true,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if status.Convert(err).Message() != walletstore.ErrMissingFundingSourceID.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrMissingFundingSourceID.Error())
	}
}

func TestResetWalletPINRequiresAdminAuth(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)

	_, err := server.ResetWalletPIN(context.Background(), &walletv1.ResetWalletPINRequest{
		TenantId: "tenant",
		WalletId: uuid.NewString(),
		AdminId:  42,
		NewPin:   "1234",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
}

func TestResetWalletPINValidatesAfterAdminAuth(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)
	ctx := metadata.NewIncomingContext(context.Background(), adminMetadata())

	_, err := server.ResetWalletPIN(ctx, &walletv1.ResetWalletPINRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if status.Convert(err).Message() != walletstore.ErrMissingTenantID.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrMissingTenantID.Error())
	}
}
