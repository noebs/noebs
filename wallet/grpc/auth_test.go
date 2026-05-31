package walletgrpc

import (
	"context"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	"google.golang.org/grpc"
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

func TestClaimsForRPCRequiresGatewayIdentityOnPublicUserMethods(t *testing.T) {
	server := NewServer(&wallet.Service{})

	_, err := server.claimsForRPC(walletServerMethodContext(context.Background(), walletv1.WalletPublicService_RequestP2PTransfer_FullMethodName))
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("public user method status = %v, want %v", status.Code(err), codes.Unauthenticated)
	}

	claims, err := server.claimsForRPC(walletServerMethodContext(walletGatewayIdentityContext(42, "tenant-a"), walletv1.WalletPublicService_RequestP2PTransfer_FullMethodName))
	if err != nil {
		t.Fatalf("public user method with identity error = %v", err)
	}
	if claims == nil || claims.UserID != 42 || claims.TenantID != "tenant-a" {
		t.Fatalf("claims = %+v, want gateway identity", claims)
	}

	claims, err = server.claimsForRPC(walletServerMethodContext(context.Background(), walletv1.WalletPublicService_RequestManualTransfer_FullMethodName))
	if err != nil {
		t.Fatalf("public admin method should be handled by admin auth, got %v", err)
	}
	if claims != nil {
		t.Fatalf("public admin method claims = %+v, want nil without user identity", claims)
	}

	claims, err = server.claimsForRPC(walletServerMethodContext(context.Background(), walletv1.WalletInternalService_RequestP2PTransfer_FullMethodName))
	if err != nil {
		t.Fatalf("internal method error = %v", err)
	}
	if claims != nil {
		t.Fatalf("internal method claims = %+v, want nil without gateway identity", claims)
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

type walletTestServerTransportStream struct {
	method string
}

func (s walletTestServerTransportStream) Method() string {
	return s.method
}

func (s walletTestServerTransportStream) SetHeader(metadata.MD) error {
	return nil
}

func (s walletTestServerTransportStream) SendHeader(metadata.MD) error {
	return nil
}

func (s walletTestServerTransportStream) SetTrailer(metadata.MD) error {
	return nil
}

func walletServerMethodContext(ctx context.Context, method string) context.Context {
	return grpc.NewContextWithServerTransportStream(ctx, walletTestServerTransportStream{method: method})
}
