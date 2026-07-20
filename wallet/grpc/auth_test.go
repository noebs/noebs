package walletgrpc

import (
	"context"
	"strconv"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/tenantauth"
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
		{"missing tenant", deletePrincipalMetadata(userMetadata(42, "tenant-a"), gateway.GatewayTenantIDHeader)},
		{"reserved tenant", setPrincipalMetadata(userMetadata(42, "tenant-a"), gateway.GatewayTenantIDHeader, "default")},
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

func TestPrincipalFromMetadataParsesCompleteV2Identity(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	expiresAt := now.Add(5 * time.Minute)
	md := principalMetadataFixture{
		tenantID:        "tenant-a",
		issuer:          "https://api.noebs.sd/auth/realms/noebs",
		subject:         "keycloak-subject",
		organizationID:  "org-tenant-a",
		authorizedParty: "noebs-mobile",
		roles:           string(tenantauth.RoleUser),
		permission:      string(tenantauth.PermissionWalletRead),
		userID:          "42",
		sourceIP:        "203.0.113.10",
		expiresAt:       expiresAt,
	}.metadata()

	principal, err := principalFromMetadata(md, now)
	if err != nil {
		t.Fatalf("principalFromMetadata() error = %v", err)
	}
	if principal == nil {
		t.Fatal("principalFromMetadata() = nil")
	}
	if principal.TenantID != "tenant-a" || principal.Issuer != "https://api.noebs.sd/auth/realms/noebs" ||
		principal.Subject != "keycloak-subject" || principal.OrganizationID != "org-tenant-a" ||
		principal.AuthorizedParty != "noebs-mobile" || principal.UserID != 42 ||
		principal.SourceIP != "203.0.113.10" || !principal.TokenExpiresAt.Equal(expiresAt) {
		t.Fatalf("principal = %+v, want complete V2 identity", principal)
	}
	if !principal.HasRole(tenantauth.RoleUser) || principal.Permission() != tenantauth.PermissionWalletRead {
		t.Fatalf("roles = %v permission = %q", principal.Roles(), principal.Permission())
	}
}

func TestPrincipalFromMetadataRejectsIncompleteDuplicateAndExpiredIdentity(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()
	valid := principalMetadataFixture{
		tenantID:        "tenant-a",
		issuer:          "https://api.noebs.sd/auth/realms/noebs",
		subject:         "keycloak-subject",
		organizationID:  "org-tenant-a",
		authorizedParty: "noebs-mobile",
		roles:           string(tenantauth.RoleUser),
		permission:      string(tenantauth.PermissionWalletRead),
		userID:          "42",
		sourceIP:        "203.0.113.10",
		expiresAt:       now.Add(5 * time.Minute),
	}.metadata()
	duplicateTenant := valid.Copy()
	duplicateTenant.Append(gateway.GatewayTenantIDHeader, "tenant-b")

	tests := []struct {
		name string
		md   metadata.MD
	}{
		{"missing issuer", deletePrincipalMetadata(valid, gateway.GatewayIssuerHeader)},
		{"missing subject", deletePrincipalMetadata(valid, gateway.GatewaySubjectHeader)},
		{"missing organization", deletePrincipalMetadata(valid, gateway.GatewayOrganizationIDHeader)},
		{"missing authorized party", deletePrincipalMetadata(valid, gateway.GatewayAuthorizedPartyHeader)},
		{"missing roles", deletePrincipalMetadata(valid, gateway.GatewayRolesHeader)},
		{"missing source IP", deletePrincipalMetadata(valid, gateway.GatewaySourceIPHeader)},
		{"missing expiry", deletePrincipalMetadata(valid, gateway.GatewayTokenExpiresAtHeader)},
		{"duplicate tenant", duplicateTenant},
		{"expired", setPrincipalMetadata(valid, gateway.GatewayTokenExpiresAtHeader, strconv.FormatInt(now.Unix(), 10))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := principalFromMetadata(tt.md, now)
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

	_, err = server.claimsForRPC(walletServerMethodContext(
		metadata.NewIncomingContext(context.Background(), operatorMetadata(tenantauth.PermissionWalletRead)),
		walletv1.WalletPublicService_RequestP2PTransfer_FullMethodName,
	))
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("public user method with operator status = %v, want %v", status.Code(err), codes.Unauthenticated)
	}

}

func TestBindTenantToClaimsValidatesRequestAndGatewayTenant(t *testing.T) {
	claims := mustPrincipal(t, userMetadata(42, "tenant-a"))
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
	invalidClaims := *claims
	invalidClaims.TenantID = "default"
	if _, err := bindTenantToClaims("tenant-a", &invalidClaims); status.Code(err) != codes.Unauthenticated {
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
