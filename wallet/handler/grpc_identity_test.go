package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/grpc/metadata"
)

func TestWalletOutgoingContextUsesValidatedIdentityLocals(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		c.Locals("tenant_id", "tenant_1")
		c.Locals("user_id", int64(42))
		c.Locals("mobile", "0912345678")

		ctx := walletOutgoingContext(c, "tenant_1", 42)
		requireOutgoingMetadata(t, ctx, gateway.GatewayTenantIDHeader, "tenant_1")
		requireOutgoingMetadata(t, ctx, gateway.GatewayUserIDHeader, "42")
		requireOutgoingMetadata(t, ctx, gateway.GatewayMobileHeader, "0912345678")
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, "spoofed-tenant")
	req.Header.Set(gateway.GatewayUserIDHeader, "7")
	req.Header.Set(gateway.GatewayMobileHeader, "0999999999")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestAdminOutgoingContextUsesValidatedIdentityLocals(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		c.Locals("admin_identity", true)
		c.Locals("tenant_id", "tenant_1")
		if err := authenticatedAdminIdentity(c); err != nil {
			t.Fatalf("authenticatedAdminIdentity() error = %v", err)
		}
		tenantID, err := authenticatedTenantID(c)
		if err != nil {
			t.Fatalf("authenticatedTenantID() error = %v", err)
		}

		ctx := adminOutgoingContext(c, tenantID)
		requireOutgoingMetadata(t, ctx, gateway.GatewayAdminIdentityHeader, gateway.GatewayAdminIdentityValue)
		requireOutgoingMetadata(t, ctx, gateway.GatewayTenantIDHeader, "tenant_1")
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayAdminIdentityHeader, "spoofed-admin")
	req.Header.Set(gateway.GatewayTenantIDHeader, "spoofed-tenant")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func requireOutgoingMetadata(t *testing.T, ctx context.Context, header, want string) {
	t.Helper()
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatalf("outgoing metadata missing")
	}
	values := md.Get(header)
	if len(values) != 1 || values[0] != want {
		t.Fatalf("metadata %s = %v, want [%s]", header, values, want)
	}
}
