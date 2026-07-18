package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/jmoiron/sqlx"
)

func TestGetCardsReturnsServerErrorForDatabaseFailure(t *testing.T) {
	db, err := sqlx.Open(store.DriverPostgres, "postgres://unused")
	if err != nil {
		t.Fatalf("open closed test database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close test database: %v", err)
	}

	service := &consumer.Service{Store: store.New(&store.DB{DB: db, Driver: store.DriverPostgres})}
	app := fiber.New()
	app.Use(authenticatedUserTestIdentity)
	app.Get("/consumer/get_cards", (&Handler{Service: service}).GetCards)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/consumer/get_cards", nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["code"] != "database_error" {
		t.Fatalf("code = %v, want database_error", payload["code"])
	}
	if _, ok := payload["cards"]; ok {
		t.Fatalf("database failure masqueraded as an empty card list: %#v", payload)
	}
}

func TestKYCRequiresAuthenticatedMobile(t *testing.T) {
	app := fiber.New()
	app.Post("/consumer/kyc", (&Handler{}).KYC)

	req := httptest.NewRequest(http.MethodPost, "/consumer/kyc", strings.NewReader(`{"mobile":"0990000001"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["code"] != "unauthorized" {
		t.Fatalf("code = %v, want unauthorized", payload["code"])
	}
}

func TestTransactionByUUIDRequiresAuthenticatedMobile(t *testing.T) {
	app := fiber.New()
	app.Get("/consumer/transaction", (&Handler{}).TransactionByUUID)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/consumer/transaction?uuid=transaction-1", nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	assertJSONStatusCode(t, resp, http.StatusUnauthorized, "unauthorized")
}

func TestKYCRejectsOversizedBody(t *testing.T) {
	app := fiber.New(fiber.Config{BodyLimit: maxKYCRequestBodyBytes + 1024})
	app.Use(authenticatedUserTestIdentity)
	app.Post("/consumer/kyc", (&Handler{}).KYC)

	body := []byte(`{"selfie":"` + strings.Repeat("A", maxKYCRequestBodyBytes) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/consumer/kyc", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	assertJSONStatusCode(t, resp, http.StatusRequestEntityTooLarge, "payload_too_large")
}

func TestKYCRejectsOversizedImages(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{name: "selfie", field: "selfie"},
		{name: "passport", field: "passport_image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := fiber.New()
			app.Use(authenticatedUserTestIdentity)
			app.Post("/consumer/kyc", (&Handler{}).KYC)

			body, err := json.Marshal(map[string]string{tt.field: strings.Repeat("A", maxKYCImageBytes+1)})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/consumer/kyc", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			assertJSONStatusCode(t, resp, http.StatusRequestEntityTooLarge, "image_too_large")
		})
	}
}

func authenticatedUserTestIdentity(c *fiber.Ctx) error {
	c.Locals("tenant_id", "tenant_1")
	c.Locals("user_id", int64(1))
	c.Locals("mobile", "0990000000")
	return c.Next()
}

func assertJSONStatusCode(t *testing.T, resp *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", resp.StatusCode, wantStatus)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["code"] != wantCode {
		t.Fatalf("code = %v, want %s", payload["code"], wantCode)
	}
}
