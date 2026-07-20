package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/grpc/metadata"
)

func TestWalletOutgoingContextPropagatesCompleteUserPrincipal(t *testing.T) {
	values := walletUserPrincipalHeaderValues(time.Now().UTC().Add(time.Hour))
	app := fiber.New()
	app.Get("/", gateway.InternalUserIdentityMiddleware(), func(c *fiber.Ctx) error {
		ctx, err := walletOutgoingContext(c, "tenant-1", 42)
		if err != nil {
			t.Fatalf("walletOutgoingContext() error = %v", err)
		}
		requireCompleteOutgoingPrincipal(t, ctx, values)
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	setWalletPrincipalHeaders(req, values)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer closeWalletResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestWalletOutgoingContextRejectsPrincipalMismatch(t *testing.T) {
	values := walletUserPrincipalHeaderValues(time.Now().UTC().Add(time.Hour))
	app := fiber.New()
	app.Get("/", gateway.InternalUserIdentityMiddleware(), func(c *fiber.Ctx) error {
		if _, err := walletOutgoingContext(c, "other-tenant", 42); err == nil {
			t.Fatal("walletOutgoingContext() accepted mismatched tenant")
		}
		if _, err := walletOutgoingContext(c, "tenant-1", 7); err == nil {
			t.Fatal("walletOutgoingContext() accepted mismatched user")
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	setWalletPrincipalHeaders(req, values)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer closeWalletResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestAdminOutgoingContextPropagatesCompleteOperatorPrincipal(t *testing.T) {
	values := walletOperatorPrincipalHeaderValues(time.Now().UTC().Add(time.Hour))
	app := fiber.New()
	app.Get("/", gateway.InternalPrincipalIdentityMiddleware(), func(c *fiber.Ctx) error {
		if err := authenticatedAdminIdentity(c); err != nil {
			t.Fatalf("authenticatedAdminIdentity() error = %v", err)
		}
		if err := requirePermission(c, tenantauth.PermissionWalletFeesWrite); err != nil {
			t.Fatalf("requirePermission() error = %v", err)
		}
		tenantID, err := authenticatedTenantID(c)
		if err != nil {
			t.Fatalf("authenticatedTenantID() error = %v", err)
		}
		ctx, err := adminOutgoingContext(c, tenantID)
		if err != nil {
			t.Fatalf("adminOutgoingContext() error = %v", err)
		}
		requireCompleteOutgoingPrincipal(t, ctx, values)
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	setWalletPrincipalHeaders(req, values)
	req.Header.Set(backofficeauth.HeaderCSRFToken, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer closeWalletResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func closeWalletResponseBody(t testing.TB, body io.Closer) {
	t.Helper()
	if err := body.Close(); err != nil {
		t.Errorf("close response body: %v", err)
	}
}

func TestOperatorAuthorizationRequiresRoleAndExactPermission(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	tests := []struct {
		name             string
		values           gateway.PrincipalHeaderValues
		wantAdminAllowed bool
		wantPermAllowed  bool
	}{
		{
			name:             "operator with exact permission",
			values:           walletOperatorPrincipalHeaderValues(expiresAt),
			wantAdminAllowed: true,
			wantPermAllowed:  true,
		},
		{
			name:             "operator with different permission",
			values:           walletOperatorPrincipalHeaderValues(expiresAt),
			wantAdminAllowed: true,
			wantPermAllowed:  false,
		},
		{
			name:             "user with permission",
			values:           walletUserPrincipalHeaderValues(expiresAt),
			wantAdminAllowed: false,
			wantPermAllowed:  true,
		},
	}
	tests[1].values.Permission = string(tenantauth.PermissionWalletRatesWrite)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", gateway.InternalPrincipalIdentityMiddleware(), func(c *fiber.Ctx) error {
				if got := authenticatedAdminIdentity(c) == nil; got != tc.wantAdminAllowed {
					t.Fatalf("admin allowed = %t, want %t", got, tc.wantAdminAllowed)
				}
				if got := requirePermission(c, tenantauth.PermissionWalletFeesWrite) == nil; got != tc.wantPermAllowed {
					t.Fatalf("permission allowed = %t, want %t", got, tc.wantPermAllowed)
				}
				return c.SendStatus(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			setWalletPrincipalHeaders(req, tc.values)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer closeWalletResponseBody(t, resp.Body)
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
			}
		})
	}
}

func walletUserPrincipalHeaderValues(expiresAt time.Time) gateway.PrincipalHeaderValues {
	return gateway.PrincipalHeaderValues{
		TenantID:        "tenant-1",
		Issuer:          "https://api.noebs.sd/auth/realms/noebs",
		Subject:         "afe669f8-f18a-4380-8624-c2a4ba30b9e3",
		OrganizationID:  "4e367332-8e4d-4a86-b0b7-8d5d5a48c40b",
		AuthorizedParty: "noebs-mobile",
		Roles:           string(tenantauth.RoleUser),
		Permission:      string(tenantauth.PermissionWalletFeesWrite),
		UserID:          "42",
		SourceIP:        "203.0.113.42",
		TokenExpiresAt:  strconv.FormatInt(expiresAt.Unix(), 10),
	}
}

func walletOperatorPrincipalHeaderValues(expiresAt time.Time) gateway.PrincipalHeaderValues {
	return gateway.PrincipalHeaderValues{
		TenantID:        "tenant-1",
		Issuer:          "https://api.noebs.sd/auth/realms/noebs",
		Subject:         "2b5c32ae-4785-4662-89fd-ec5b5d6dcf4d",
		OrganizationID:  "4e367332-8e4d-4a86-b0b7-8d5d5a48c40b",
		AuthorizedParty: "noebs-backoffice",
		Roles:           "backoffice,tenant-admin",
		Permission:      string(tenantauth.PermissionWalletFeesWrite),
		SourceIP:        "203.0.113.8",
		TokenExpiresAt:  strconv.FormatInt(expiresAt.Unix(), 10),
	}
}

func setWalletPrincipalHeaders(req *http.Request, values gateway.PrincipalHeaderValues) {
	req.Header.Set(gateway.GatewayTenantIDHeader, values.TenantID)
	req.Header.Set(gateway.GatewayIssuerHeader, values.Issuer)
	req.Header.Set(gateway.GatewaySubjectHeader, values.Subject)
	req.Header.Set(gateway.GatewayOrganizationIDHeader, values.OrganizationID)
	req.Header.Set(gateway.GatewayAuthorizedPartyHeader, values.AuthorizedParty)
	req.Header.Set(gateway.GatewayRolesHeader, values.Roles)
	req.Header.Set(gateway.GatewayPermissionHeader, values.Permission)
	req.Header.Set(gateway.GatewayUserIDHeader, values.UserID)
	req.Header.Set(gateway.GatewaySourceIPHeader, values.SourceIP)
	req.Header.Set(gateway.GatewayTokenExpiresAtHeader, values.TokenExpiresAt)
}

func requireCompleteOutgoingPrincipal(t *testing.T, ctx context.Context, values gateway.PrincipalHeaderValues) {
	t.Helper()
	requireOutgoingMetadata(t, ctx, gateway.GatewayTenantIDHeader, values.TenantID)
	requireOutgoingMetadata(t, ctx, gateway.GatewayIssuerHeader, values.Issuer)
	requireOutgoingMetadata(t, ctx, gateway.GatewaySubjectHeader, values.Subject)
	requireOutgoingMetadata(t, ctx, gateway.GatewayOrganizationIDHeader, values.OrganizationID)
	requireOutgoingMetadata(t, ctx, gateway.GatewayAuthorizedPartyHeader, values.AuthorizedParty)
	requireOutgoingMetadata(t, ctx, gateway.GatewayRolesHeader, values.Roles)
	requireOutgoingMetadata(t, ctx, gateway.GatewayPermissionHeader, values.Permission)
	requireOutgoingMetadata(t, ctx, gateway.GatewayUserIDHeader, values.UserID)
	requireOutgoingMetadata(t, ctx, gateway.GatewaySourceIPHeader, values.SourceIP)
	requireOutgoingMetadata(t, ctx, gateway.GatewayTokenExpiresAtHeader, values.TokenExpiresAt)
}

func requireOutgoingMetadata(t *testing.T, ctx context.Context, header, want string) {
	t.Helper()
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("outgoing metadata missing")
	}
	values := md.Get(header)
	if len(values) != 1 || values[0] != want {
		t.Fatalf("metadata %s = %v, want [%s]", header, values, want)
	}
}
