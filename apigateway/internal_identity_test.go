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
