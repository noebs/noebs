package handler

import (
	"bytes"
	"context"
	"encoding/json"
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
	app.Post("/", func(c *fiber.Ctx) error {
		var req ebs_fields.ConsumerPurchaseFields
		return handleConfiguredEBS(h, c, &req, func(r *ebs_fields.ConsumerPurchaseFields) {
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

func TestCompleteRegistrationAppliesMountedConfigBeforeValidation(t *testing.T) {
	app := fiber.New()
	h := &Handler{Service: &consumer.Service{NoebsConfig: ebs_fields.NoebsConfig{
		ConsumerID: "mounted-consumer-app",
	}}}
	app.Post("/", h.CompleteRegistration)

	body := []byte(`{"tranDateTime":"20260528120000","UUID":"uuid-1","otp":"123456","IPIN":"123456","originalTranUUID":"original-uuid","userPassword":"ebs-password","mobile":"0912141660"}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant_1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var payload map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if payload["code"] != consumer.ErrMissingPassword.Error() {
		t.Fatalf("code = %q, want %q", payload["code"], consumer.ErrMissingPassword.Error())
	}
}
