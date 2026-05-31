package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	"github.com/shopspring/decimal"
)

func TestPSPTransactionPersistenceReplaysAndStatusUpdates(t *testing.T) {
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

	dbName := fmt.Sprintf("noebs_wallet_store_%d", time.Now().UnixNano())
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
		RawRequest:      RawJSON(`{"client_reference":"ref-1","amount":100}`),
	}
	created, err := s.CreatePSPTransaction(ctx, txn)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	replayed, err := s.CreatePSPTransaction(ctx, txn)
	if err != nil {
		t.Fatalf("idempotent create transaction replay: %v", err)
	}
	if replayed.ClientReference != txn.ClientReference {
		t.Fatalf("replayed transaction ref = %q, want %q", replayed.ClientReference, txn.ClientReference)
	}
	mismatch := txn
	mismatch.Amount++
	if _, err := s.CreatePSPTransaction(ctx, mismatch); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("mismatched create transaction replay error = %v, want %v", err, ErrDuplicateTransaction)
	}
	rawRequestMismatch := txn
	rawRequestMismatch.RawRequest = RawJSON(`{"client_reference":"ref-1","amount":101}`)
	if _, err := s.CreatePSPTransaction(ctx, rawRequestMismatch); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("mismatched create transaction raw request replay error = %v, want %v", err, ErrDuplicateTransaction)
	}

	amount := PSPTransactionAmount{
		TenantID:         txn.TenantID,
		PSPTransactionID: created.ID,
		AmountKind:       PSPAmountReported,
		Amount:           100,
		Currency:         "USD",
		FxRate:           decimal.NullDecimal{Decimal: decimal.RequireFromString("3.75000000"), Valid: true},
		FxBaseCurrency:   sql.NullString{String: "USD", Valid: true},
		FxQuoteCurrency:  sql.NullString{String: "AED", Valid: true},
		FxSource:         sql.NullString{String: "provider", Valid: true},
	}
	storedAmount, err := s.AddPSPTransactionAmount(ctx, amount)
	if err != nil {
		t.Fatalf("add psp amount: %v", err)
	}
	replayedAmount, err := s.AddPSPTransactionAmount(ctx, amount)
	if err != nil {
		t.Fatalf("replay psp amount: %v", err)
	}
	if replayedAmount.ID != storedAmount.ID {
		t.Fatalf("replayed amount id = %d, want %d", replayedAmount.ID, storedAmount.ID)
	}
	mismatchedAmount := amount
	mismatchedAmount.Amount++
	if _, err := s.AddPSPTransactionAmount(ctx, mismatchedAmount); !errors.Is(err, ErrDuplicateAmount) {
		t.Fatalf("mismatched psp amount replay error = %v, want %v", err, ErrDuplicateAmount)
	}

	batch := []PSPTransactionAmountInput{{
		AmountKind: PSPAmountFee,
		Amount:     5,
		Currency:   "USD",
	}}
	storedBatch, err := s.AddPSPTransactionAmounts(ctx, txn.TenantID, created.ID, batch)
	if err != nil {
		t.Fatalf("add psp amount batch: %v", err)
	}
	if len(storedBatch) != 1 {
		t.Fatalf("stored amount batch length = %d, want 1", len(storedBatch))
	}
	replayedBatch, err := s.AddPSPTransactionAmounts(ctx, txn.TenantID, created.ID, batch)
	if err != nil {
		t.Fatalf("replay psp amount batch: %v", err)
	}
	if len(replayedBatch) != 1 || replayedBatch[0].ID != storedBatch[0].ID {
		t.Fatalf("replayed amount batch = %+v, want id %d", replayedBatch, storedBatch[0].ID)
	}
	mismatchedBatch := []PSPTransactionAmountInput{{
		AmountKind: PSPAmountFee,
		Amount:     6,
		Currency:   "USD",
	}}
	if _, err := s.AddPSPTransactionAmounts(ctx, txn.TenantID, created.ID, mismatchedBatch); !errors.Is(err, ErrDuplicateAmount) {
		t.Fatalf("mismatched psp amount batch replay error = %v, want %v", err, ErrDuplicateAmount)
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
		Status:           "success",
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		ResponseMessage:  sql.NullString{String: "ok", Valid: true},
	}
	if err := s.UpdatePSPTransactionStatus(ctx, txn.TenantID, txn.ClientReference, update); err != nil {
		t.Fatalf("update status: %v", err)
	}
	if err := s.UpdatePSPTransactionStatus(ctx, txn.TenantID, txn.ClientReference, PSPStatusUpdate{
		Status:           "success",
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		ResponseMessage:  sql.NullString{String: "changed", Valid: true},
	}); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("terminal response message rewrite error = %v, want %v", err, ErrDuplicateTransaction)
	}
	if err := s.UpdatePSPTransactionStatus(ctx, txn.TenantID, txn.ClientReference, PSPStatusUpdate{
		Status:           "success",
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		ConfirmedAt:      sql.NullTime{Time: confirmedAt.Add(time.Second), Valid: true},
	}); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("terminal confirmed_at rewrite error = %v, want %v", err, ErrDuplicateTransaction)
	}
	if err := s.UpdatePSPTransactionStatus(ctx, txn.TenantID, txn.ClientReference, PSPStatusUpdate{
		Status:           "success",
		PSPTransactionID: sql.NullString{String: "psp-2", Valid: true},
	}); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("provider transaction id mismatch update error = %v, want %v", err, ErrDuplicateTransaction)
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
	if !got.PSPTransactionID.Valid || got.PSPTransactionID.String != "psp-1" {
		t.Fatalf("expected provider transaction id psp-1, got %+v", got.PSPTransactionID)
	}

	if err := s.UpdatePSPTransactionStatus(ctx, txn.TenantID, txn.ClientReference, PSPStatusUpdate{Status: "pending"}); !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("downgrade terminal status error = %v, want %v", err, ErrInvalidStatusTransition)
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

func TestListAvailablePSPMethodsPaginatesAfterScopedEligibility(t *testing.T) {
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

	dbName := fmt.Sprintf("noebs_wallet_store_%d", time.Now().UnixNano())
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

	if err := basestore.MigrateScope(ctx, db, "tenant", basestore.MigrationScopePSPWebhook); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, enabled_currencies,
		is_active, supports_deposit, supports_withdrawal, method_type, display_name,
		supported_regions, min_amount, max_amount, deposit_input_schema, presentation_schema
	) VALUES
		('tenant', 'alpha', 'Alpha Pay', 'https://alpha.example',
		 ARRAY['USD'], FALSE, FALSE, FALSE, 'redirect', 'Alpha Pay',
		 ARRAY['US'], 100, 100000, '{"kind":"redirect"}', '{"kind":"redirect"}'),
		('tenant', 'beta', 'Beta Pay', 'https://beta.example',
		 ARRAY['AED'], TRUE, FALSE, TRUE, 'bank_transfer', 'Beta Pay',
		 ARRAY['AE'], 100, 100000, '{"kind":"bank"}', '{"kind":"bank"}'),
		('tenant', 'gamma', 'Gamma Pay', 'https://gamma.example',
		 ARRAY['USD'], TRUE, TRUE, FALSE, 'card', 'Gamma Pay',
		 ARRAY['AE'], 100, 100000, '{"kind":"card"}', '{"kind":"card"}'),
		('tenant', 'zeta', 'Zeta Pay', 'https://zeta.example',
		 ARRAY['AED'], TRUE, TRUE, FALSE, 'qr', 'Zeta Pay',
		 ARRAY['AE'], 100, 100000, '{"kind":"qr"}', '{"kind":"qr"}')`); err != nil {
		t.Fatalf("insert psp configs: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO psp_config_overrides(
		tenant_id, provider_code, region, currency, direction, is_active,
		supports_deposit, supports_withdrawal, enabled_currencies, method_type,
		display_name, supported_regions, min_amount, max_amount, deposit_input_schema,
		presentation_schema
	) VALUES(
		'tenant', 'alpha', 'AE', 'AED', 'deposit', TRUE,
		TRUE, FALSE, ARRAY['AED'], 'qr', 'Alpha AE QR',
		ARRAY['AE'], 100, 100000, '{"kind":"qr"}', '{"kind":"qr"}'
	)`); err != nil {
		t.Fatalf("insert psp override: %v", err)
	}

	s := New(db)
	methods, err := s.ListAvailablePSPMethods(ctx, PSPMethodFilter{
		TenantID:  "tenant",
		Direction: "deposit",
		Currency:  "AED",
		Region:    "AE",
		Amount:    500,
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected first eligible page to contain alpha override, got %d", len(methods))
	}
	if methods[0].ProviderCode != "alpha" || methods[0].DisplayName != "Alpha AE QR" || methods[0].MethodType != "qr" {
		t.Fatalf("unexpected first eligible method: %+v", methods[0])
	}

	methods, err = s.ListAvailablePSPMethods(ctx, PSPMethodFilter{
		TenantID:  "tenant",
		Direction: "deposit",
		Currency:  "AED",
		Region:    "AE",
		Amount:    500,
		Limit:     1,
		Offset:    1,
	})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected second eligible page to contain zeta, got %d", len(methods))
	}
	if methods[0].ProviderCode != "zeta" {
		t.Fatalf("unexpected second eligible method: %+v", methods[0])
	}
}
