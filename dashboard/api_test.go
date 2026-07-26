package dashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/jmoiron/sqlx"
)

func TestService_calculateOffset(t *testing.T) {
	type fields struct {
		Store *store.Store
	}
	type args struct {
		page     int
		pageSize int
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   uint
	}{
		{"calculateOffset", fields{}, args{page: 0, pageSize: 10}, 0},
		{"calculateOffset", fields{}, args{page: 1, pageSize: 10}, 0},
		{"calculateOffset", fields{}, args{page: 2, pageSize: 10}, 10},
		{"calculateOffset", fields{}, args{page: 3, pageSize: 10}, 20},
		{"calculateOffset", fields{}, args{page: 4, pageSize: 10}, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Service{
				Store: tt.fields.Store,
			}
			if got := s.calculateOffset(tt.args.page, tt.args.pageSize); got != tt.want {
				t.Errorf("Service.calculateOffset() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParsePositiveQueryInt(t *testing.T) {
	got, err := parsePositiveQueryInt("", 50)
	if err != nil {
		t.Fatalf("parsePositiveQueryInt(default) error = %v", err)
	}
	if got != 50 {
		t.Fatalf("parsePositiveQueryInt(default) = %d, want 50", got)
	}

	got, err = parsePositiveQueryInt(" 2 ", 50)
	if err != nil {
		t.Fatalf("parsePositiveQueryInt(valid) error = %v", err)
	}
	if got != 2 {
		t.Fatalf("parsePositiveQueryInt(valid) = %d, want 2", got)
	}

	for _, raw := range []string{"0", "-1", "abc"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parsePositiveQueryInt(raw, 50); !errors.Is(err, ErrInvalidPagination) {
				t.Fatalf("parsePositiveQueryInt(%q) error = %v, want %v", raw, err, ErrInvalidPagination)
			}
		})
	}
}

func TestDashboardPaginationRejectsInvalidInputsBeforeDB(t *testing.T) {
	service := Service{}
	app := fiber.New()
	app.Get("/transactions", func(c *fiber.Ctx) error {
		service.GetAll(c)
		return nil
	})
	app.Get("/browser", func(c *fiber.Ctx) error {
		service.BrowserDashboard(c)
		return nil
	})

	tests := []string{
		"/transactions?page=0",
		"/transactions?size=-1",
		"/transactions?perPage=-1",
		"/browser?page=-1",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

func TestDashboardTransactionsDefaultOmittedPageAtBoundary(t *testing.T) {
	service := Service{}
	app := fiber.New()
	app.Get("/transactions", func(c *fiber.Ctx) error {
		service.GetAll(c)
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/transactions", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["code"] != "internal_error" || payload["message"] != "internal server error" {
		t.Fatalf("payload = %#v", payload)
	}
	if strings.Contains(payload["message"].(string), "nil db") {
		t.Fatalf("response leaked database detail: %#v", payload)
	}
}

func TestDashboardTransactionQueryRejectsInvalidFieldsBeforeDB(t *testing.T) {
	service := Service{}
	app := fiber.New()
	app.Get("/transactions", func(c *fiber.Ctx) error {
		service.GetAll(c)
		return nil
	})

	tests := []string{
		"/transactions?search=value&field=unknownField",
		"/transactions?sort_field=unknownField",
		"/transactions?order=SIDEWAYS",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

func TestBrowserDashboardIgnoresGETBodyAtBoundary(t *testing.T) {
	service := Service{}
	app := fiber.New()
	app.Get("/browser", gateway.InternalTenantIdentityMiddleware(), func(c *fiber.Ctx) error {
		service.BrowserDashboard(c)
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/browser?tid=terminal-1", strings.NewReader("{"))
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant-1")
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d after ignoring GET body", resp.StatusCode, http.StatusInternalServerError)
	}
}

func TestQRStatusReturnsInternalDatabaseFailures(t *testing.T) {
	service := Service{}
	app := fiber.New()
	app.Get("/qr", gateway.InternalTenantIdentityMiddleware(), func(c *fiber.Ctx) error {
		service.QRStatus(c)
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/qr?id=merchant-1", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant-1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["code"] != "internal_error" || payload["message"] != "internal server error" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestResolveTenantIDDoesNotDefaultFromServiceConfig(t *testing.T) {
	s := Service{
		NoebsConfig: ebs_fields.NoebsConfig{DefaultTenantID: "test-tenant"},
	}

	_, err := s.resolveTenantID(nil)
	if !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("resolveTenantID(nil) error = %v, want %v", err, store.ErrMissingTenantID)
	}
}

func TestResolveTenantIDUsesValidatedTenantIdentity(t *testing.T) {
	app := fiber.New()
	service := Service{}
	app.Get("/", gateway.InternalTenantIdentityMiddleware(), func(c *fiber.Ctx) error {
		tenantID, err := service.resolveTenantID(c)
		if err != nil {
			t.Fatalf("resolveTenantID() error = %v", err)
		}
		if tenantID != "tenant-1" {
			t.Fatalf("tenantID = %q, want tenant-1", tenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant-1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestResolveTenantIDDoesNotReadGatewayTenantHeaderDirectly(t *testing.T) {
	app := fiber.New()
	service := Service{}
	app.Get("/", func(c *fiber.Ctx) error {
		if _, err := service.resolveTenantID(c); !errors.Is(err, store.ErrMissingTenantID) {
			t.Fatalf("resolveTenantID() error = %v, want %v", err, store.ErrMissingTenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant-1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestResolveTenantIDIgnoresPublicTenantHeader(t *testing.T) {
	app := fiber.New()
	service := Service{}
	app.Get("/", func(c *fiber.Ctx) error {
		if _, err := service.resolveTenantID(c); !errors.Is(err, store.ErrMissingTenantID) {
			t.Fatalf("resolveTenantID() error = %v, want %v", err, store.ErrMissingTenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "tenant-1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestMerchantTransactionsEndpointReturnsQueryErrors(t *testing.T) {
	db := sqlx.MustOpen(store.DriverPostgres, "postgres://noebs:noebs@127.0.0.1:1/noebs?sslmode=disable")
	t.Cleanup(func() { _ = db.Close() })
	service := Service{Store: store.New(&store.DB{DB: db, Driver: store.DriverPostgres})}
	app := fiber.New()
	app.Get("/merchant-transactions", gateway.InternalTenantIdentityMiddleware(), func(c *fiber.Ctx) error {
		service.MerchantTransactionsEndpoint(c)
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/merchant-transactions?terminal=terminal-a", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant-1")
	resp, err := app.Test(req, 1000)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["code"] != "internal_error" || payload["message"] != "internal server error" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestMerchantTransactionSummaryIsCountOnly(t *testing.T) {
	payload, err := json.Marshal(MerchantTransactionCounts{
		AllTransactions:        3,
		SuccessfulTransactions: 2,
		FailedTransactions:     1,
	})
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	encoded := string(payload)
	for _, want := range []string{`"transactions_count":3`, `"successful_transactions":2`, `"failed_transactions":1`} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("summary = %s, want %s", encoded, want)
		}
	}
	for _, unsupported := range []string{"purchase_amount", "tran_amount", "response_status"} {
		if strings.Contains(encoded, unsupported) || strings.Contains(merchantTransactionCountsQuery, unsupported) {
			t.Fatalf("merchant summary retains unsupported contract %q", unsupported)
		}
	}
}

func TestDashboardHandlersDoNotIgnoreRuntimeErrors(t *testing.T) {
	source, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatalf("read api.go: %v", err)
	}
	for _, token := range []string{
		"_ = db.SelectContext",
		"_ = json.NewEncoder",
		"_ = c.SendStream",
		"error counting transactions",
		"error summing transactions",
	} {
		if strings.Contains(string(source), token) {
			t.Fatalf("dashboard api must return runtime errors, found ignored-error pattern %q", token)
		}
	}
}
