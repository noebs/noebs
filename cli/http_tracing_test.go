package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/apperr"
	"github.com/gofiber/contrib/otelfiber"
	"github.com/gofiber/fiber/v2"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestHTTPTracingRedactsReturnedErrorCause(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })

	const secret = "dial tcp database.internal:5432 with password secret"
	cause := errors.New(secret)
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler:          gateway.JSONErrorHandler,
	})
	app.Use(gateway.RequestID())
	app.Use(httpTracingMiddleware("test", otelfiber.WithTracerProvider(provider)))
	app.Use(gateway.RedactReturnedErrors)
	app.Get("/failure", func(*fiber.Ctx) error {
		return apperr.Wrap(cause, apperr.ErrDatabase, cause.Error())
	})

	response, err := app.Test(newTracingTestRequest(t, "/failure"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["message"] != "internal server error" {
		t.Fatalf("payload = %#v", payload)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	foundErrorCode := false
	for _, event := range spans[0].Events() {
		for _, attr := range event.Attributes {
			value := attr.Value.Emit()
			if strings.Contains(value, secret) {
				t.Fatalf("trace event exposed error cause in %s=%q", attr.Key, value)
			}
			if string(attr.Key) == "exception.message" && value == "database_error" {
				foundErrorCode = true
			}
		}
	}
	if !foundErrorCode {
		t.Fatalf("trace did not retain stable application error code: %#v", spans[0].Events())
	}
}

func TestHTTPTracingSkipsCredentialBearingAuthLifecycleURLs(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })

	app := fiber.New()
	app.Use(httpTracingMiddleware("test", otelfiber.WithTracerProvider(provider)))
	for _, path := range []string{
		backofficeCallbackPath,
		walletAuthorizationBrowserStartPath,
		walletAuthorizationCallbackPath,
		"/health",
	} {
		app.Get(path, func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
	}

	for _, target := range []string{
		walletAuthorizationBrowserStartPath + "?request=browser-start-secret",
		walletAuthorizationCallbackPath + "?state=wallet-state-secret&code=wallet-code-secret",
		backofficeCallbackPath + "?state=backoffice-state-secret&code=backoffice-code-secret",
	} {
		response, err := app.Test(newTracingTestRequest(t, target))
		if err != nil {
			t.Fatalf("GET %s: %v", target, err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatalf("close GET %s response: %v", target, err)
		}
		if spans := recorder.Ended(); len(spans) != 0 {
			t.Fatalf("GET %s produced %d trace spans", target, len(spans))
		}
	}

	response, err := app.Test(newTracingTestRequest(t, "/health"))
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close health response: %v", err)
	}
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ordinary request produced %d spans, want 1", len(spans))
	}
	for _, attribute := range spans[0].Attributes() {
		value := attribute.Value.Emit()
		if strings.Contains(value, "browser-start-secret") ||
			strings.Contains(value, "state-secret") ||
			strings.Contains(value, "code-secret") ||
			strings.Contains(value, "intent-secret") {
			t.Fatalf("sensitive authorization value entered trace attribute %s=%q", attribute.Key, value)
		}
	}
}

func newTracingTestRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "https://api.noebs.sd"+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Noebs-Transaction-Authorization", "intent-secret")
	return request
}
