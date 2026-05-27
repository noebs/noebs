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

func TestRenderWalletAdminRequiresExplicitTenant(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})

	_, err := server.RenderWalletAdmin(context.Background(), &walletv1.AdminWalletRequest{
		Action: walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_DASHBOARD,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if status.Convert(err).Message() != walletstore.ErrMissingTenantID.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrMissingTenantID.Error())
	}
}

func TestRenderWalletAdminDecisionRequiresExplicitTenant(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})

	_, err := server.RenderWalletAdmin(context.Background(), &walletv1.AdminWalletRequest{
		Action: walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_APPROVE_TRANSFER,
		Path: map[string]string{
			"workflow_id": "manual-transfer-workflow",
		},
		Form: map[string]string{
			"kind":             "manual_transfer",
			"approver_id":      "42",
			"proof_of_payment": "proof",
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if status.Convert(err).Message() != walletstore.ErrMissingTenantID.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrMissingTenantID.Error())
	}
}

func TestAdminCurrencyRequiresExplicitCurrency(t *testing.T) {
	if _, err := adminCurrency(" "); err != walletstore.ErrMissingCurrency {
		t.Fatalf("adminCurrency() error = %v, want %v", err, walletstore.ErrMissingCurrency)
	}
}
