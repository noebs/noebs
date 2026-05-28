package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/gofiber/fiber/v2"
)

func TestWebhookIPAllowed(t *testing.T) {
	allowed, err := webhookIPAllowed("192.0.2.10", []string{"192.0.2.10"})
	if err != nil {
		t.Fatalf("exact ip allow: %v", err)
	}
	if !allowed {
		t.Fatal("expected exact ip to be allowed")
	}

	allowed, err = webhookIPAllowed("192.0.2.25", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatalf("cidr allow: %v", err)
	}
	if !allowed {
		t.Fatal("expected cidr ip to be allowed")
	}

	allowed, err = webhookIPAllowed("198.51.100.10", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatalf("cidr deny: %v", err)
	}
	if allowed {
		t.Fatal("expected out-of-range ip to be denied")
	}

	if _, err := webhookIPAllowed("192.0.2.10", []string{"invalid-cidr"}); err == nil {
		t.Fatal("expected invalid allow-list entry to fail")
	}
}

func TestPSPWebhookRequiresGatewayTenantHeaderNotPayloadOrQuery(t *testing.T) {
	app := fiber.New()
	handler := &PSPWebhookHandler{Store: &walletstore.Store{}}
	app.Post("/psp/webhooks/:provider", handler.Handle)

	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/noop?tenant_id=tenant-from-query", bytes.NewBufferString(`{"tenant_id":"tenant-from-payload"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant-from-public-header")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestPSPWebhookHandlerDoesNotReadPublicTenantQuery(t *testing.T) {
	data, err := os.ReadFile("psp_webhook.go")
	if err != nil {
		t.Fatalf("read psp_webhook.go: %v", err)
	}
	if strings.Contains(string(data), `c.Query("tenant_id")`) {
		t.Fatalf("psp_webhook.go reads public tenant query; webhook tenant must come from %s", gateway.GatewayTenantIDHeader)
	}
}

func TestMappedPSPWebhookFieldsRequireConfiguredPaths(t *testing.T) {
	payload := map[string]any{
		"client_reference":   "payload-ref",
		"psp_transaction_id": "payload-tx",
		"status":             "success",
		"amount":             float64(1250),
		"currency":           "AED",
		"direction":          "inbound",
		"message":            "accepted",
	}

	fields := mappedPSPWebhookFields(payload, walletpsp.ResponseMapping{})
	if fields.ClientReference != "" ||
		fields.PSPTransactionID != "" ||
		fields.Status != "" ||
		fields.Amount != 0 ||
		fields.Currency != "" ||
		fields.Direction != "" ||
		fields.Message != "" {
		t.Fatalf("fields = %+v, want empty without configured paths", fields)
	}

	fields = mappedPSPWebhookFields(payload, walletpsp.ResponseMapping{
		ClientReference: []string{"client_reference"},
		TransactionID:   []string{"psp_transaction_id"},
		Status:          []string{"status"},
		Amount:          []string{"amount"},
		Currency:        []string{"currency"},
		Direction:       []string{"direction"},
		Message:         []string{"message"},
	})
	if fields.ClientReference != "payload-ref" ||
		fields.PSPTransactionID != "payload-tx" ||
		fields.Status != "success" ||
		fields.Amount != 1250 ||
		fields.Currency != "AED" ||
		fields.Direction != "deposit" ||
		fields.Message != "accepted" {
		t.Fatalf("fields = %+v, want configured mapping values", fields)
	}
}

func TestPSPWebhookRejectsUnknownClientReference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	defer func() {
		_ = container.Terminate(context.Background())
	}()

	dbName := fmt.Sprintf("noebs_psp_webhook_handler_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	db, err := basestore.OpenFromConfig(dbURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	}()

	const tenantID = "tenant"
	const providerCode = "handler-noop"
	if err := basestore.MigrateScope(ctx, db, tenantID, basestore.MigrationScopePSPWebhook); err != nil {
		t.Fatalf("migrate psp webhook db: %v", err)
	}
	if err := basestore.New(db).EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}

	mapping := `{"client_reference":["ref"],"transaction_id":["psp_tx"],"status":["status"],"amount":["amount"],"currency":["currency"],"direction":["direction"]}`
	stmt := db.Rebind(`INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, enabled_currencies,
		is_active, supports_deposit, supports_withdrawal, webhook_response_mapping
	) VALUES(?, ?, ?, ?, ARRAY['SDG'], TRUE, TRUE, TRUE, ?::jsonb)`)
	if _, err := db.ExecContext(ctx, stmt, tenantID, providerCode, "Handler Noop", "https://psp.example", mapping); err != nil {
		t.Fatalf("insert psp config: %v", err)
	}

	store := walletstore.New(db)
	loader := &walletpsp.Loader{
		Store: store,
		Secrets: walletpsp.SecretResolverFunc(func(context.Context, string, string) (walletpsp.SecretBundle, error) {
			return walletpsp.SecretBundle{WebhookSecret: "handler-test-secret"}, nil
		}),
	}
	registry := walletpsp.NewRegistry()
	provider := &acceptingWebhookProvider{code: providerCode}
	if err := registry.Register(providerCode, func(*walletpsp.Config) (walletpsp.Provider, error) {
		return provider, nil
	}); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	app := fiber.New(fiber.Config{ProxyHeader: fiber.HeaderXForwardedFor})
	app.Post("/psp/webhooks/:provider", NewPSPWebhookHandler(store, loader, registry, nil).Handle)

	payload := `{"ref":"missing-ref","psp_tx":"psp-missing","status":"success","amount":1250,"currency":"SDG","direction":"inbound"}`
	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/"+providerCode+"?tenant_id=default", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gateway.GatewayTenantIDHeader, tenantID)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", resp.StatusCode, http.StatusNotFound, string(body))
	}
	if _, err := store.GetPSPTransactionByReference(ctx, tenantID, "missing-ref"); !errors.Is(err, walletstore.ErrPSPTransactionNotFound) {
		t.Fatalf("transaction lookup error = %v, want %v", err, walletstore.ErrPSPTransactionNotFound)
	}

	var interactions int
	stmt = db.Rebind(`SELECT COUNT(*) FROM psp_interactions
		WHERE tenant_id = ? AND psp_provider = ? AND client_reference = ? AND status_code = ?`)
	if err := db.GetContext(ctx, &interactions, stmt, tenantID, providerCode, "missing-ref", http.StatusNotFound); err != nil {
		t.Fatalf("count psp interactions: %v", err)
	}
	if interactions != 1 {
		t.Fatalf("interactions = %d, want 1", interactions)
	}
}

func TestAuthorizeUnsignedWebhookRequiresMappedPSPTransactionID(t *testing.T) {
	provider := &acceptingWebhookProvider{code: "handler-noop"}
	handler := &PSPWebhookHandler{}
	var authorizeErr error

	app := fiber.New()
	app.Post("/webhook", func(c *fiber.Ctx) error {
		_, _, authorizeErr = handler.authorizeUnsignedWebhook(
			c,
			&walletpsp.Config{
				WebhookAuthMode:     "ip_allowlist",
				WebhookAllowedCIDRs: []string{"0.0.0.0/0", "::/0"},
				StatusCheckWebhook:  true,
			},
			provider,
			"handler-noop",
			"tenant",
			"client-ref",
			"",
			"deposit",
			map[string]any{"ref": "client-ref"},
			[]byte(`{"ref":"client-ref"}`),
		)
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{"ref":"client-ref"}`))
	req.Header.Set(fiber.HeaderXForwardedFor, "192.0.2.10")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if !errors.Is(authorizeErr, walletstore.ErrMissingPSPTransactionID) {
		t.Fatalf("authorize error = %v, want %v", authorizeErr, walletstore.ErrMissingPSPTransactionID)
	}
	if provider.statusCalls != 0 {
		t.Fatalf("status calls = %d, want 0", provider.statusCalls)
	}
}

type acceptingWebhookProvider struct {
	code        string
	statusCalls int
}

func (p *acceptingWebhookProvider) VerifyDeposit(context.Context, string) (*walletpsp.DepositVerification, error) {
	return nil, walletpsp.ErrPSPPermanent
}

func (p *acceptingWebhookProvider) SendPayout(context.Context, walletpsp.PayoutRequest) (*walletpsp.PayoutResult, error) {
	return nil, walletpsp.ErrPSPPermanent
}

func (p *acceptingWebhookProvider) GetTransactionStatus(context.Context, string) (*walletpsp.TxStatus, error) {
	p.statusCalls++
	return &walletpsp.TxStatus{Status: "success"}, nil
}

func (p *acceptingWebhookProvider) VerifyWebhook([]byte, string) bool {
	return true
}

func (p *acceptingWebhookProvider) Code() string {
	return p.code
}

func (p *acceptingWebhookProvider) SupportedOperations() []walletpsp.Operation {
	return []walletpsp.Operation{walletpsp.OperationDeposit, walletpsp.OperationWithdrawal}
}
