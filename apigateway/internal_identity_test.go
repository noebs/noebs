package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/gofiber/fiber/v2"
)

func TestInternalTenantIdentityMiddlewareStoresGatewaySource(t *testing.T) {
	app := fiber.New()
	app.Get("/", InternalTenantIdentityMiddleware(), func(c *fiber.Ctx) error {
		if got := c.Locals("request_source"); got != "203.0.113.8" {
			t.Fatalf("request_source local = %#v, want 203.0.113.8", got)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(GatewayTenantIDHeader, "tenant")
	req.Header.Set(GatewaySourceIPHeader, "203.0.113.8")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer closeTestResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestInternalTenantIdentityMiddlewareRejectsInvalidGatewaySource(t *testing.T) {
	app := fiber.New()
	app.Get("/", InternalTenantIdentityMiddleware(), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(GatewayTenantIDHeader, "tenant")
	req.Header.Set(GatewaySourceIPHeader, "not-an-ip")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer closeTestResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestInternalPrincipalIdentityMiddlewareStoresCompleteTypedPrincipal(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	values := operatorPrincipalHeaderValues(expiresAt)

	app := fiber.New()
	app.Post("/", InternalPrincipalIdentityMiddleware(), func(c *fiber.Ctx) error {
		principal, ok := InternalPrincipalIdentity(c)
		if !ok {
			t.Fatal("typed principal is missing")
		}
		if principal.TenantID != values.TenantID ||
			principal.Issuer != values.Issuer ||
			principal.Subject != values.Subject ||
			principal.OrganizationID != values.OrganizationID ||
			principal.AuthorizedParty != values.AuthorizedParty ||
			principal.UserID != 0 ||
			principal.SourceIP != values.SourceIP ||
			!principal.TokenExpiresAt.Equal(expiresAt) {
			t.Fatalf("principal = %+v, want complete principal from %+v", principal, values)
		}
		wantRoles := []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleTenantAdmin}
		if got := principal.Roles(); !slices.Equal(got, wantRoles) {
			t.Fatalf("roles = %v, want %v", got, wantRoles)
		}
		if got := principal.Permission(); got != tenantauth.PermissionWalletFeesWrite {
			t.Fatalf("permission = %q, want %q", got, tenantauth.PermissionWalletFeesWrite)
		}
		if got := principal.HeaderValues(); got != values {
			t.Fatalf("HeaderValues() = %+v, want %+v", got, values)
		}
		if got := c.Locals("tenant_id"); got != values.TenantID {
			t.Fatalf("tenant_id local = %#v, want %q", got, values.TenantID)
		}
		if got := c.Locals("request_source"); got != values.SourceIP {
			t.Fatalf("request_source local = %#v, want %q", got, values.SourceIP)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	setPrincipalHeaders(req, values)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer closeTestResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestInternalPrincipalIdentityMiddlewareRejectsIncompletePrincipal(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	complete := operatorPrincipalHeaderValues(expiresAt)

	cases := []struct {
		name   string
		mutate func(*PrincipalHeaderValues)
	}{
		{"tenant", func(values *PrincipalHeaderValues) { values.TenantID = "" }},
		{"issuer", func(values *PrincipalHeaderValues) { values.Issuer = "" }},
		{"subject", func(values *PrincipalHeaderValues) { values.Subject = "" }},
		{"organization", func(values *PrincipalHeaderValues) { values.OrganizationID = "" }},
		{"authorized party", func(values *PrincipalHeaderValues) { values.AuthorizedParty = "" }},
		{"roles", func(values *PrincipalHeaderValues) { values.Roles = "" }},
		{"source", func(values *PrincipalHeaderValues) { values.SourceIP = "" }},
		{"expiry", func(values *PrincipalHeaderValues) { values.TokenExpiresAt = "" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := complete
			tc.mutate(&values)
			assertPrincipalMiddlewareUnauthorized(t, values)
		})
	}
}

func TestInternalPrincipalIdentityMiddlewareRejectsInvalidAuthority(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	complete := operatorPrincipalHeaderValues(expiresAt)

	cases := []struct {
		name   string
		mutate func(*PrincipalHeaderValues)
	}{
		{"http issuer", func(values *PrincipalHeaderValues) { values.Issuer = "http://identity.example/realms/noebs" }},
		{"unknown role", func(values *PrincipalHeaderValues) { values.Roles = "operator" }},
		{"unmodeled realm role", func(values *PrincipalHeaderValues) { values.Roles = "platform-admin" }},
		{"unsorted roles", func(values *PrincipalHeaderValues) { values.Roles = "tenant-admin,backoffice" }},
		{"duplicate roles", func(values *PrincipalHeaderValues) { values.Roles = "backoffice,backoffice" }},
		{"unknown permission", func(values *PrincipalHeaderValues) { values.Permission = "wallet:all" }},
		{"invalid user", func(values *PrincipalHeaderValues) { values.UserID = "042" }},
		{"invalid source", func(values *PrincipalHeaderValues) { values.SourceIP = "203.0.113.008" }},
		{"expired", func(values *PrincipalHeaderValues) {
			values.TokenExpiresAt = strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := complete
			tc.mutate(&values)
			assertPrincipalMiddlewareUnauthorized(t, values)
		})
	}
}

func TestInternalUserIdentityMiddlewareStoresUserPrincipal(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	values := userPrincipalHeaderValues(expiresAt)

	app := fiber.New()
	app.Get("/", InternalUserIdentityMiddleware(), func(c *fiber.Ctx) error {
		principal, ok := InternalPrincipalIdentity(c)
		if !ok {
			t.Fatal("typed principal is missing")
		}
		if principal.UserID != 42 || !principal.HasRole(tenantauth.RoleUser) {
			t.Fatalf("principal = %+v, want user 42 with user role", principal)
		}
		if got := c.Locals("tenant_id"); got != "tenant" {
			t.Fatalf("tenant_id local = %#v, want tenant", got)
		}
		if got := c.Locals("user_id"); got != int64(42) {
			t.Fatalf("user_id local = %#v, want 42", got)
		}
		if got := c.Locals("request_source"); got != "203.0.113.42" {
			t.Fatalf("request_source local = %#v, want 203.0.113.42", got)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	setPrincipalHeaders(req, values)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer closeTestResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestInternalUserIdentityMiddlewareRejectsOperatorAndUnprojectedUser(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	operator := operatorPrincipalHeaderValues(expiresAt)
	userWithoutProjection := userPrincipalHeaderValues(expiresAt)
	userWithoutProjection.UserID = ""

	for name, values := range map[string]PrincipalHeaderValues{
		"operator":        operator,
		"missing user id": userWithoutProjection,
	} {
		t.Run(name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", InternalUserIdentityMiddleware(), func(c *fiber.Ctx) error {
				return c.SendStatus(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			setPrincipalHeaders(req, values)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer closeTestResponseBody(t, resp.Body)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}
}

func operatorPrincipalHeaderValues(expiresAt time.Time) PrincipalHeaderValues {
	return PrincipalHeaderValues{
		TenantID:        "tenant",
		Issuer:          "https://api.noebs.sd/auth/realms/noebs",
		Subject:         "b754ff9d-92a9-43a4-95d1-194b27db28db",
		OrganizationID:  "2f54b568-10a8-47fa-9f17-1d4bc519dc86",
		AuthorizedParty: "noebs-backoffice",
		Roles:           "backoffice,tenant-admin",
		Permission:      string(tenantauth.PermissionWalletFeesWrite),
		SourceIP:        "203.0.113.8",
		TokenExpiresAt:  strconv.FormatInt(expiresAt.Unix(), 10),
	}
}

func userPrincipalHeaderValues(expiresAt time.Time) PrincipalHeaderValues {
	return PrincipalHeaderValues{
		TenantID:        "tenant",
		Issuer:          "https://api.noebs.sd/auth/realms/noebs",
		Subject:         "f0f6aa13-f4d3-4f5d-987d-2581873f003b",
		OrganizationID:  "2f54b568-10a8-47fa-9f17-1d4bc519dc86",
		AuthorizedParty: "noebs-mobile",
		Roles:           string(tenantauth.RoleUser),
		Permission:      string(tenantauth.PermissionWalletRead),
		UserID:          "42",
		SourceIP:        "203.0.113.42",
		TokenExpiresAt:  strconv.FormatInt(expiresAt.Unix(), 10),
	}
}

func assertPrincipalMiddlewareUnauthorized(t *testing.T, values PrincipalHeaderValues) {
	t.Helper()
	app := fiber.New()
	app.Post("/", InternalPrincipalIdentityMiddleware(), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	setPrincipalHeaders(req, values)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer closeTestResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func closeTestResponseBody(t testing.TB, body io.Closer) {
	t.Helper()
	if err := body.Close(); err != nil {
		t.Errorf("close response body: %v", err)
	}
}

func setPrincipalHeaders(req *http.Request, values PrincipalHeaderValues) {
	req.Header.Set(GatewayTenantIDHeader, values.TenantID)
	req.Header.Set(GatewayIssuerHeader, values.Issuer)
	req.Header.Set(GatewaySubjectHeader, values.Subject)
	req.Header.Set(GatewayOrganizationIDHeader, values.OrganizationID)
	req.Header.Set(GatewayAuthorizedPartyHeader, values.AuthorizedParty)
	req.Header.Set(GatewayRolesHeader, values.Roles)
	req.Header.Set(GatewayPermissionHeader, values.Permission)
	req.Header.Set(GatewayUserIDHeader, values.UserID)
	req.Header.Set(GatewaySourceIPHeader, values.SourceIP)
	req.Header.Set(GatewayTokenExpiresAtHeader, values.TokenExpiresAt)
}
