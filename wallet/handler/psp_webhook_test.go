package handler

import (
	"bytes"
	"context"
	"database/sql"
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
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/gofiber/fiber/v2"
)

const pspWebhookRouteTestTimeout = 5_000

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

func TestPSPWebhookRequiresValidatedGatewayTenantIdentity(t *testing.T) {
	app := fiber.New()
	handler := &PSPWebhookHandler{Store: &walletstore.Store{}}
	app.Post("/psp/webhooks/:provider", handler.Handle)

	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/noop?tenant_id=tenant-from-query", bytes.NewBufferString(`{"tenant_id":"tenant-from-payload"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant-from-public-header")

	resp, err := app.Test(req, pspWebhookRouteTestTimeout)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestPSPWebhookHandlerDoesNotReadPublicTenantQuery(t *testing.T) {
	data, err := os.ReadFile("psp_webhook.go")
	if err != nil {
		t.Fatalf("read psp_webhook.go: %v", err)
	}
	for _, token := range []string{`c.Query("tenant_id")`, `c.Get(gateway.GatewayTenantIDHeader)`} {
		if strings.Contains(string(data), token) {
			t.Fatalf("psp_webhook.go reads %q; webhook tenant must come from validated gateway identity", token)
		}
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

	fields, err := mappedPSPWebhookFields(payload, walletpsp.ResponseMapping{})
	if err != nil {
		t.Fatalf("mappedPSPWebhookFields() error = %v", err)
	}
	if fields.ClientReference != "" ||
		fields.PSPTransactionID != "" ||
		fields.Status != "" ||
		fields.Amount != 0 ||
		fields.Currency != "" ||
		fields.Direction != "" ||
		fields.Message != "" {
		t.Fatalf("fields = %+v, want empty without configured paths", fields)
	}

	fields, err = mappedPSPWebhookFields(payload, walletpsp.ResponseMapping{
		ClientReference: []string{"client_reference"},
		TransactionID:   []string{"psp_transaction_id"},
		Status:          []string{"status"},
		Amount:          []string{"amount"},
		Currency:        []string{"currency"},
		Direction:       []string{"direction"},
		Message:         []string{"message"},
	})
	if err != nil {
		t.Fatalf("mappedPSPWebhookFields() error = %v", err)
	}
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

func TestMappedPSPWebhookFieldsRejectsInvalidAmount(t *testing.T) {
	_, err := mappedPSPWebhookFields(
		map[string]any{"amount": "12.34"},
		walletpsp.ResponseMapping{Amount: []string{"amount"}},
	)
	if !errors.Is(err, walletpsp.ErrPSPResponseInvalid) {
		t.Fatalf("mappedPSPWebhookFields() error = %v, want %v", err, walletpsp.ErrPSPResponseInvalid)
	}
}

func TestPSPWebhookStatusUpdateDoesNotRewriteConfirmedAtOnTerminalReplay(t *testing.T) {
	confirmedAt := time.Date(2026, time.May, 31, 12, 0, 0, 0, time.UTC)
	payload := []byte(`{"status":"success","psp_tx":"psp-1","message":"ok"}`)
	existing := &walletstore.PSPTransaction{
		Status:           walletstore.PSPStatusSuccess,
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		ResponseMessage:  sql.NullString{String: "ok", Valid: true},
		RawResponse:      walletstore.RawJSON(payload),
		ConfirmedAt:      sql.NullTime{Time: confirmedAt, Valid: true},
	}
	fields := pspWebhookFields{
		PSPTransactionID: "psp-1",
		Status:           walletstore.PSPStatusSuccess,
		Message:          "ok",
	}

	replay := pspWebhookStatusUpdate(existing, fields, payload, confirmedAt.Add(time.Hour))
	if replay.ConfirmedAt.Valid {
		t.Fatalf("terminal replay confirmed_at = %+v, want unset so store preserves existing evidence", replay.ConfirmedAt)
	}
	if err := walletstore.ValidatePSPStatusUpdate(existing, replay); err != nil {
		t.Fatalf("ValidatePSPStatusUpdate(replay) error = %v", err)
	}

	firstSuccess := pspWebhookStatusUpdate(&walletstore.PSPTransaction{Status: walletstore.PSPStatusPending}, fields, payload, confirmedAt)
	if !firstSuccess.ConfirmedAt.Valid || !firstSuccess.ConfirmedAt.Time.Equal(confirmedAt) {
		t.Fatalf("first success confirmed_at = %+v, want %s", firstSuccess.ConfirmedAt, confirmedAt)
	}
}

func TestPSPWebhookRejectsUnknownClientReference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
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
	app.Post("/psp/webhooks/:provider", gateway.InternalTenantIdentityMiddleware(), NewPSPWebhookHandler(store, loader, registry, nil).Handle)

	payload := `{"ref":"missing-ref","psp_tx":"psp-missing","status":"success","amount":1250,"currency":"SDG","direction":"inbound"}`
	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/"+providerCode+"?tenant_id=default", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gateway.GatewayTenantIDHeader, tenantID)

	resp, err := app.Test(req, pspWebhookRouteTestTimeout)
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

func TestPSPWebhookSignalsMappedCurrencyWithoutStoredCurrencyFallback(t *testing.T) {
	fixture := newPSPWebhookTestFixture(t, `{"client_reference":["ref"],"transaction_id":["psp_tx"],"status":["status"],"amount":["amount"],"currency":["currency"],"direction":["direction"]}`)
	signaler := &captureTemporalSignaler{}
	app := fixture.app(signaler)
	fixture.createPSPTransaction(t, "explicit-currency-ref", "SDG", "workflow-explicit")

	status, body := fixture.postWebhook(t, app, `{"ref":"explicit-currency-ref","psp_tx":"psp-explicit","status":"success","amount":1250,"currency":"AED","direction":"inbound"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", status, http.StatusOK, body)
	}
	if signaler.calls != 1 {
		t.Fatalf("temporal signal calls = %d, want 1", signaler.calls)
	}
	if signaler.workflowID != "workflow-explicit" {
		t.Fatalf("workflow id = %q, want workflow-explicit", signaler.workflowID)
	}
	if signaler.signalName != walletworkflow.PSPStatusUpdateSignal {
		t.Fatalf("signal name = %q, want %q", signaler.signalName, walletworkflow.PSPStatusUpdateSignal)
	}
	signal, ok := signaler.arg.(walletpsp.TxStatus)
	if !ok {
		t.Fatalf("signal arg type = %T, want walletpsp.TxStatus", signaler.arg)
	}
	if signal.Currency != "AED" {
		t.Fatalf("signal currency = %q, want mapped webhook currency AED", signal.Currency)
	}
}

func TestPSPWebhookRejectsWorkflowWebhookWithoutMappedCurrency(t *testing.T) {
	fixture := newPSPWebhookTestFixture(t, `{"client_reference":["ref"],"transaction_id":["psp_tx"],"status":["status"],"amount":["amount"],"currency":["currency"],"direction":["direction"]}`)
	signaler := &captureTemporalSignaler{}
	app := fixture.app(signaler)
	fixture.createPSPTransaction(t, "missing-currency-ref", "SDG", "workflow-missing-currency")

	status, body := fixture.postWebhook(t, app, `{"ref":"missing-currency-ref","psp_tx":"psp-missing-currency","status":"success","amount":1250,"direction":"inbound"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", status, http.StatusBadRequest, body)
	}
	if signaler.calls != 0 {
		t.Fatalf("temporal signal calls = %d, want 0", signaler.calls)
	}
	stored := fixture.mustGetPSPTransaction(t, "missing-currency-ref")
	if stored.Status != "pending" {
		t.Fatalf("stored status = %q, want pending", stored.Status)
	}
	if stored.PSPTransactionID.Valid {
		t.Fatalf("stored psp transaction id = %q, want unset", stored.PSPTransactionID.String)
	}
}

func TestPSPWebhookRejectsWorkflowWebhookWithoutTemporalSignaler(t *testing.T) {
	fixture := newPSPWebhookTestFixture(t, `{"client_reference":["ref"],"transaction_id":["psp_tx"],"status":["status"],"amount":["amount"],"currency":["currency"],"direction":["direction"]}`)
	app := fixture.app(nil)
	fixture.createPSPTransaction(t, "missing-temporal-ref", "SDG", "workflow-missing-temporal")

	status, body := fixture.postWebhook(t, app, `{"ref":"missing-temporal-ref","psp_tx":"psp-missing-temporal","status":"success","amount":1250,"currency":"SDG","direction":"inbound"}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", status, http.StatusServiceUnavailable, body)
	}
	stored := fixture.mustGetPSPTransaction(t, "missing-temporal-ref")
	if stored.Status != "pending" {
		t.Fatalf("stored status = %q, want pending", stored.Status)
	}
	if stored.PSPTransactionID.Valid {
		t.Fatalf("stored psp transaction id = %q, want unset", stored.PSPTransactionID.String)
	}
}

func TestPSPWebhookSignalFailureIsRetriable(t *testing.T) {
	fixture := newPSPWebhookTestFixture(t, `{"client_reference":["ref"],"transaction_id":["psp_tx"],"status":["status"],"amount":["amount"],"currency":["currency"],"direction":["direction"]}`)
	fixture.createPSPTransaction(t, "signal-retry-ref", "SDG", "workflow-signal-retry")
	payload := `{"ref":"signal-retry-ref","psp_tx":"psp-signal-retry","status":"success","amount":1250,"currency":"SDG","direction":"inbound"}`

	failing := &captureTemporalSignaler{err: errors.New("temporal unavailable")}
	statusCode, body := fixture.postWebhook(t, fixture.app(failing), payload)
	if statusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", statusCode, http.StatusServiceUnavailable, body)
	}
	if failing.calls != 1 {
		t.Fatalf("failing signal calls = %d, want 1", failing.calls)
	}
	storedAfterFailure := fixture.mustGetPSPTransaction(t, "signal-retry-ref")
	if storedAfterFailure.Status != walletstore.PSPStatusSuccess {
		t.Fatalf("stored status after signal failure = %q, want success", storedAfterFailure.Status)
	}
	if !storedAfterFailure.ConfirmedAt.Valid {
		t.Fatal("stored confirmed_at should be set after first success webhook")
	}

	signaler := &captureTemporalSignaler{}
	statusCode, body = fixture.postWebhook(t, fixture.app(signaler), payload)
	if statusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want %d, body=%s", statusCode, http.StatusOK, body)
	}
	if signaler.calls != 1 {
		t.Fatalf("retry signal calls = %d, want 1", signaler.calls)
	}
	storedAfterRetry := fixture.mustGetPSPTransaction(t, "signal-retry-ref")
	if !storedAfterRetry.ConfirmedAt.Valid || storedAfterRetry.ConfirmedAt.Time.Sub(storedAfterFailure.ConfirmedAt.Time).Abs() >= time.Millisecond {
		t.Fatalf("retry confirmed_at = %+v, want preserved %+v", storedAfterRetry.ConfirmedAt, storedAfterFailure.ConfirmedAt)
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
	resp, err := app.Test(req, pspWebhookRouteTestTimeout)
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

type pspWebhookTestFixture struct {
	store        *walletstore.Store
	loader       *walletpsp.Loader
	registry     *walletpsp.Registry
	tenantID     string
	providerCode string
}

func newPSPWebhookTestFixture(t *testing.T, mapping string) *pspWebhookTestFixture {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	dbName := fmt.Sprintf("noebs_psp_webhook_workflow_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	db, err := basestore.OpenFromConfig(dbURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	})

	const tenantID = "tenant"
	const providerCode = "handler-noop"
	if err := basestore.MigrateScope(ctx, db, tenantID, basestore.MigrationScopePSPWebhook); err != nil {
		t.Fatalf("migrate psp webhook db: %v", err)
	}
	if err := basestore.New(db).EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}

	stmt := db.Rebind(`INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, enabled_currencies,
		is_active, supports_deposit, supports_withdrawal, webhook_response_mapping
	) VALUES(?, ?, ?, ?, ARRAY['SDG', 'AED'], TRUE, TRUE, TRUE, ?::jsonb)`)
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

	return &pspWebhookTestFixture{
		store:        store,
		loader:       loader,
		registry:     registry,
		tenantID:     tenantID,
		providerCode: providerCode,
	}
}

func (f *pspWebhookTestFixture) app(signaler TemporalSignaler) *fiber.App {
	app := fiber.New(fiber.Config{ProxyHeader: fiber.HeaderXForwardedFor})
	app.Post("/psp/webhooks/:provider", gateway.InternalTenantIdentityMiddleware(), NewPSPWebhookHandler(f.store, f.loader, f.registry, signaler).Handle)
	return app
}

func (f *pspWebhookTestFixture) createPSPTransaction(t *testing.T, clientReference, currency, workflowID string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := f.store.CreatePSPTransaction(ctx, walletstore.PSPTransaction{
		TenantID:        f.tenantID,
		PSPProvider:     f.providerCode,
		IdempotencyKey:  clientReference + "-idempotency",
		ClientReference: clientReference,
		Direction:       "inbound",
		Amount:          1250,
		Currency:        currency,
		Status:          "pending",
		WorkflowID:      sql.NullString{String: workflowID, Valid: workflowID != ""},
	})
	if err != nil {
		t.Fatalf("create psp transaction: %v", err)
	}
}

func (f *pspWebhookTestFixture) postWebhook(t *testing.T, app *fiber.App, payload string) (int, string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/"+f.providerCode+"?tenant_id=default", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gateway.GatewayTenantIDHeader, f.tenantID)

	resp, err := app.Test(req, pspWebhookRouteTestTimeout)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp.StatusCode, string(body)
}

func (f *pspWebhookTestFixture) mustGetPSPTransaction(t *testing.T, clientReference string) *walletstore.PSPTransaction {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	txn, err := f.store.GetPSPTransactionByReference(ctx, f.tenantID, clientReference)
	if err != nil {
		t.Fatalf("get psp transaction: %v", err)
	}
	return txn
}

type captureTemporalSignaler struct {
	calls      int
	workflowID string
	signalName string
	arg        interface{}
	err        error
}

func (s *captureTemporalSignaler) SignalWorkflow(_ context.Context, workflowID, runID, signalName string, arg interface{}) error {
	_ = runID
	s.calls++
	s.workflowID = workflowID
	s.signalName = signalName
	s.arg = arg
	return s.err
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
