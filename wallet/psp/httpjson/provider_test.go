package httpjson

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/adonese/noebs/wallet/psp"
)

func TestNewProviderRequiresExplicitRequestRoutes(t *testing.T) {
	cfg := &psp.Config{
		ProviderCode:         "pay",
		APIBaseURL:           "https://pay.example",
		DepositRequestMethod: http.MethodPost,
		DepositRequestPath:   "/deposit/verify",
		PayoutRequestMethod:  http.MethodPost,
		PayoutRequestPath:    "/payouts",
		StatusRequestMethod:  http.MethodGet,
		StatusRequestPath:    "/transactions/{transaction_id}",
	}

	if _, err := NewProvider(cfg); err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	cfg.StatusRequestPath = ""
	_, err := NewProvider(cfg)
	if !errors.Is(err, psp.ErrPSPConfigInvalid) {
		t.Fatalf("NewProvider() error = %v, want %v", err, psp.ErrPSPConfigInvalid)
	}
}

func TestNewProviderDoesNotUseEnvironmentProxy(t *testing.T) {
	provider, err := NewProvider(&psp.Config{
		ProviderCode:         "pay",
		APIBaseURL:           "https://pay.example",
		DepositRequestMethod: http.MethodPost,
		DepositRequestPath:   "/deposit/verify",
		PayoutRequestMethod:  http.MethodPost,
		PayoutRequestPath:    "/payouts",
		StatusRequestMethod:  http.MethodGet,
		StatusRequestPath:    "/transactions/{transaction_id}",
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	transport, ok := provider.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", provider.client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatalf("transport.Proxy must be nil; PSP providers must not read proxy environment variables")
	}
}

func TestAppendQueryForMethodAddsMappedGETFields(t *testing.T) {
	path := appendQueryForMethod(http.MethodGet, "/status", map[string]any{
		"reference": "ref-1",
		"nested": map[string]any{
			"code": "abc",
		},
	})
	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatalf("parse path: %v", err)
	}
	if parsed.Path != "/status" {
		t.Fatalf("path = %q, want /status", parsed.Path)
	}
	if got := parsed.Query().Get("reference"); got != "ref-1" {
		t.Fatalf("reference query = %q, want ref-1", got)
	}
	if got := parsed.Query().Get("nested.code"); got != "abc" {
		t.Fatalf("nested.code query = %q, want abc", got)
	}
}

func TestAppendQueryForMethodLeavesPOSTBodyFieldsOutOfURL(t *testing.T) {
	path := appendQueryForMethod(http.MethodPost, "/status", map[string]any{"reference": "ref-1"})
	if path != "/status" {
		t.Fatalf("path = %q, want /status", path)
	}
}
