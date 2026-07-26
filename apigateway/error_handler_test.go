package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adonese/noebs/apperr"
	"github.com/gofiber/fiber/v2"
)

func TestJSONErrorHandlerKeepsInternalCausesPrivate(t *testing.T) {
	secret := "postgres://operator:secret@db.internal"
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "application error",
			err:         apperr.Wrap(errors.New(secret), apperr.ErrDatabase, secret),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "database_error",
			wantMessage: "internal server error",
		},
		{
			name:        "bad gateway",
			err:         fiber.NewError(http.StatusBadGateway, secret),
			wantStatus:  http.StatusBadGateway,
			wantCode:    "upstream_unavailable",
			wantMessage: "upstream service unavailable",
		},
		{
			name:        "unknown error",
			err:         errors.New(secret),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "internal_error",
			wantMessage: "internal server error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := fiber.New(fiber.Config{
				DisableStartupMessage: true,
				ErrorHandler:          JSONErrorHandler,
			})
			app.Use(RequestID())
			app.Use(RedactReturnedErrors)
			app.Get("/result", func(*fiber.Ctx) error { return test.err })

			request := httptest.NewRequest(http.MethodGet, "/result", nil)
			request.Header.Set(RequestIDHeader, "request-123")
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("request error = %v", err)
			}
			t.Cleanup(func() { _ = response.Body.Close() })
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}

			var payload map[string]any
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload["code"] != test.wantCode || payload["message"] != test.wantMessage {
				t.Fatalf("payload = %#v, want code %q and message %q", payload, test.wantCode, test.wantMessage)
			}
			if payload["request_id"] != "request-123" {
				t.Fatalf("request_id = %v, want request-123", payload["request_id"])
			}
			if strings.Contains(payload["message"].(string), secret) {
				t.Fatalf("response leaked internal cause: %#v", payload)
			}
		})
	}
}

func TestJSONErrorHandlerPreservesClientErrorMessage(t *testing.T) {
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          JSONErrorHandler,
	})
	app.Get("/", func(*fiber.Ctx) error {
		return fiber.NewError(http.StatusBadRequest, "currency is required")
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })

	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["code"] != "request_error" || payload["message"] != "currency is required" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestReturnedHTTPErrorPreservesCauseWithStableMessage(t *testing.T) {
	cause := errors.New("private transport detail")
	err := &returnedHTTPError{
		err: apperr.Wrap(cause, apperr.ErrBadGateway, ""),
	}
	if !errors.Is(err, cause) {
		t.Fatal("returned error no longer preserves its cause")
	}
	if got := err.Error(); got != "upstream_unavailable" {
		t.Fatalf("Error() = %q, want upstream_unavailable", got)
	}
}
