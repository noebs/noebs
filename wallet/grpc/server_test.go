package walletgrpc

import (
	"context"
	"testing"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestEnsureWalletRequiresExplicitCurrency(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})

	_, err := server.EnsureWallet(context.Background(), &walletv1.EnsureWalletRequest{
		TenantId: "tenant",
		UserId:   42,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if status.Convert(err).Message() != walletstore.ErrMissingCurrency.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrMissingCurrency.Error())
	}
}
