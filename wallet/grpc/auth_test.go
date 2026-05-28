package walletgrpc

import (
	"context"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/wallet"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestClaimsFromContextValidatesGatewayTenant(t *testing.T) {
	server := NewServer(&wallet.Service{})
	cases := []struct {
		name string
		md   metadata.MD
	}{
		{"missing_tenant", metadata.Pairs(
			gateway.GatewayUserIDHeader, "42",
			gateway.GatewayMobileHeader, "0990000000",
		)},
		{"reserved_tenant", metadata.Pairs(
			gateway.GatewayTenantIDHeader, "default",
			gateway.GatewayUserIDHeader, "42",
			gateway.GatewayMobileHeader, "0990000000",
		)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), tc.md)
			_, err := server.claimsFromContext(ctx)
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.Unauthenticated)
			}
		})
	}
}

func TestBindTenantToClaimsValidatesRequestAndGatewayTenant(t *testing.T) {
	claims := &gateway.TokenClaims{TenantID: "tenant-a", UserID: 42}
	tenantID, err := bindTenantToClaims("", claims)
	if err != nil {
		t.Fatalf("bindTenantToClaims() error = %v", err)
	}
	if tenantID != claims.TenantID {
		t.Fatalf("tenantID = %q, want %q", tenantID, claims.TenantID)
	}

	if _, err := bindTenantToClaims("default", claims); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("reserved request tenant code = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if _, err := bindTenantToClaims("tenant-b", claims); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("mismatched tenant code = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
	if _, err := bindTenantToClaims("tenant-a", &gateway.TokenClaims{TenantID: "default", UserID: 42}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("reserved gateway tenant code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}
