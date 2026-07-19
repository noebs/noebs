package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func TestHandleConfiguredEBSAppliesMountedConfigBeforeValidation(t *testing.T) {
	var got ebs_fields.ConsumerPurchaseFields

	app := fiber.New()
	h := &Handler{Service: &consumer.Service{NoebsConfig: ebs_fields.NoebsConfig{
		ConsumerID: "mounted-consumer-app",
		EBSDynamicFees: ebs_fields.DynamicFeesFields{
			SpecialPaymentFees: 17,
		},
	}}}
	app.Use(gateway.InternalTenantIdentityMiddleware())
	app.Post("/", func(c *fiber.Ctx) error {
		var req ebs_fields.ConsumerPurchaseFields
		return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerPurchaseFields) {
			r.ApplicationId = h.Service.NoebsConfig.ConsumerID
			r.DynamicFees = h.Service.NoebsConfig.EBSDynamicFees.SpecialPaymentFees
		}, func(ctx context.Context, tenantID string, req ebs_fields.ConsumerPurchaseFields) (ebs_fields.EBSParserFields, error) {
			if tenantID != "tenant_1" {
				t.Fatalf("tenantID = %q, want tenant_1", tenantID)
			}
			got = req
			return ebs_fields.EBSParserFields{}, nil
		}, nil)
	})

	body := []byte(`{"tranDateTime":"20260528120000","UUID":"uuid-1","PAN":"9222081700000000","IPIN":"123456","expDate":"2601","tranAmount":25,"serviceProviderId":"provider-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gateway.GatewayTenantIDHeader, " tenant_1 ")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got.ApplicationId != "mounted-consumer-app" {
		t.Fatalf("ApplicationId = %q, want mounted-consumer-app", got.ApplicationId)
	}
	if got.DynamicFees != 17 {
		t.Fatalf("DynamicFees = %v, want 17", got.DynamicFees)
	}
}

func TestAuthenticatedEBSRequiresGatewayUserIdentity(t *testing.T) {
	var called bool
	app := fiber.New()
	app.Post("/", authenticatedEBS(func(c *fiber.Ctx) error {
		called = true
		return c.SendStatus(http.StatusNoContent)
	}))

	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		t.Fatalf("request without user identity: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || called {
		t.Fatalf("missing identity status/called = %d/%v, want %d/false", resp.StatusCode, called, http.StatusUnauthorized)
	}

	app = fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", int64(42))
		return c.Next()
	})
	app.Post("/", authenticatedEBS(func(c *fiber.Ctx) error {
		called = true
		return c.SendStatus(http.StatusNoContent)
	}))
	resp, err = app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		t.Fatalf("request with user identity: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || !called {
		t.Fatalf("authenticated status/called = %d/%v, want %d/true", resp.StatusCode, called, http.StatusNoContent)
	}
}
