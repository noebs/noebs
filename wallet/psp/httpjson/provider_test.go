package httpjson

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/adonese/noebs/wallet/psp"
)

func TestCreateDepositSendsServerReferenceAsPayloadAndIdempotencyKey(t *testing.T) {
	type observedRequest struct {
		body           map[string]any
		idempotencyKey string
	}
	observed := make(chan observedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		observed <- observedRequest{body: body, idempotencyKey: r.Header.Get("X-Deposit-Key")}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"reference":"dep_server_reference","id":"psp-123","state":"pending","amount":2500,"currency":"AED"}}`))
	}))
	defer server.Close()

	provider, err := NewProvider(&psp.Config{
		ProviderCode:         "pay",
		APIBaseURL:           server.URL,
		SupportsDeposit:      true,
		DepositRequestMethod: http.MethodPost,
		DepositRequestPath:   "/deposits",
		DepositRequestMapping: psp.RequestMapping{Fields: map[string]string{
			"reference": "client_reference",
			"amount":    "amount",
			"currency":  "currency",
			"metadata":  "metadata",
		}},
		DepositResponseMapping: psp.ResponseMapping{
			ClientReference: []string{"result.reference"},
			TransactionID:   []string{"result.id"},
			Status:          []string{"result.state"},
			Amount:          []string{"result.amount"},
			Currency:        []string{"result.currency"},
		},
		PayoutRequestMethod:   http.MethodPost,
		PayoutRequestPath:     "/payouts",
		StatusRequestMethod:   http.MethodGet,
		StatusRequestPath:     "/status",
		IdempotencyHeaderName: "X-Deposit-Key",
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	result, err := provider.CreateDeposit(context.Background(), psp.DepositRequest{
		IdempotencyKey:  "deposit-idem",
		ClientReference: "dep_server_reference",
		Amount:          2500,
		Currency:        "AED",
		Metadata:        map[string]any{"order": "42"},
	})
	if err != nil {
		t.Fatalf("CreateDeposit() error = %v", err)
	}
	request := <-observed
	if request.idempotencyKey != "deposit-idem" || request.body["reference"] != "dep_server_reference" {
		t.Fatalf("request = %+v", request)
	}
	if result.ClientReference != "dep_server_reference" || result.ProviderTxID != "psp-123" ||
		result.Amount != 2500 || result.Currency != "AED" || result.Status != "pending" {
		t.Fatalf("result = %+v", result)
	}
}

func TestNewProviderRejectsIncompleteDepositMappings(t *testing.T) {
	base := psp.Config{
		ProviderCode:          "pay",
		APIBaseURL:            "https://pay.example",
		IdempotencyHeaderName: "Idempotency-Key",
		SupportsDeposit:       true,
		DepositRequestMethod:  http.MethodPost,
		DepositRequestPath:    "/deposits",
		DepositResponseMapping: psp.ResponseMapping{
			ClientReference: []string{"reference"},
			TransactionID:   []string{"id"},
			Status:          []string{"status"},
			Amount:          []string{"amount"},
			Currency:        []string{"currency"},
		},
		PayoutRequestMethod: http.MethodPost,
		PayoutRequestPath:   "/payouts",
		StatusRequestMethod: http.MethodGet,
		StatusRequestPath:   "/status",
	}

	missingResponseReference := base
	missingResponseReference.DepositResponseMapping.ClientReference = nil
	if _, err := NewProvider(&missingResponseReference); !errors.Is(err, psp.ErrPSPConfigInvalid) {
		t.Fatalf("missing response reference error = %v, want %v", err, psp.ErrPSPConfigInvalid)
	}

	missingRequestReference := base
	missingRequestReference.DepositRequestMapping = psp.RequestMapping{Fields: map[string]string{
		"amount": "amount", "currency": "currency",
	}}
	if _, err := NewProvider(&missingRequestReference); !errors.Is(err, psp.ErrPSPConfigInvalid) {
		t.Fatalf("missing request reference error = %v, want %v", err, psp.ErrPSPConfigInvalid)
	}
}

func TestNewProviderRejectsIncompletePayoutSettlementMappings(t *testing.T) {
	mapping := psp.ResponseMapping{
		TransactionID: []string{"id"}, Status: []string{"status"}, Amount: []string{"amount"}, Currency: []string{"currency"},
	}
	base := psp.Config{
		ProviderCode: "pay", APIBaseURL: "https://pay.example", IdempotencyHeaderName: "Idempotency-Key",
		SupportsWithdrawal:   true,
		DepositRequestMethod: http.MethodPost, DepositRequestPath: "/deposits",
		PayoutRequestMethod: http.MethodPost, PayoutRequestPath: "/payouts", PayoutResponseMapping: mapping,
		StatusRequestMethod: http.MethodGet, StatusRequestPath: "/status", StatusResponseMapping: mapping,
	}
	for _, mutate := range []func(*psp.Config){
		func(config *psp.Config) { config.PayoutResponseMapping.Amount = nil },
		func(config *psp.Config) { config.PayoutResponseMapping.Currency = nil },
		func(config *psp.Config) { config.StatusResponseMapping.Amount = nil },
		func(config *psp.Config) { config.StatusResponseMapping.Currency = nil },
	} {
		config := base
		config.PayoutResponseMapping = mapping
		config.StatusResponseMapping = mapping
		mutate(&config)
		if _, err := NewProvider(&config); !errors.Is(err, psp.ErrPSPConfigInvalid) {
			t.Fatalf("incomplete payout mapping error = %v, want %v", err, psp.ErrPSPConfigInvalid)
		}
	}
}

func TestNewProviderRequiresExplicitRequestRoutes(t *testing.T) {
	cfg := &psp.Config{
		ProviderCode:          "pay",
		APIBaseURL:            "https://pay.example",
		IdempotencyHeaderName: "Idempotency-Key",
		DepositRequestMethod:  http.MethodPost,
		DepositRequestPath:    "/deposits",
		PayoutRequestMethod:   http.MethodPost,
		PayoutRequestPath:     "/payouts",
		StatusRequestMethod:   http.MethodGet,
		StatusRequestPath:     "/transactions/{transaction_id}",
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
		ProviderCode:          "pay",
		APIBaseURL:            "https://pay.example",
		IdempotencyHeaderName: "Idempotency-Key",
		DepositRequestMethod:  http.MethodPost,
		DepositRequestPath:    "/deposits",
		PayoutRequestMethod:   http.MethodPost,
		PayoutRequestPath:     "/payouts",
		StatusRequestMethod:   http.MethodGet,
		StatusRequestPath:     "/transactions/{transaction_id}",
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
	path, err := appendQueryForMethod(http.MethodGet, "/status?existing=1", map[string]any{
		"reference": "ref-1",
		"nested": map[string]any{
			"code": "abc",
		},
	})
	if err != nil {
		t.Fatalf("appendQueryForMethod() error = %v", err)
	}
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
	if got := parsed.Query().Get("existing"); got != "1" {
		t.Fatalf("existing query = %q, want 1", got)
	}
}

func TestAppendQueryForMethodLeavesPOSTBodyFieldsOutOfURL(t *testing.T) {
	path, err := appendQueryForMethod(http.MethodPost, "/status", map[string]any{"reference": "ref-1"})
	if err != nil {
		t.Fatalf("appendQueryForMethod() error = %v", err)
	}
	if path != "/status" {
		t.Fatalf("path = %q, want /status", path)
	}
}

func TestAppendQueryForMethodRejectsMalformedExistingQuery(t *testing.T) {
	_, err := appendQueryForMethod(http.MethodGet, "/status?existing=%zz", map[string]any{"reference": "ref-1"})
	if !errors.Is(err, psp.ErrPSPConfigInvalid) {
		t.Fatalf("appendQueryForMethod() error = %v, want %v", err, psp.ErrPSPConfigInvalid)
	}
}

func TestGetTransactionStatusRejectsMalformedConfiguredQueryBeforeHTTP(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	}))
	defer server.Close()

	provider, err := NewProvider(&psp.Config{
		ProviderCode:          "pay",
		APIBaseURL:            server.URL,
		IdempotencyHeaderName: "Idempotency-Key",
		DepositRequestMethod:  http.MethodPost,
		DepositRequestPath:    "/deposits",
		PayoutRequestMethod:   http.MethodPost,
		PayoutRequestPath:     "/payouts",
		StatusRequestMethod:   http.MethodGet,
		StatusRequestPath:     "/status?existing=%zz",
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	_, err = provider.GetTransactionStatus(context.Background(), psp.TransactionLookup{
		ProviderTxID: "psp-123", IdempotencyKey: "status-idem", ClientReference: "client-ref",
	})
	if !errors.Is(err, psp.ErrPSPConfigInvalid) {
		t.Fatalf("GetTransactionStatus() error = %v, want %v", err, psp.ErrPSPConfigInvalid)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestGetTransactionStatusResolvesByPersistedCommandIdentity(t *testing.T) {
	requests := make(chan url.Values, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"provider-1","status":"pending","amount":100,"currency":"AED"}`))
	}))
	defer server.Close()

	mapping := psp.ResponseMapping{
		TransactionID: []string{"id"}, Status: []string{"status"}, Amount: []string{"amount"}, Currency: []string{"currency"},
	}
	provider, err := NewProvider(&psp.Config{
		ProviderCode: "pay", APIBaseURL: server.URL, IdempotencyHeaderName: "Idempotency-Key",
		SupportsWithdrawal:   true,
		DepositRequestMethod: http.MethodPost, DepositRequestPath: "/deposits",
		PayoutRequestMethod: http.MethodPost, PayoutRequestPath: "/payouts", PayoutResponseMapping: mapping,
		StatusRequestMethod: http.MethodGet, StatusRequestPath: "/status", StatusResponseMapping: mapping,
		StatusRequestMapping: psp.RequestMapping{Fields: map[string]string{
			"lookup.idempotency": "idempotency_key", "lookup.reference": "client_reference",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	lookup := psp.TransactionLookup{IdempotencyKey: "withdrawal-idem", ClientReference: "withdrawal-1"}
	status, err := provider.GetTransactionStatus(context.Background(), lookup)
	if err != nil {
		t.Fatal(err)
	}
	query := <-requests
	if query.Get("lookup.idempotency") != lookup.IdempotencyKey || query.Get("lookup.reference") != lookup.ClientReference {
		t.Fatalf("lookup query = %v", query)
	}
	if status.ProviderTxID != "provider-1" || status.Amount != 100 || status.Currency != "AED" || status.Status != "pending" {
		t.Fatalf("status = %+v", status)
	}
}

func TestNewProviderRejectsStatusLookupMappingWithoutImmutableCommandIdentity(t *testing.T) {
	mapping := psp.ResponseMapping{
		TransactionID: []string{"id"}, Status: []string{"status"}, Amount: []string{"amount"}, Currency: []string{"currency"},
	}
	_, err := NewProvider(&psp.Config{
		ProviderCode: "pay", APIBaseURL: "https://pay.example", IdempotencyHeaderName: "Idempotency-Key",
		SupportsWithdrawal:   true,
		DepositRequestMethod: http.MethodPost, DepositRequestPath: "/deposits",
		PayoutRequestMethod: http.MethodPost, PayoutRequestPath: "/payouts", PayoutResponseMapping: mapping,
		StatusRequestMethod: http.MethodGet, StatusRequestPath: "/status", StatusResponseMapping: mapping,
		StatusRequestMapping: psp.RequestMapping{Fields: map[string]string{
			"lookup.idempotency": "idempotency_key",
		}},
	})
	if !errors.Is(err, psp.ErrPSPConfigInvalid) {
		t.Fatalf("NewProvider() error = %v, want %v", err, psp.ErrPSPConfigInvalid)
	}
}

func TestDoJSONReturnsInvalidResponseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{"))
	}))
	defer server.Close()

	provider, err := NewProvider(&psp.Config{
		ProviderCode:          "pay",
		APIBaseURL:            server.URL,
		IdempotencyHeaderName: "Idempotency-Key",
		DepositRequestMethod:  http.MethodPost,
		DepositRequestPath:    "/deposits",
		PayoutRequestMethod:   http.MethodPost,
		PayoutRequestPath:     "/payouts",
		StatusRequestMethod:   http.MethodGet,
		StatusRequestPath:     "/status",
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

func TestDoJSONPreservesExactMonetaryIntegers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"amounts":{"above_javascript_limit":9007199254740993,"maximum":9223372036854775807,"minimum":-9223372036854775808}}`))
	}))
	defer server.Close()

	provider := &Provider{
		config: &psp.Config{APIBaseURL: server.URL},
		client: server.Client(),
	}
	out := map[string]any{}
	if err := provider.doJSON(context.Background(), http.MethodGet, "/", nil, "", &out); err != nil {
		t.Fatalf("doJSON() error = %v", err)
	}

	tests := []struct {
		path string
		want int64
	}{
		{path: "amounts.above_javascript_limit", want: 9007199254740993},
		{path: "amounts.maximum", want: 9223372036854775807},
		{path: "amounts.minimum", want: -9223372036854775808},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			value, ok := valueAtPathForTest(out, tt.path)
			if !ok {
				t.Fatalf("missing decoded value at %q", tt.path)
			}
			if _, ok := value.(json.Number); !ok {
				t.Fatalf("decoded value type = %T, want json.Number", value)
			}
			mapped, err := psp.MapResponse(out, psp.ResponseMapping{Amount: []string{tt.path}})
			if err != nil {
				t.Fatalf("MapResponse() error = %v", err)
			}
			if mapped.Amount != tt.want {
				t.Fatalf("MapResponse().Amount = %d, want %d", mapped.Amount, tt.want)
			}
		})
	}
}

func TestDoJSONRejectsMultipleJSONValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"amount":1} {"amount":2}`))
	}))
	defer server.Close()

	provider := &Provider{
		config: &psp.Config{APIBaseURL: server.URL},
		client: server.Client(),
	}
	out := map[string]any{}
	err := provider.doJSON(context.Background(), http.MethodGet, "/", nil, "", &out)
	if !errors.Is(err, psp.ErrPSPResponseInvalid) {
		t.Fatalf("doJSON() error = %v, want %v", err, psp.ErrPSPResponseInvalid)
	}
}

func valueAtPathForTest(payload map[string]any, path string) (any, bool) {
	var current any = payload
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func TestCreateDepositRejectsInvalidMappedAmount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"provider_id":"psp-123","state":"success","minor_units":"12.34","currency":"AED"}}`))
	}))
	defer server.Close()

	provider, err := NewProvider(&psp.Config{
		ProviderCode:          "pay",
		APIBaseURL:            server.URL,
		IdempotencyHeaderName: "Idempotency-Key",
		SupportsDeposit:       true,
		DepositRequestMethod:  http.MethodPost,
		DepositRequestPath:    "/deposits",
		DepositResponseMapping: psp.ResponseMapping{
			ClientReference: []string{"result.client_reference"},
			TransactionID:   []string{"result.provider_id"},
			Status:          []string{"result.state"},
			Amount:          []string{"result.minor_units"},
			Currency:        []string{"result.currency"},
		},
		PayoutRequestMethod: http.MethodPost,
		PayoutRequestPath:   "/payouts",
		StatusRequestMethod: http.MethodGet,
		StatusRequestPath:   "/status",
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	_, err = provider.CreateDeposit(context.Background(), psp.DepositRequest{
		IdempotencyKey:  "deposit-idem",
		ClientReference: "deposit-reference",
		Amount:          100,
		Currency:        "AED",
	})
	if !errors.Is(err, psp.ErrPSPResponseInvalid) {
		t.Fatalf("CreateDeposit() error = %v, want %v", err, psp.ErrPSPResponseInvalid)
	}
}

func TestSendPayoutRejectsMissingMappedSourceBeforeHTTP(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	}))
	defer server.Close()

	provider, err := NewProvider(&psp.Config{
		ProviderCode:          "pay",
		APIBaseURL:            server.URL,
		IdempotencyHeaderName: "Idempotency-Key",
		SupportsWithdrawal:    true,
		DepositRequestMethod:  http.MethodPost,
		DepositRequestPath:    "/deposits",
		PayoutRequestMethod:   http.MethodPost,
		PayoutRequestPath:     "/payouts",
		PayoutRequestMapping: psp.RequestMapping{
			Fields: map[string]string{
				"reference": "client_reference", "amount": "amount", "currency": "currency",
				"beneficiary.iban": "destination.iban",
			},
		},
		PayoutResponseMapping: psp.ResponseMapping{
			TransactionID: []string{"id"}, Status: []string{"status"}, Amount: []string{"amount"}, Currency: []string{"currency"},
		},
		StatusResponseMapping: psp.ResponseMapping{
			TransactionID: []string{"id"}, Status: []string{"status"}, Amount: []string{"amount"}, Currency: []string{"currency"},
		},
		StatusRequestMethod: http.MethodGet,
		StatusRequestPath:   "/status",
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	_, err = provider.SendPayout(context.Background(), psp.PayoutRequest{
		IdempotencyKey:  "payout-idem",
		ClientReference: "ref-1",
		Amount:          1500,
		Currency:        "AED",
		Destination:     map[string]any{},
	})
	if !errors.Is(err, psp.ErrPSPRequestInvalid) {
		t.Fatalf("SendPayout() error = %v, want %v", err, psp.ErrPSPRequestInvalid)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestSendPayoutRetriesUseTheSameRequiredIdempotencyHeader(t *testing.T) {
	keys := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys <- r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"payout-1","status":"pending","amount":100,"currency":"AED"}`))
	}))
	defer server.Close()

	provider, err := NewProvider(&psp.Config{
		ProviderCode:          "pay",
		APIBaseURL:            server.URL,
		IdempotencyHeaderName: "Idempotency-Key",
		SupportsWithdrawal:    true,
		DepositRequestMethod:  http.MethodPost,
		DepositRequestPath:    "/deposits",
		PayoutRequestMethod:   http.MethodPost,
		PayoutRequestPath:     "/payouts",
		PayoutResponseMapping: psp.ResponseMapping{
			TransactionID: []string{"id"},
			Status:        []string{"status"},
			Amount:        []string{"amount"},
			Currency:      []string{"currency"},
		},
		StatusResponseMapping: psp.ResponseMapping{
			TransactionID: []string{"id"},
			Status:        []string{"status"},
			Amount:        []string{"amount"},
			Currency:      []string{"currency"},
		},
		StatusRequestMethod: http.MethodGet,
		StatusRequestPath:   "/status",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := psp.PayoutRequest{
		IdempotencyKey: "withdrawal-idem", ClientReference: "withdrawal-1",
		Amount: 100, Currency: "AED", Destination: map[string]any{},
	}
	for range 2 {
		if _, err := provider.SendPayout(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if key := <-keys; key != request.IdempotencyKey {
			t.Fatalf("idempotency header = %q, want %q", key, request.IdempotencyKey)
		}
	}
}

func TestDoJSONClassifiesAmbiguousClientStatusesAsTemporary(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{name: "success", status: http.StatusNoContent},
		{name: "bad request", status: http.StatusBadRequest, want: psp.ErrPSPPermanent},
		{name: "unauthorized", status: http.StatusUnauthorized, want: psp.ErrPSPPermanent},
		{name: "forbidden", status: http.StatusForbidden, want: psp.ErrPSPPermanent},
		{name: "not found", status: http.StatusNotFound, want: psp.ErrPSPPermanent},
		{name: "request timeout", status: http.StatusRequestTimeout, want: psp.ErrPSPTemporary},
		{name: "conflict", status: http.StatusConflict, want: psp.ErrPSPTemporary},
		{name: "unprocessable entity", status: http.StatusUnprocessableEntity, want: psp.ErrPSPPermanent},
		{name: "too early", status: http.StatusTooEarly, want: psp.ErrPSPTemporary},
		{name: "too many requests", status: http.StatusTooManyRequests, want: psp.ErrPSPTemporary},
		{name: "server error", status: http.StatusInternalServerError, want: psp.ErrPSPTemporary},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &Provider{
				config: &psp.Config{APIBaseURL: "https://pay.example"},
				client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: test.status,
						Body:       http.NoBody,
						Header:     make(http.Header),
						Request:    req,
					}, nil
				})},
			}

			err := provider.doJSON(context.Background(), http.MethodGet, "/status", nil, "", nil)
			if !errors.Is(err, test.want) {
				t.Fatalf("doJSON() error = %v, want %v", err, test.want)
			}
			if test.want == psp.ErrPSPTemporary && errors.Is(err, psp.ErrPSPPermanent) {
				t.Fatalf("doJSON() classified HTTP %d as permanent", test.status)
			}
		})
	}
}

func TestSendPayoutLostResponseThenConflictRemainsAmbiguousAcrossRetries(t *testing.T) {
	mapping := psp.ResponseMapping{
		TransactionID: []string{"id"},
		Status:        []string{"status"},
		Amount:        []string{"amount"},
		Currency:      []string{"currency"},
	}
	provider, err := NewProvider(&psp.Config{
		ProviderCode:          "pay",
		APIBaseURL:            "https://pay.example",
		IdempotencyHeaderName: "Idempotency-Key",
		SupportsWithdrawal:    true,
		DepositRequestMethod:  http.MethodPost,
		DepositRequestPath:    "/deposits",
		PayoutRequestMethod:   http.MethodPost,
		PayoutRequestPath:     "/payouts",
		PayoutResponseMapping: mapping,
		StatusRequestMethod:   http.MethodGet,
		StatusRequestPath:     "/status",
		StatusResponseMapping: mapping,
	})
	if err != nil {
		t.Fatal(err)
	}

	const idempotencyKey = "withdrawal-idem"
	attempts := 0
	provider.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if key := req.Header.Get("Idempotency-Key"); key != idempotencyKey {
			t.Fatalf("attempt %d idempotency key = %q, want %q", attempts, key, idempotencyKey)
		}
		if attempts == 1 {
			return nil, errors.New("connection lost after provider accepted payout")
		}
		return &http.Response{
			StatusCode: http.StatusConflict,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	request := psp.PayoutRequest{
		IdempotencyKey:  idempotencyKey,
		ClientReference: "withdrawal-1",
		Amount:          100,
		Currency:        "AED",
		Destination:     map[string]any{},
	}
	for attempt := 1; attempt <= 3; attempt++ {
		_, err := provider.SendPayout(context.Background(), request)
		if !errors.Is(err, psp.ErrPSPTemporary) || errors.Is(err, psp.ErrPSPPermanent) {
			t.Fatalf("attempt %d error = %v, want temporary ambiguity", attempt, err)
		}
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
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
