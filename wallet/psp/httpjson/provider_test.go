package httpjson

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestDoJSONReturnsInvalidResponseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{"))
	}))
	defer server.Close()

	provider, err := NewProvider(&psp.Config{
		ProviderCode:         "pay",
		APIBaseURL:           server.URL,
		DepositRequestMethod: http.MethodPost,
		DepositRequestPath:   "/deposit/verify",
		PayoutRequestMethod:  http.MethodPost,
		PayoutRequestPath:    "/payouts",
		StatusRequestMethod:  http.MethodGet,
		StatusRequestPath:    "/status",
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	out := map[string]any{}
	err = provider.doJSON(context.Background(), http.MethodGet, "/status", nil, "", &out)
	if !errors.Is(err, psp.ErrPSPResponseInvalid) {
		t.Fatalf("doJSON() error = %v, want %v", err, psp.ErrPSPResponseInvalid)
	}
}

func TestVerifyDepositRejectsInvalidMappedAmount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"provider_id":"psp-123","state":"success","minor_units":"12.34","currency":"AED"}}`))
	}))
	defer server.Close()

	provider, err := NewProvider(&psp.Config{
		ProviderCode:         "pay",
		APIBaseURL:           server.URL,
		DepositRequestMethod: http.MethodPost,
		DepositRequestPath:   "/deposit/verify",
		DepositResponseMapping: psp.ResponseMapping{
			TransactionID: []string{"result.provider_id"},
			Status:        []string{"result.state"},
			Amount:        []string{"result.minor_units"},
			Currency:      []string{"result.currency"},
		},
		PayoutRequestMethod: http.MethodPost,
		PayoutRequestPath:   "/payouts",
		StatusRequestMethod: http.MethodGet,
		StatusRequestPath:   "/status",
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	_, err = provider.VerifyDeposit(context.Background(), "psp-123")
	if !errors.Is(err, psp.ErrPSPResponseInvalid) {
		t.Fatalf("VerifyDeposit() error = %v, want %v", err, psp.ErrPSPResponseInvalid)
	}
}

func TestDoJSONReturnsReadError(t *testing.T) {
	provider := &Provider{
		config: &psp.Config{
			ProviderCode: "pay",
			APIBaseURL:   "https://pay.example",
		},
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       failingReadCloser{},
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})},
	}

	out := map[string]any{}
	err := provider.doJSON(context.Background(), http.MethodGet, "/status", nil, "", &out)
	if !errors.Is(err, psp.ErrPSPTemporary) {
		t.Fatalf("doJSON() error = %v, want %v", err, psp.ErrPSPTemporary)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (failingReadCloser) Close() error {
	return nil
}
