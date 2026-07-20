package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/internal/workloadauth"
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

func TestPSPWebhookHandlerDoesNotReadPublicRoutingSelectors(t *testing.T) {
	data, err := os.ReadFile("psp_webhook.go")
	if err != nil {
		t.Fatalf("read psp_webhook.go: %v", err)
	}
	for _, token := range []string{
		`c.Query("tenant_id")`,
		`c.Query("region")`,
		`c.Query("currency")`,
		`c.Query("direction")`,
		`c.Get(gateway.GatewayTenantIDHeader)`,
	} {
		if strings.Contains(string(data), token) {
			t.Fatalf("psp_webhook.go reads public routing selector %q", token)
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

func TestAuthoritativeWebhookPayloadPreservesIdentityAndUsesStatusCheckFields(t *testing.T) {
	mapping := walletpsp.ResponseMapping{
		ClientReference: []string{"ref"},
		TransactionID:   []string{"psp_tx"},
		Status:          []string{"state"},
		Amount:          []string{"money.amount"},
		Currency:        []string{"money.currency"},
		Direction:       []string{"direction"},
	}
	original := map[string]any{
		"ref":       "client-ref",
		"psp_tx":    "psp-original",
		"state":     "claimed-success",
		"direction": "inbound",
		"money": map[string]any{
			"amount":   "999",
			"currency": "BAD",
		},
	}

	merged := authoritativeWebhookPayload(original, mapping, "client-ref", "psp-original", "deposit", &walletpsp.TxStatus{
		ProviderTxID: "psp-checked",
		Status:       "failed",
		Amount:       1250,
		Currency:     "SDG",
		RawResponse:  map[string]any{"provider_status": "failed"},
	})
	fields, err := mappedPSPWebhookFields(merged, mapping)
	if err != nil {
		t.Fatalf("mappedPSPWebhookFields() error = %v", err)
	}
	if fields.ClientReference != "client-ref" ||
		fields.PSPTransactionID != "psp-checked" ||
		fields.Status != "failed" ||
		fields.Amount != 1250 ||
		fields.Currency != "SDG" ||
		fields.Direction != "deposit" {
		t.Fatalf("fields = %+v, want original identity and authoritative status-check values", fields)
	}
	if merged["provider_status"] != "failed" {
		t.Fatalf("merged payload dropped provider raw response: %+v", merged)
	}
	if original["psp_tx"] != "psp-original" {
		t.Fatalf("original payload was mutated: %+v", original)
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
	if err := basestore.MigrateScope(ctx, db, basestore.MigrationScopePSPWebhook); err != nil {
		t.Fatalf("migrate psp webhook db: %v", err)
	}
	provisionWalletHandlerTestTenant(t, ctx, db, tenantID, "PSP Webhook Tenant")

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

func TestPSPWebhookBindsCallbackToStoredProvider(t *testing.T) {
	fixture := newPSPWebhookTestFixture(t, `{"client_reference":["ref"],"transaction_id":["psp_tx"],"status":["status"],"amount":["amount"],"currency":["currency"],"direction":["direction"]}`)
	signaler := &captureTemporalSignaler{}
	fixture.createPSPTransactionFor(t, "other-provider", "inbound", "provider-bound-ref", "SDG", "workflow-provider-bound")

	status, body := fixture.postWebhook(t, fixture.app(signaler), `{"ref":"provider-bound-ref","psp_tx":"psp-forged","status":"success","amount":1250,"currency":"SDG","direction":"inbound"}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", status, http.StatusNotFound, body)
	}
	if strings.Contains(body, ErrPSPWebhookProviderMismatch.Error()) {
		t.Fatalf("response disclosed provider binding: %s", body)
	}
	if signaler.calls != 0 {
		t.Fatalf("temporal signal calls = %d, want 0", signaler.calls)
	}
	assertPSPWebhookDidNotMutateTransaction(t, fixture.mustGetPSPTransaction(t, "provider-bound-ref"))
}

func TestPSPWebhookRejectsCallbackDirectionMismatch(t *testing.T) {
	fixture := newPSPWebhookTestFixture(t, `{"client_reference":["ref"],"transaction_id":["psp_tx"],"status":["status"],"amount":["amount"],"currency":["currency"],"direction":["direction"]}`)
	signaler := &captureTemporalSignaler{}
	fixture.createPSPTransactionFor(t, fixture.providerCode, "inbound", "direction-bound-ref", "SDG", "workflow-direction-bound")

	status, body := fixture.postWebhook(t, fixture.app(signaler), `{"ref":"direction-bound-ref","psp_tx":"psp-forged","status":"success","amount":1250,"currency":"SDG","direction":"outbound"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", status, http.StatusBadRequest, body)
	}
	if !strings.Contains(body, ErrPSPWebhookDirectionMismatch.Error()) {
		t.Fatalf("response body = %s, want direction mismatch", body)
	}
	if signaler.calls != 0 {
		t.Fatalf("temporal signal calls = %d, want 0", signaler.calls)
	}
	assertPSPWebhookDidNotMutateTransaction(t, fixture.mustGetPSPTransaction(t, "direction-bound-ref"))
}

func TestPSPWebhookAcceptsStoredDepositAndWithdrawalDirections(t *testing.T) {
	fixture := newPSPWebhookTestFixture(t, `{"client_reference":["ref"],"transaction_id":["psp_tx"],"status":["status"],"amount":["amount"],"currency":["currency"],"direction":["direction"]}`)
	app := fixture.app(nil)

	tests := []struct {
		name              string
		storedDirection   string
		callbackDirection string
	}{
		{name: "deposit", storedDirection: "inbound", callbackDirection: "deposit"},
		{name: "withdrawal", storedDirection: "outbound", callbackDirection: "payout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clientReference := "valid-" + test.name + "-ref"
			fixture.createPSPTransactionFor(t, fixture.providerCode, test.storedDirection, clientReference, "SDG", "")
			payload := fmt.Sprintf(`{"ref":%q,"psp_tx":%q,"status":"success","amount":1250,"currency":"SDG","direction":%q}`, clientReference, "psp-"+test.name, test.callbackDirection)

			status, body := fixture.postWebhook(t, app, payload)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", status, http.StatusOK, body)
			}
			stored := fixture.mustGetPSPTransaction(t, clientReference)
			if stored.Status != walletstore.PSPStatusSuccess || !stored.PSPTransactionID.Valid || stored.PSPTransactionID.String != "psp-"+test.name {
				t.Fatalf("stored transaction = %+v, want successful %s callback", stored, test.name)
			}
		})
	}
}

func TestValidatePSPWebhookTransactionReturnsTypedBindingErrors(t *testing.T) {
	stored := &walletstore.PSPTransaction{PSPProvider: "provider-a", Direction: "inbound"}
	if err := validatePSPWebhookTransaction(stored, "provider-b", "deposit"); !errors.Is(err, ErrPSPWebhookProviderMismatch) {
		t.Fatalf("provider mismatch error = %v, want %v", err, ErrPSPWebhookProviderMismatch)
	}
	if err := validatePSPWebhookTransaction(stored, "provider-a", "withdrawal"); !errors.Is(err, ErrPSPWebhookDirectionMismatch) {
		t.Fatalf("direction mismatch error = %v, want %v", err, ErrPSPWebhookDirectionMismatch)
	}
	if err := validatePSPWebhookTransaction(stored, "provider-a", ""); err != nil {
		t.Fatalf("callback without a mapped direction error = %v, want nil", err)
	}
}

func assertPSPWebhookDidNotMutateTransaction(t *testing.T, stored *walletstore.PSPTransaction) {
	t.Helper()
	if stored.Status != walletstore.PSPStatusPending {
		t.Fatalf("stored status = %q, want pending", stored.Status)
	}
	if stored.PSPTransactionID.Valid {
		t.Fatalf("stored psp transaction id = %q, want unset", stored.PSPTransactionID.String)
	}
	if len(stored.RawResponse) != 0 {
		t.Fatalf("stored raw response = %s, want unset", stored.RawResponse)
	}
	if stored.ConfirmedAt.Valid {
		t.Fatalf("stored confirmed_at = %s, want unset", stored.ConfirmedAt.Time)
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

func TestAuthorizeIPAllowedWebhookRequiresMappedPSPTransactionID(t *testing.T) {
	provider := &acceptingWebhookProvider{code: "handler-noop"}
	handler := &PSPWebhookHandler{}
	var authorizeErr error

	app := fiber.New()
	app.Post("/webhook", gateway.InternalTenantIdentityMiddleware(), func(c *fiber.Ctx) error {
		_, _, authorizeErr = handler.authorizeIPAllowedWebhook(
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
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant")
	req.Header.Set(gateway.GatewaySourceIPHeader, "192.0.2.10")
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

func TestAuthorizeIPAllowedWebhookUsesAuthenticatedSourceIP(t *testing.T) {
	tests := []struct {
		name           string
		fiber          fiber.Config
		forwarded      string
		peer           string
		sourceIP       string
		allowedCIDR    string
		wantAuthorized bool
	}{
		{
			name:           "authenticated source",
			fiber:          fiber.Config{ProxyHeader: fiber.HeaderXForwardedFor},
			forwarded:      "192.0.2.10",
			sourceIP:       "198.51.100.10",
			allowedCIDR:    "198.51.100.0/24",
			wantAuthorized: true,
		},
		{
			name:        "forwarded header",
			fiber:       fiber.Config{ProxyHeader: fiber.HeaderXForwardedFor},
			forwarded:   "192.0.2.10",
			sourceIP:    "198.51.100.10",
			allowedCIDR: "192.0.2.0/24",
		},
		{
			name:        "gateway peer",
			peer:        "192.0.2.10",
			sourceIP:    "198.51.100.10",
			allowedCIDR: "192.0.2.0/24",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var authorizeErr error
			app := fiber.New(test.fiber)
			app.Post("/webhook", gateway.InternalTenantIdentityMiddleware(), func(c *fiber.Ctx) error {
				if test.peer != "" {
					c.Context().SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP(test.peer)})
				}
				if got := c.IP(); got != "192.0.2.10" {
					t.Fatalf("test request IP = %q, want spoofable/peer IP 192.0.2.10", got)
				}
				_, _, authorizeErr = (&PSPWebhookHandler{}).authorizeIPAllowedWebhook(
					c,
					&walletpsp.Config{
						WebhookAuthMode:     "ip_allowlist",
						WebhookAllowedCIDRs: []string{test.allowedCIDR},
					},
					&acceptingWebhookProvider{code: "handler-noop"},
					"handler-noop",
					"tenant",
					"client-ref",
					"psp-transaction",
					"deposit",
					map[string]any{"ref": "client-ref"},
					[]byte(`{"ref":"client-ref"}`),
				)
				return c.SendStatus(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			req.Header.Set(gateway.GatewayTenantIDHeader, "tenant")
			req.Header.Set(gateway.GatewaySourceIPHeader, test.sourceIP)
			if test.forwarded != "" {
				req.Header.Set(fiber.HeaderXForwardedFor, test.forwarded)
			}
			resp, err := app.Test(req, pspWebhookRouteTestTimeout)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer closeWalletResponseBody(t, resp.Body)
			if test.wantAuthorized && authorizeErr != nil {
				t.Fatalf("authorize error = %v, want authenticated source to be allowed", authorizeErr)
			}
			if !test.wantAuthorized && !errors.Is(authorizeErr, walletpsp.ErrPSPWebhookInvalid) {
				t.Fatalf("authorize error = %v, want %v", authorizeErr, walletpsp.ErrPSPWebhookInvalid)
			}
		})
	}
}

func TestWebhookAuthorizationModesAreExact(t *testing.T) {
	tests := []struct {
		name           string
		mode           string
		signatureValid bool
		sourceIP       string
		wantAuthorized bool
	}{
		{name: "signature accepts signature", mode: "signature", signatureValid: true, sourceIP: "198.51.100.10", wantAuthorized: true},
		{name: "signature rejects allowed IP", mode: "signature", sourceIP: "192.0.2.10"},
		{name: "IP rejects valid signature from wrong source", mode: "ip_allowlist", signatureValid: true, sourceIP: "198.51.100.10"},
		{name: "IP accepts allowed source without signature", mode: "ip_allowlist", sourceIP: "192.0.2.10", wantAuthorized: true},
		{name: "removed OR mode rejects signature", mode: "signature_or_ip_allowlist", signatureValid: true, sourceIP: "198.51.100.10"},
		{name: "removed OR mode rejects allowed source", mode: "signature_or_ip_allowlist", sourceIP: "192.0.2.10"},
		{name: "unknown mode rejects", mode: "unknown", signatureValid: true, sourceIP: "192.0.2.10"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var authorizeErr error
			app := fiber.New()
			app.Post("/webhook", func(c *fiber.Ctx) error {
				c.Locals("request_source", test.sourceIP)
				_, _, authorizeErr = (&PSPWebhookHandler{}).authorizeWebhook(
					c,
					&walletpsp.Config{WebhookAuthMode: test.mode, WebhookAllowedCIDRs: []string{"192.0.2.0/24"}},
					&acceptingWebhookProvider{code: "handler-noop"},
					"handler-noop",
					"tenant",
					pspWebhookFields{},
					test.signatureValid,
					map[string]any{"status": "success"},
					[]byte(`{"status":"success"}`),
				)
				return c.SendStatus(http.StatusNoContent)
			})

			resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/webhook", nil), pspWebhookRouteTestTimeout)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer closeWalletResponseBody(t, resp.Body)
			if test.wantAuthorized && authorizeErr != nil {
				t.Fatalf("authorize error = %v, want success", authorizeErr)
			}
			if !test.wantAuthorized && !errors.Is(authorizeErr, walletpsp.ErrPSPWebhookInvalid) {
				t.Fatalf("authorize error = %v, want %v", authorizeErr, walletpsp.ErrPSPWebhookInvalid)
			}
		})
	}
}

func TestWebhookAuditHeadersRedactCredentials(t *testing.T) {
	var captured walletstore.RawJSON
	app := fiber.New()
	app.Post("/webhook", func(c *fiber.Ctx) error {
		var err error
		captured, err = webhookAuditHeaders(c)
		if err != nil {
			return err
		}
		return c.SendStatus(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Webhook-Signature", "webhook-secret")
	req.Header.Set(workloadauth.HeaderSignature, "workload-secret")
	req.Header.Set(workloadauth.HeaderSubject, "subject-secret")
	req.Header.Set("X-Provider-Event", "event-123")
	resp, err := app.Test(req, pspWebhookRouteTestTimeout)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer closeWalletResponseBody(t, resp.Body)

	var headers map[string][]string
	if err := json.Unmarshal(captured, &headers); err != nil {
		t.Fatalf("decode audit headers: %v", err)
	}
	value := func(name string) string {
		for key, values := range headers {
			if strings.EqualFold(key, name) && len(values) != 0 {
				return values[0]
			}
		}
		return ""
	}
	for _, name := range []string{"Authorization", "X-Webhook-Signature", workloadauth.HeaderSignature, workloadauth.HeaderSubject} {
		if got := value(name); got != "REDACTED" {
			t.Fatalf("audit header %s = %q, want REDACTED", name, got)
		}
	}
	if got := value("X-Provider-Event"); got != "event-123" {
		t.Fatalf("benign audit header = %q, want event-123", got)
	}
}

func TestWebhookSignatureHasOneCanonicalHeader(t *testing.T) {
	tests := []struct {
		name      string
		headers   http.Header
		wantValue string
	}{
		{name: "canonical", headers: http.Header{"X-Webhook-Signature": {"signature"}}, wantValue: "signature"},
		{name: "legacy alias", headers: http.Header{"X-Signature": {"signature"}}},
		{name: "duplicate", headers: http.Header{"X-Webhook-Signature": {"one", "two"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got string
			app := fiber.New()
			app.Post("/webhook", func(c *fiber.Ctx) error {
				got = webhookSignature(c)
				return c.SendStatus(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			req.Header = test.headers
			resp, err := app.Test(req, pspWebhookRouteTestTimeout)
			if err != nil {
				t.Fatal(err)
			}
			defer closeWalletResponseBody(t, resp.Body)
			if got != test.wantValue {
				t.Fatalf("webhook signature = %q, want %q", got, test.wantValue)
			}
		})
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
	if err := basestore.MigrateScope(ctx, db, basestore.MigrationScopePSPWebhook); err != nil {
		t.Fatalf("migrate psp webhook db: %v", err)
	}
	provisionWalletHandlerTestTenant(t, ctx, db, tenantID, "PSP Replay Tenant")

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
	f.createPSPTransactionFor(t, f.providerCode, "inbound", clientReference, currency, workflowID)
}

func (f *pspWebhookTestFixture) createPSPTransactionFor(t *testing.T, providerCode, direction, clientReference, currency, workflowID string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := f.store.CreatePSPTransaction(ctx, walletstore.PSPTransaction{
		TenantID:        f.tenantID,
		PSPProvider:     providerCode,
		IdempotencyKey:  clientReference + "-idempotency",
		ClientReference: clientReference,
		Direction:       direction,
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
