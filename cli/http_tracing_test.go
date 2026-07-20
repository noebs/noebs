package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/contrib/otelfiber"
	"github.com/gofiber/fiber/v2"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

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
