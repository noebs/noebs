package walletgrpc

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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
	ctx := metadata.NewIncomingContext(context.Background(), adminMetadata())

	_, err := server.RenderWalletAdmin(ctx, &walletv1.AdminWalletRequest{
		Action: walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_DASHBOARD,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if status.Convert(err).Message() != walletstore.ErrMissingTenantID.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrMissingTenantID.Error())
	}
}

func TestRenderWalletAdminRequiresAdminAuth(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})

	_, err := server.RenderWalletAdmin(context.Background(), &walletv1.AdminWalletRequest{
		Action: walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_DASHBOARD,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
}

func TestRenderWalletAdminUsesGatewayTenantMetadata(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})

	resp, err := server.RenderWalletAdmin(walletAdminTenantContext("context-tenant"), &walletv1.AdminWalletRequest{
		Action: walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_DASHBOARD,
		Query: map[string]string{
			"tenant_id": "default",
		},
	})
	if err != nil {
		t.Fatalf("RenderWalletAdmin() error = %v", err)
	}
	if resp.GetStatusCode() != 200 {
		t.Fatalf("status = %d, want 200", resp.GetStatusCode())
	}
	if !strings.Contains(string(resp.GetBody()), "context-tenant") {
		t.Fatalf("admin dashboard body does not contain gateway tenant")
	}
}

func TestRenderWalletAdminDecisionRequiresExplicitTenant(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	ctx := metadata.NewIncomingContext(context.Background(), adminMetadata())

	_, err := server.RenderWalletAdmin(ctx, &walletv1.AdminWalletRequest{
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

func TestAdminBoolRejectsMalformedValues(t *testing.T) {
	_, err := adminBool(map[string]string{"active_only": "maybe"}, "active_only")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if !strings.Contains(status.Convert(err).Message(), "active_only") {
		t.Fatalf("status message = %q, want field name", status.Convert(err).Message())
	}
}

func TestAdminLimitOffsetUsesTypedValidation(t *testing.T) {
	limit, offset, err := adminLimitOffset(map[string]string{"limit": "25", "offset": "5"}, 50)
	if err != nil {
		t.Fatalf("adminLimitOffset() error = %v", err)
	}
	if limit != 25 || offset != 5 {
		t.Fatalf("adminLimitOffset() = %d, %d; want 25, 5", limit, offset)
	}
	if _, _, err := adminLimitOffset(map[string]string{"limit": "0"}, 50); status.Convert(err).Message() != walletstore.ErrInvalidLimit.Error() {
		t.Fatalf("adminLimitOffset(invalid limit) error = %v, want %v", err, walletstore.ErrInvalidLimit)
	}
	if _, _, err := adminLimitOffset(map[string]string{"offset": "-1"}, 50); status.Convert(err).Message() != walletstore.ErrInvalidOffset.Error() {
		t.Fatalf("adminLimitOffset(invalid offset) error = %v, want %v", err, walletstore.ErrInvalidOffset)
	}
}

func TestAdminWithdrawalApprovalDecodesRawRequest(t *testing.T) {
	item, err := adminWithdrawalApproval(walletstore.PSPTransaction{
		ClientReference: "withdrawal-1",
		WorkflowID:      sql.NullString{String: "workflow-1", Valid: true},
		Amount:          1000,
		Currency:        "AED",
		PSPProvider:     "provider",
		Status:          "held",
		RawRequest:      walletstore.RawJSON(`{"wallet_id":"wallet-1","owner_type":"user","owner_id":"42","destination_id":7,"approval_required":true}`),
	})
	if err != nil {
		t.Fatalf("adminWithdrawalApproval() error = %v", err)
	}
	if item.WalletID != "wallet-1" || item.OwnerType != "user" || item.OwnerID != "42" || item.DestinationID != 7 || !item.ApprovalNeeded {
		t.Fatalf("decoded item = %+v", item)
	}
}

func TestAdminWithdrawalApprovalRejectsMalformedRawRequest(t *testing.T) {
	_, err := adminWithdrawalApproval(walletstore.PSPTransaction{
		ClientReference: "withdrawal-1",
		WorkflowID:      sql.NullString{String: "workflow-1", Valid: true},
		RawRequest:      walletstore.RawJSON(`{`),
	})
	if err == nil {
		t.Fatal("adminWithdrawalApproval() error = nil, want malformed raw request error")
	}
	if !strings.Contains(err.Error(), "withdrawal-1") {
		t.Fatalf("adminWithdrawalApproval() error = %v, want client reference context", err)
	}
}

func walletAdminTenantContext(tenantID string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		strings.ToLower(gateway.GatewayTenantIDHeader), tenantID,
		strings.ToLower(gateway.GatewayAdminIdentityHeader), gateway.GatewayAdminIdentityValue,
	))
}
