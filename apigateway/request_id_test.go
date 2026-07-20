package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestRequestIDAcceptsOneCanonicalToken(t *testing.T) {
	app := fiber.New()
	app.Use(RequestID())
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString(RequestIDFromCtx(c))
	})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, "request-01_A")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get(RequestIDHeader) != "request-01_A" {
		t.Fatalf("status/header = %d/%q", response.StatusCode, response.Header.Get(RequestIDHeader))
	}
}

func TestRequestIDGeneratesUUIDWhenAbsent(t *testing.T) {
	app := fiber.New()
	app.Use(RequestID())
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()
	requestID := response.Header.Get(RequestIDHeader)
	if parsed, err := uuid.Parse(requestID); err != nil || parsed.String() != requestID {
		t.Fatalf("generated request ID = %q, error = %v", requestID, err)
	}
}

func TestRequestIDRejectsValuesWorkloadSigningCannotRepresent(t *testing.T) {
	app := fiber.New()
	app.Use(RequestID())
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })

	for _, value := range []string{"contains space", strings.Repeat("a", maxRequestIDBytes+1)} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set(RequestIDHeader, value)
		response, err := app.Test(request)
		if err != nil {
			t.Fatalf("request %q: %v", value, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("request ID %q status = %d, want 400", value, response.StatusCode)
		}
	}
	if validRequestID("\tvalue") {
		t.Fatal("tab-prefixed request ID accepted")
	}
}
