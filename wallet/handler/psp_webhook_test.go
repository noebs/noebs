package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/gofiber/fiber/v2"
)

func TestWebhookIPAllowed(t *testing.T) {
	allowed, err := webhookIPAllowed("192.0.2.10", []string{"192.0.2.10"})
	if err != nil {
		t.Fatalf("exact ip allow: %v", err)
	}
	if !allowed {
		t.Fatal("expected exact ip to be allowed")
	}

	allowed, err = webhookIPAllowed("192.0.2.25", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatalf("cidr allow: %v", err)
	}
	if !allowed {
		t.Fatal("expected cidr ip to be allowed")
	}

	allowed, err = webhookIPAllowed("198.51.100.10", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatalf("cidr deny: %v", err)
	}
	if allowed {
		t.Fatal("expected out-of-range ip to be denied")
	}

	if _, err := webhookIPAllowed("192.0.2.10", []string{"invalid-cidr"}); err == nil {
		t.Fatal("expected invalid allow-list entry to fail")
	}
}

func TestPSPWebhookRequiresTenantQueryNotPayload(t *testing.T) {
	app := fiber.New()
	handler := &PSPWebhookHandler{Store: &walletstore.Store{}}
	app.Post("/psp/webhooks/:provider", handler.Handle)

	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/noop", bytes.NewBufferString(`{"tenant_id":"tenant-from-payload"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestMappedPSPWebhookFieldsRequireConfiguredPaths(t *testing.T) {
	payload := map[string]any{
		"client_reference":   "payload-ref",
		"psp_transaction_id": "payload-tx",
		"status":             "success",
		"amount":             float64(1250),
		"currency":           "AED",
		"direction":          "inbound",
		"message":            "accepted",
	}

	fields := mappedPSPWebhookFields(payload, walletpsp.ResponseMapping{})
	if fields.ClientReference != "" ||
		fields.PSPTransactionID != "" ||
		fields.Status != "" ||
		fields.Amount != 0 ||
		fields.Currency != "" ||
		fields.Direction != "" ||
		fields.Message != "" {
		t.Fatalf("fields = %+v, want empty without configured paths", fields)
	}

	fields = mappedPSPWebhookFields(payload, walletpsp.ResponseMapping{
		ClientReference: []string{"client_reference"},
		TransactionID:   []string{"psp_transaction_id"},
		Status:          []string{"status"},
		Amount:          []string{"amount"},
		Currency:        []string{"currency"},
		Direction:       []string{"direction"},
		Message:         []string{"message"},
	})
	if fields.ClientReference != "payload-ref" ||
		fields.PSPTransactionID != "payload-tx" ||
		fields.Status != "success" ||
		fields.Amount != 1250 ||
		fields.Currency != "AED" ||
		fields.Direction != "deposit" ||
		fields.Message != "accepted" {
		t.Fatalf("fields = %+v, want configured mapping values", fields)
	}
}
