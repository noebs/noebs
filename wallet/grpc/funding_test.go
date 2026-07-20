package walletgrpc

import (
	"context"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCreateReturnToSourceDestinationRequiresLinkedFundingSource(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)
	_, err := server.CreateWithdrawalDestination(context.Background(), &walletv1.CreateWithdrawalDestinationRequest{
		TenantId: "tenant",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if status.Convert(err).Message() != walletstore.ErrMissingFundingSourceID.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrMissingFundingSourceID.Error())
	}
}

func TestCreateWithdrawalDestinationRequestCannotSubstituteAccountDetails(t *testing.T) {
	fields := (&walletv1.CreateWithdrawalDestinationRequest{}).ProtoReflect().Descriptor().Fields()
	if fields.ByName("destination_details") != nil || fields.ByName("wallet_id") != nil {
		t.Fatal("destination request exposes source-owned account fields")
	}
}
