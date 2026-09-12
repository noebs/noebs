package walletgrpc

import (
	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

func TestTransactionResolutionBoundaryUsesAuthenticatedActorAndExplicitVersion(t *testing.T) {
	req := &walletv1.RenderWalletAdminRequest{Path: map[string]string{"client_reference": "reference-1"}, Form: map[string]string{"tenant_id": "attacker-tenant", "operator_id": "999", "idempotency_key": "stable-command", "expected_version": "4", "status": "success", "fulfillment_method": "offline", "reason": "receipt reviewed", "evidence_reference": "receipt-1", "settlement_reference": "settled-1"}}
	command, err := adminTransactionResolution(req, "tenant-a", walletstore.OperatorIdentity{ID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if command.TenantID != "tenant-a" || command.OperatorID != 7 || command.ExpectedVersion != 4 {
		t.Fatalf("authority/version=%+v", command)
	}
	for _, field := range []string{"idempotency_key", "expected_version", "status", "fulfillment_method", "reason", "evidence_reference", "settlement_reference"} {
		original := req.Form[field]
		req.Form[field] = ""
		if _, err := adminTransactionResolution(req, "tenant-a", walletstore.OperatorIdentity{ID: 7}); err == nil {
			t.Errorf("missing %s accepted", field)
		}
		req.Form[field] = original
	}
	if _, err := adminTransactionResolution(req, "", walletstore.OperatorIdentity{ID: 7}); err == nil {
		t.Error("missing tenant accepted")
	}
	if _, err := adminTransactionResolution(req, "tenant-a", walletstore.OperatorIdentity{}); err == nil {
		t.Error("missing authenticated operator accepted")
	}
	req.Form["status"] = "failed"
	if _, err := adminTransactionResolution(req, "tenant-a", walletstore.OperatorIdentity{ID: 7}); err == nil {
		t.Error("failed outcome with settlement accepted")
	}
}

func TestResolveTransactionRequiresExactPermissionAndTenantAdmin(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	permission := tenantauth.PermissionWalletTransactionResolve
	for _, tc := range []struct {
		role       tenantauth.Role
		permission tenantauth.Permission
		want       codes.Code
	}{
		{tenantauth.RoleTenantAdmin, permission, codes.OK},
		{tenantauth.RoleBackoffice, permission, codes.PermissionDenied},
		{tenantauth.RoleTenantAdmin, tenantauth.PermissionWalletWorkflowApprove, codes.PermissionDenied},
	} {
		md := setPrincipalMetadata(operatorMetadata(tc.permission), gateway.GatewayRolesHeader, string(tc.role))
		_, err := server.requireAdminPermission(md, permission)
		if status.Code(err) != tc.want {
			t.Errorf("role=%s permission=%s code=%s", tc.role, tc.permission, status.Code(err))
		}
	}
	if !isAdminWalletMutation(walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_RESOLVE_TRANSACTION) || adminWalletActionPermission(walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_RESOLVE_TRANSACTION) != permission {
		t.Fatal("resolution action is not protected as mutation")
	}
}
