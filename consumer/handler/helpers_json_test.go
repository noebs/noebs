package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestBindJSONRejectsUnknownAuthorityFields(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c *fiber.Ctx) error {
		var request struct {
			Name string `json:"name"`
		}
		if err := bindJSON(c, &request); err != nil {
			return c.SendStatus(http.StatusBadRequest)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"valid","tenant_id":"other"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}

func TestParseJSONRejectsTrailingValues(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c *fiber.Ctx) error {
		var request struct {
			Name string `json:"name"`
		}
		if err := parseJSON(c, &request); err != nil {
			return c.SendStatus(http.StatusBadRequest)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"first"}{"name":"second"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}
