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

func TestRequestDepositDoesNotGenerateProviderTransactionID(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)

	req := &walletv1.DepositRequest{
		TenantId:        "tenant",
		ClientReference: "ref-1",
		ProviderCode:    "noop",
		WalletId:        uuid.NewString(),
		OwnerType:       "user",
		OwnerId:         "1",
		Amount:          100,
		Currency:        "USD",
	}

	_, err := server.RequestDeposit(context.Background(), req)
	if err == nil {
		t.Fatalf("expected temporal precondition error")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition, got %v", status.Code(err))
	}
	if req.PspTransactionId != "" {
		t.Fatalf("expected provider transaction id to remain absent, got %q", req.PspTransactionId)
	}
}
