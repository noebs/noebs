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
