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
		if tenantID != "tenant-1" {
			t.Fatalf("tenantID = %q, want tenant-1", tenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant-1")
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
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant-1")
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
	req.Header.Set("X-Tenant-ID", "tenant-1")
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

func TestStatusForErrorMapsValidationToBadRequest(t *testing.T) {
	for _, err := range []error{
		consumer.ErrMissingMerchantID,
		consumer.ErrInvalidMerchantID,
		store.ErrInvalidPushData,
		store.ErrMissingPushTarget,
		store.ErrInvalidTransactionUUID,
	} {
		if got := statusForError(err); got != http.StatusBadRequest {
			t.Fatalf("statusForError(%v) = %d, want %d", err, got, http.StatusBadRequest)
		}
	}
}

func TestStatusForErrorMapsMissingInternalClientToUnavailable(t *testing.T) {
	if got := statusForError(consumer.ErrMissingInternalHTTPClient); got != http.StatusServiceUnavailable {
		t.Fatalf("statusForError(ErrMissingInternalHTTPClient) = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

func TestAuthenticatedUserIDRequiresExactGatewayType(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		wantID  int64
		wantErr bool
	}{
		{name: "exact int64", value: int64(42), wantID: 42},
		{name: "missing", wantErr: true},
		{name: "zero", value: int64(0), wantErr: true},
		{name: "int", value: int(42), wantErr: true},
		{name: "uint", value: uint(42), wantErr: true},
		{name: "float", value: float64(42.9), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", func(c *fiber.Ctx) error {
				if test.value != nil {
					c.Locals("user_id", test.value)
				}
				userID, err := authenticatedUserID(c)
				if (err != nil) != test.wantErr {
					t.Fatalf("authenticatedUserID() error = %v, wantErr %t", err, test.wantErr)
				}
				if userID != test.wantID {
					t.Fatalf("authenticatedUserID() = %d, want %d", userID, test.wantID)
				}
				return c.SendStatus(http.StatusNoContent)
			})

			response, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
		})
	}
}

func TestGetTransactionsRejectsMalformedGatewayUserIdentity(t *testing.T) {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("tenant_id", "tenant-1")
		c.Locals("user_id", float64(42))
		return c.Next()
	})
	app.Get("/transactions", (&Handler{}).GetTransactions)

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/transactions", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}
