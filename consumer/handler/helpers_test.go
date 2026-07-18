package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

func TestResolveTenantIDRequiresExplicitTenant(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		if _, err := resolveTenantID(c); !errors.Is(err, store.ErrMissingTenantID) {
			t.Fatalf("resolveTenantID() error = %v, want %v", err, store.ErrMissingTenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestResolveTenantIDUsesGatewayTenantMiddleware(t *testing.T) {
	app := fiber.New()
	app.Use(gateway.InternalTenantIdentityMiddleware())
	app.Get("/", func(c *fiber.Ctx) error {
		tenantID, err := resolveTenantID(c)
		if err != nil {
			t.Fatalf("resolveTenantID() error = %v", err)
		}
		if tenantID != "tenant_1" {
			t.Fatalf("tenantID = %q, want tenant_1", tenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, " tenant_1 ")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestResolveRequestSourceUsesGatewayIdentityLocal(t *testing.T) {
	app := fiber.New()
	app.Use(gateway.InternalTenantIdentityMiddleware())
	app.Get("/", func(c *fiber.Ctx) error {
		source, err := resolveRequestSource(c)
		if err != nil {
			t.Fatalf("resolveRequestSource() error = %v", err)
		}
		if source != "203.0.113.8" {
			t.Fatalf("source = %q, want 203.0.113.8", source)
		}
		return c.SendStatus(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant_1")
	req.Header.Set(gateway.GatewaySourceIPHeader, "203.0.113.8")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestResolveTenantIDDoesNotReadGatewayTenantHeaderDirectly(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		if _, err := resolveTenantID(c); !errors.Is(err, store.ErrMissingTenantID) {
			t.Fatalf("resolveTenantID() error = %v, want %v", err, store.ErrMissingTenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant_1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestResolveTenantIDIgnoresPublicTenantHeader(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		if _, err := resolveTenantID(c); !errors.Is(err, store.ErrMissingTenantID) {
			t.Fatalf("resolveTenantID() error = %v, want %v", err, store.ErrMissingTenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "tenant_1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestStatusForErrorMapsDuplicateTransactionsToConflict(t *testing.T) {
	if got := statusForError(store.ErrDuplicateTransaction); got != http.StatusConflict {
		t.Fatalf("statusForError(ErrDuplicateTransaction) = %d, want %d", got, http.StatusConflict)
	}
}

func TestStatusForErrorMapsInvalidAmountToBadRequest(t *testing.T) {
	if got := statusForError(store.ErrInvalidAmount); got != http.StatusBadRequest {
		t.Fatalf("statusForError(ErrInvalidAmount) = %d, want %d", got, http.StatusBadRequest)
	}
}

func TestStatusForErrorMapsMerchantValidationToBadRequest(t *testing.T) {
	for _, err := range []error{consumer.ErrMissingMerchantID, consumer.ErrInvalidMerchantID} {
		if got := statusForError(err); got != http.StatusBadRequest {
			t.Fatalf("statusForError(%v) = %d, want %d", err, got, http.StatusBadRequest)
		}
	}
}
