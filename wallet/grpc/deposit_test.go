package walletgrpc

import (
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDepositIntentReferencesAreServerGenerated(t *testing.T) {
	first, err := newDepositIntentReference()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newDepositIntentReference()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) != 47 || first[:4] != "dep_" {
		t.Fatalf("deposit references = %q, %q", first, second)
	}
}

func TestRequestDepositRequiresReachableStore(t *testing.T) {
	svc := &wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	}
	server := NewServer(svc)

	req := &walletv1.RequestDepositRequest{
		TenantId:       "tenant",
		IdempotencyKey: "ref-1",
		ProviderCode:   "noop",
		WalletId:       uuid.NewString(),
		Amount:         100,
		Currency:       "USD",
	}

	_, err := server.RequestDeposit(walletGatewayIdentityContext(1, "tenant"), req)
	if err == nil {
		t.Fatalf("expected temporal precondition error")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition, got %v", status.Code(err))
	}
}

func TestRequestDepositRequiresExplicitIdempotencyAndNonNilWallet(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	req := &walletv1.RequestDepositRequest{
		TenantId:     "tenant",
		ProviderCode: "noop",
		WalletId:     uuid.NewString(),
		Amount:       100,
		Currency:     "USD",
	}

	_, err := server.RequestDeposit(walletGatewayIdentityContext(1, "tenant"), req)
	if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != walletstore.ErrMissingIdempotencyKey.Error() {
		t.Fatalf("missing idempotency error = %v", err)
	}

	req.IdempotencyKey = "ref-1"
	req.WalletId = uuid.Nil.String()
	_, err = server.RequestDeposit(walletGatewayIdentityContext(1, "tenant"), req)
	if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != walletstore.ErrMissingWalletID.Error() {
		t.Fatalf("nil wallet error = %v", err)
	}
}
