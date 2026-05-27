package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
)

func TestUpdatePSPTransactionStatus_PreservesConfirmedAtAndRetryCount(t *testing.T) {
	if os.Getenv("DOCKER_HOST") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		t.Skip("docker host not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		t.Skipf("postgres container unavailable: %v", err)
	}
	defer func() {
		_ = container.Terminate(context.Background())
	}()

	dbName := fmt.Sprintf("noebs_wallet_store_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	db, err := basestore.OpenFromConfig(dbURL, "", basestore.DriverPostgres)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	}()

	if err := basestore.MigrateScope(ctx, db, "tenant", basestore.MigrationScopePSPWebhook); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	s := New(db)
	confirmedAt := time.Now().UTC().Truncate(time.Microsecond)
	txn := PSPTransaction{
		TenantID:        "tenant",
		PSPProvider:     "noop",
		IdempotencyKey:  "idem-1",
		ClientReference: "ref-1",
		Direction:       "inbound",
		Amount:          100,
		Currency:        "USD",
		Status:          "success",
		ConfirmedAt:     sql.NullTime{Time: confirmedAt, Valid: true},
		RetryCount:      7,
	}
	if _, err := s.CreatePSPTransaction(ctx, txn); err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	interaction, err := s.RecordPSPInteraction(ctx, PSPInteraction{
		TenantID:         txn.TenantID,
		PSPProvider:      txn.PSPProvider,
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		ClientReference:  sql.NullString{String: txn.ClientReference, Valid: true},
		Direction:        sql.NullString{String: txn.Direction, Valid: true},
		InteractionType:  "status_check",
		Method:           sql.NullString{String: "GET", Valid: true},
		URL:              sql.NullString{String: "https://psp.example/transactions/psp-1", Valid: true},
		RequestHeaders:   RawJSON(`{"x-api-key":"REDACTED"}`),
		RequestBody:      RawJSON(`{"transaction_id":"psp-1"}`),
		ResponseBody:     RawJSON(`{"status":"success"}`),
		StatusCode:       sql.NullInt64{Int64: 200, Valid: true},
	})
	if err != nil {
		t.Fatalf("record psp interaction: %v", err)
	}
	if interaction.ID <= 0 {
		t.Fatalf("expected interaction id, got %d", interaction.ID)
	}
	if interaction.InteractionType != "status_check" {
		t.Fatalf("expected interaction type status_check, got %q", interaction.InteractionType)
	}

	update := PSPStatusUpdate{
		Status:          "success",
		ResponseMessage: sql.NullString{String: "ok", Valid: true},
	}
	if err := s.UpdatePSPTransactionStatus(ctx, txn.TenantID, txn.ClientReference, update); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := s.GetPSPTransactionByReference(ctx, txn.TenantID, txn.ClientReference)
	if err != nil {
		t.Fatalf("get transaction: %v", err)
	}
	if got.RetryCount != txn.RetryCount {
		t.Fatalf("expected retry_count %d, got %d", txn.RetryCount, got.RetryCount)
	}
	if !got.ConfirmedAt.Valid {
		t.Fatalf("expected confirmed_at to remain set")
	}
	if !got.ConfirmedAt.Time.Equal(confirmedAt) {
		t.Fatalf("expected confirmed_at %v, got %v", confirmedAt, got.ConfirmedAt.Time)
	}

	if err := s.UpdatePSPTransactionStatus(ctx, txn.TenantID, txn.ClientReference, PSPStatusUpdate{Status: "pending"}); err != nil {
		t.Fatalf("downgrade terminal status: %v", err)
	}
	got, err = s.GetPSPTransactionByReference(ctx, txn.TenantID, txn.ClientReference)
	if err != nil {
		t.Fatalf("get transaction after downgrade attempt: %v", err)
	}
	if got.Status != "success" {
		t.Fatalf("expected terminal success status to be preserved, got %q", got.Status)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, enabled_currencies,
		is_active, supports_deposit, supports_withdrawal, method_type, display_name,
		supported_regions, min_amount, max_amount, deposit_input_schema, presentation_schema
	) VALUES(
		'tenant', 'globalpay', 'Global Pay', 'https://psp.example',
		ARRAY['USD'], TRUE, TRUE, TRUE, 'redirect', 'Global Pay Checkout',
		ARRAY['US'], 100, 100000, '{"required":["card_last4"]}', '{"kind":"redirect"}'
	)`); err != nil {
		t.Fatalf("insert psp config: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO psp_config_overrides(
		tenant_id, provider_code, region, currency, direction, is_active,
		supports_deposit, supports_withdrawal, enabled_currencies, method_type,
		display_name, supported_regions, min_amount, max_amount, deposit_input_schema,
		presentation_schema
	) VALUES(
		'tenant', 'globalpay', 'AE', 'AED', 'deposit', TRUE,
		TRUE, FALSE, ARRAY['AED'], 'qr', 'Global Pay QR',
		ARRAY['AE'], 500, 50000, '{"required":["bank_account"]}', '{"kind":"qr"}'
	)`); err != nil {
		t.Fatalf("insert psp override: %v", err)
	}

	methods, err := s.ListAvailablePSPMethods(ctx, PSPMethodFilter{
		TenantID:  "tenant",
		Direction: "deposit",
		Currency:  "AED",
		Region:    "AE",
		Amount:    500,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("list payment methods: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected one AE/AED method, got %d", len(methods))
	}
	if methods[0].ProviderCode != "globalpay" || methods[0].MethodType != "qr" {
		t.Fatalf("unexpected method: %+v", methods[0])
	}
	if methods[0].MinAmount.Int64 != 500 || methods[0].MaxAmount.Int64 != 50000 {
		t.Fatalf("expected override amount bounds, got min=%v max=%v", methods[0].MinAmount, methods[0].MaxAmount)
	}
	var inputSchema map[string][]string
	if err := json.Unmarshal(methods[0].InputSchema, &inputSchema); err != nil {
		t.Fatalf("unmarshal input schema: %v", err)
	}
	if got := inputSchema["required"]; len(got) != 1 || got[0] != "bank_account" {
		t.Fatalf("expected override input schema, got %v", inputSchema)
	}

	methods, err = s.ListAvailablePSPMethods(ctx, PSPMethodFilter{
		TenantID:  "tenant",
		Direction: "deposit",
		Currency:  "AED",
		Region:    "AE",
		Amount:    100,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("list payment methods below min: %v", err)
	}
	if len(methods) != 0 {
		t.Fatalf("expected below-min amount to hide method, got %d", len(methods))
	}
}
