package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestInternalAdminIdentityMiddlewareStoresValidatedIdentity(t *testing.T) {
	app := fiber.New()
	app.Get("/", InternalAdminIdentityMiddleware(), func(c *fiber.Ctx) error {
		authenticated, ok := c.Locals("admin_identity").(bool)
		if !ok || !authenticated {
			t.Fatalf("admin_identity local = %#v, want true", c.Locals("admin_identity"))
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(GatewayAdminIdentityHeader, GatewayAdminIdentityValue)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

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
	_ = resp.Body.Close()
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestInternalPrincipalIdentityMiddlewareStoresTypedPrincipal(t *testing.T) {
	app := fiber.New()
	app.Post("/", InternalPrincipalIdentityMiddleware(), func(c *fiber.Ctx) error {
		principal, ok := InternalPrincipalIdentity(c)
		if !ok {
			t.Fatal("typed principal is missing")
		}
		want := PrincipalIdentity{
			TenantID: "tenant",
			Issuer:   "https://identity.example/realms/noebs",
			Subject:  "b754ff9d-92a9-43a4-95d1-194b27db28db",
		}
		if principal != want {
			t.Fatalf("principal = %+v, want %+v", principal, want)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(GatewayTenantIDHeader, "tenant")
	req.Header.Set(GatewayIssuerHeader, "https://identity.example/realms/noebs")
	req.Header.Set(GatewaySubjectHeader, "b754ff9d-92a9-43a4-95d1-194b27db28db")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestInternalPrincipalIdentityMiddlewareRejectsIncompletePrincipal(t *testing.T) {
	app := fiber.New()
	app.Post("/", InternalPrincipalIdentityMiddleware(), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusNoContent)
	})

	for _, header := range []string{GatewayTenantIDHeader, GatewayIssuerHeader, GatewaySubjectHeader} {
		t.Run(header, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set(GatewayTenantIDHeader, "tenant")
			req.Header.Set(GatewayIssuerHeader, "https://identity.example/realms/noebs")
			req.Header.Set(GatewaySubjectHeader, "subject")
			req.Header.Del(header)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}
}

func TestInternalUserIdentityMiddlewareStoresUserIdentity(t *testing.T) {
	app := fiber.New()
	app.Get("/", InternalUserIdentityMiddleware(), func(c *fiber.Ctx) error {
		if got := c.Locals("tenant_id"); got != "tenant" {
			t.Fatalf("tenant_id local = %#v, want tenant", got)
		}
		if got := c.Locals("user_id"); got != int64(42) {
			t.Fatalf("user_id local = %#v, want 42", got)
		}
		if got := c.Locals("mobile"); got != "0990000000" {
			t.Fatalf("mobile local = %#v, want 0990000000", got)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(GatewayTenantIDHeader, "tenant")
	req.Header.Set(GatewayUserIDHeader, "42")
	req.Header.Set(GatewayMobileHeader, "0990000000")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}
