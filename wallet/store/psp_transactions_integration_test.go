package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	"github.com/google/uuid"
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

	const dbName = "wallet_ledger"
	dbURL, err := container.CreateDatabaseForRole(ctx, dbName, "wallet_ledger_migrate")
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

	if err := basestore.MigrateScope(ctx, db, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	provisionWalletStoreTestTenant(t, ctx, db, "tenant", "PSP Transaction Tenant")
	if _, err := db.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, idempotency_header_name, deposit_response_mapping
	) VALUES('tenant', 'noop', 'No-op PSP', 'https://psp.invalid', 'Idempotency-Key', '{}')`); err != nil {
		t.Fatalf("seed no-op PSP config: %v", err)
	}

	s := New(db)
	wallet, err := s.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: "tenant", OwnerType: OwnerTypeSystem, OwnerID: "psp-test",
		Currency: "USD", CurrencyUnitID: testCurrencyUnitID(t, ctx, s, "USD"), KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure PSP transaction wallet: %v", err)
	}
	aedUnitID := testCurrencyUnitID(t, ctx, s, "AED")
	confirmedAt := time.Now().UTC().Truncate(time.Microsecond)
	txn := PSPTransaction{
		TenantID:            "tenant",
		PSPProvider:         "noop",
		IdempotencyKey:      "idem-1",
		ClientReference:     "ref-1",
		Direction:           "outbound",
		WalletID:            uuid.NullUUID{UUID: wallet.ID, Valid: true},
		OwnerType:           sql.NullString{String: wallet.OwnerType, Valid: true},
		OwnerID:             sql.NullString{String: wallet.OwnerID, Valid: true},
		AllowReturnToSource: sql.NullBool{Bool: true, Valid: true},
		Amount:              100,
		Currency:            "USD",
		CurrencyUnitID:      wallet.CurrencyUnitID,
		Status:              "success",
		ConfirmedAt:         sql.NullTime{Time: confirmedAt, Valid: true},
		RetryCount:          7,
		RawRequest:          RawJSON(`{"client_reference":"ref-1","amount":100}`),
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
		TenantID:              txn.TenantID,
		PSPTransactionID:      created.ID,
		AmountKind:            PSPAmountReported,
		Amount:                100,
		Currency:              "USD",
		CurrencyUnitID:        wallet.CurrencyUnitID,
		FxRate:                decimal.NullDecimal{Decimal: decimal.RequireFromString("3.75000000"), Valid: true},
		FxRateNumerator:       decimal.NullDecimal{Decimal: decimal.NewFromInt(15), Valid: true},
		FxRateDenominator:     decimal.NullDecimal{Decimal: decimal.NewFromInt(4), Valid: true},
		FxBaseCurrency:        sql.NullString{String: "USD", Valid: true},
		FxQuoteCurrency:       sql.NullString{String: "AED", Valid: true},
		FxBaseCurrencyUnitID:  sql.NullInt64{Int64: wallet.CurrencyUnitID, Valid: true},
		FxQuoteCurrencyUnitID: sql.NullInt64{Int64: aedUnitID, Valid: true},
		FxSource:              sql.NullString{String: "provider", Valid: true},
		FxConversionAt:        sql.NullTime{Time: confirmedAt, Valid: true},
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
	nonRepresentableRate := amount
	nonRepresentableRate.AmountKind = PSPAmountSettlement
	nonRepresentableRate.FxRate.Decimal = decimal.RequireFromString("1.123456789")
	if _, err := s.AddPSPTransactionAmount(ctx, nonRepresentableRate); !errors.Is(err, ErrPSPFXRateNotRepresentable) {
		t.Fatalf("non-representable psp FX rate error = %v, want %v", err, ErrPSPFXRateNotRepresentable)
	}
	var settlementCount int
	if err := db.GetContext(ctx, &settlementCount, `SELECT count(*) FROM psp_transaction_amounts
		WHERE tenant_id = $1 AND psp_transaction_id = $2 AND amount_kind = 'settlement'`, txn.TenantID, created.ID); err != nil {
		t.Fatalf("count rejected settlement amount: %v", err)
	}
	if settlementCount != 0 {
		t.Fatalf("rejected non-representable rate persisted %d rows, want 0", settlementCount)
	}

	batch := []PSPTransactionAmountInput{{
		AmountKind:     PSPAmountFee,
		Amount:         5,
		Currency:       "USD",
		CurrencyUnitID: wallet.CurrencyUnitID,
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
		AmountKind:     PSPAmountFee,
		Amount:         6,
		Currency:       "USD",
		CurrencyUnitID: wallet.CurrencyUnitID,
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
	dispatch := PSPInteraction{
		TenantID:        txn.TenantID,
		PSPProvider:     txn.PSPProvider,
		ClientReference: sql.NullString{String: txn.ClientReference, Valid: true},
		Direction:       sql.NullString{String: txn.Direction, Valid: true},
		InteractionType: "payout_send",
		IdempotencyKey:  sql.NullString{String: txn.ClientReference, Valid: true},
		Method:          sql.NullString{String: "POST", Valid: true},
		RequestBody:     RawJSON(`{"client_reference":"ref-1","amount":100}`),
		ResponseBody:    RawJSON(`{"status":"success"}`),
	}
	firstDispatch, err := s.RecordPSPInteraction(ctx, dispatch)
	if err != nil {
		t.Fatalf("record dispatch interaction: %v", err)
	}
	replayedDispatch, err := s.RecordPSPInteraction(ctx, dispatch)
	if err != nil {
		t.Fatalf("replay dispatch interaction: %v", err)
	}
	if replayedDispatch.ID != firstDispatch.ID {
		t.Fatalf("replayed dispatch id = %d, want %d", replayedDispatch.ID, firstDispatch.ID)
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

	pending, err := s.CreatePSPTransaction(ctx, PSPTransaction{
		TenantID:            "tenant",
		PSPProvider:         "noop",
		IdempotencyKey:      "outbox-idem",
		ClientReference:     "outbox-ref",
		Direction:           "outbound",
		WalletID:            uuid.NullUUID{UUID: wallet.ID, Valid: true},
		OwnerType:           sql.NullString{String: wallet.OwnerType, Valid: true},
		OwnerID:             sql.NullString{String: wallet.OwnerID, Valid: true},
		AllowReturnToSource: sql.NullBool{Bool: true, Valid: true},
		Amount:              100,
		Currency:            "USD",
		CurrencyUnitID:      wallet.CurrencyUnitID,
		Status:              PSPStatusPending,
		WorkflowID:          sql.NullString{String: "outbox-workflow", Valid: true},
	})
	if err != nil {
		t.Fatalf("create pending transaction: %v", err)
	}
	notReadyAt := time.Now().UTC().Truncate(time.Microsecond)
	notReady, err := s.AcknowledgePSPWorkflowSignal(ctx, pending.TenantID, pending.ClientReference, notReadyAt, "")
	if err != nil {
		t.Fatalf("consume pending workflow signal: %v", err)
	}
	if notReady.Status != PSPStatusPending || len(notReady.WorkflowSignalPayload) != 0 || notReady.WorkflowSignalDeliveredAt.Valid {
		t.Fatalf("pending workflow signal consumption changed row: %+v", notReady)
	}
	if _, err := s.AcknowledgePSPWorkflowSignal(ctx, pending.TenantID, pending.ClientReference, notReadyAt, "poller-lock"); !errors.Is(err, ErrMissingWorkflowSignal) {
		t.Fatalf("leased pending workflow signal ack error = %v, want %v", err, ErrMissingWorkflowSignal)
	}
	terminalUpdate := PSPStatusUpdate{
		Status:           PSPStatusSuccess,
		PSPTransactionID: sql.NullString{String: "provider-outbox", Valid: true},
		RawResponse:      RawJSON(`{"status":"success","attempt":1}`),
		ConfirmedAt:      sql.NullTime{Time: confirmedAt, Valid: true},
	}
	firstSignal := &PSPWorkflowSignal{
		ProviderTxID: "provider-outbox",
		Amount:       100,
		Currency:     "USD",
		Status:       PSPStatusSuccess,
		RawResponse:  RawJSON(`{"status":"success","attempt":1}`),
	}
	queued, err := s.ApplyExternalPSPStatus(ctx, pending.TenantID, pending.ClientReference, terminalUpdate, firstSignal)
	if err != nil {
		t.Fatalf("queue terminal workflow signal: %v", err)
	}
	if queued.Status != PSPStatusSuccess || len(queued.WorkflowSignalPayload) == 0 || queued.WorkflowSignalDeliveredAt.Valid {
		t.Fatalf("queued transaction = %+v", queued)
	}
	statusDiagnostics := PSPStatusUpdate{
		Status:        PSPStatusSuccess,
		LastErrorType: sql.NullString{String: "provider_terminal", Valid: true},
		LastErrorAt:   sql.NullTime{Time: confirmedAt, Valid: true},
	}
	if err := s.UpdatePSPTransactionStatus(ctx, pending.TenantID, pending.ClientReference, statusDiagnostics); err != nil {
		t.Fatalf("record status diagnostics: %v", err)
	}
	queued, err = s.GetPSPTransactionByReference(ctx, pending.TenantID, pending.ClientReference)
	if err != nil {
		t.Fatalf("reload queued transaction: %v", err)
	}
	firstPayload := append(RawJSON(nil), queued.WorkflowSignalPayload...)
	firstResponse := append(RawJSON(nil), queued.RawResponse...)

	changedUpdate := terminalUpdate
	changedUpdate.RawResponse = RawJSON(`{"status":"success","attempt":2}`)
	changedSignal := &PSPWorkflowSignal{
		ProviderTxID: "provider-outbox",
		Amount:       999,
		Currency:     "AED",
		Status:       PSPStatusSuccess,
		RawResponse:  RawJSON(`{"status":"success","attempt":2}`),
	}
	replayedTerminal, err := s.ApplyExternalPSPStatus(ctx, pending.TenantID, pending.ClientReference, changedUpdate, changedSignal)
	if err != nil {
		t.Fatalf("replay terminal workflow signal: %v", err)
	}
	if string(replayedTerminal.WorkflowSignalPayload) != string(firstPayload) || string(replayedTerminal.RawResponse) != string(firstResponse) {
		t.Fatalf("terminal replay rewrote first snapshot: payload=%s response=%s", replayedTerminal.WorkflowSignalPayload, replayedTerminal.RawResponse)
	}

	pollable, err := s.ListPSPTransactionsForPolling(ctx, pending.TenantID, 10)
	if err != nil {
		t.Fatalf("list pending workflow signals: %v", err)
	}
	if len(pollable) != 1 || pollable[0].ClientReference != pending.ClientReference ||
		!pollable[0].PSPTransactionID.Valid || pollable[0].PSPTransactionID.String != "provider-outbox" {
		t.Fatalf("pending workflow signal rows = %+v", pollable)
	}

	lockToken := "outbox-lock"
	acquired, err := s.TryAcquirePSPTransactionLock(ctx, pending.TenantID, pending.ClientReference, lockToken, time.Now().UTC().Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("acquire workflow signal lease: acquired=%v err=%v", acquired, err)
	}
	deliveredAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := s.AcknowledgePSPWorkflowSignal(ctx, pending.TenantID, pending.ClientReference, deliveredAt, "stale-lock"); !errors.Is(err, ErrPSPTransactionLockLost) {
		t.Fatalf("stale workflow signal ack error = %v, want %v", err, ErrPSPTransactionLockLost)
	}
	acked, err := s.AcknowledgePSPWorkflowSignal(ctx, pending.TenantID, pending.ClientReference, deliveredAt, lockToken)
	if err != nil {
		t.Fatalf("ack workflow signal: %v", err)
	}
	replayedAck, err := s.AcknowledgePSPWorkflowSignal(ctx, pending.TenantID, pending.ClientReference, deliveredAt.Add(time.Minute), "different-lock")
	if err != nil {
		t.Fatalf("replay workflow signal ack: %v", err)
	}
	if !acked.WorkflowSignalDeliveredAt.Valid || !acked.WorkflowSignalDeliveredAt.Time.Equal(deliveredAt) || acked.LockToken.Valid || acked.LockExpiresAt.Valid {
		t.Fatalf("acknowledged workflow signal = %+v", acked)
	}
	if !replayedAck.WorkflowSignalDeliveredAt.Time.Equal(deliveredAt) || string(replayedAck.WorkflowSignalPayload) != string(firstPayload) || string(replayedAck.RawResponse) != string(firstResponse) {
		t.Fatalf("ack replay rewrote row snapshot: %+v", replayedAck)
	}
	if !replayedAck.LastErrorType.Valid || replayedAck.LastErrorType.String != statusDiagnostics.LastErrorType.String || !replayedAck.LastErrorAt.Valid || !replayedAck.LastErrorAt.Time.Equal(statusDiagnostics.LastErrorAt.Time) {
		t.Fatalf("ack replay changed status diagnostics: %+v", replayedAck)
	}
	pollable, err = s.ListPSPTransactionsForPolling(ctx, pending.TenantID, 10)
	if err != nil {
		t.Fatalf("list after workflow signal ack: %v", err)
	}
	if len(pollable) != 0 {
		t.Fatalf("pollable transactions after ack = %+v, want none", pollable)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, idempotency_header_name, enabled_currencies,
		is_active, supports_deposit, supports_withdrawal, method_type, display_name,
		supported_regions, deposit_input_schema, presentation_schema,
		deposit_response_mapping
	) VALUES(
		'tenant', 'globalpay', 'Global Pay', 'https://psp.example', 'Idempotency-Key',
		ARRAY['USD'], TRUE, TRUE, TRUE, 'redirect', 'Global Pay Checkout',
		ARRAY['US'], '{"required":["card_last4"]}', '{"kind":"redirect"}',
		'{"client_reference":["client_reference"],"transaction_id":["transaction_id"],"status":["status"],"amount":["amount"],"currency":["currency"]}'
	)`); err != nil {
		t.Fatalf("insert psp config: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO psp_config_overrides(
		tenant_id, provider_code, region, currency, direction, is_active,
		supports_deposit, supports_withdrawal, enabled_currencies, method_type,
		display_name, supported_regions, deposit_input_schema,
		presentation_schema
	) VALUES(
		'tenant', 'globalpay', 'AE', 'AED', 'deposit', TRUE,
		TRUE, FALSE, ARRAY['AED'], 'qr', 'Global Pay QR',
		ARRAY['AE'], '{"required":["bank_account"]}', '{"kind":"qr"}'
	)`); err != nil {
		t.Fatalf("insert psp override: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO psp_amount_policies(
		tenant_id, provider_code, currency, currency_unit_version_id,
		direction, region, min_amount, max_amount
	) VALUES('tenant', 'globalpay', 'AED', $1, 'deposit', 'AE', 500, 50000)`, aedUnitID); err != nil {
		t.Fatalf("insert PSP amount policy: %v", err)
	}

	methods, err := s.ListAvailablePSPMethods(ctx, PSPMethodFilter{
		TenantID:       "tenant",
		Direction:      "deposit",
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		Region:         "AE",
		Amount:         500,
		Limit:          10,
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
		TenantID:       "tenant",
		Direction:      "deposit",
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		Region:         "AE",
		Amount:         100,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("list payment methods below min: %v", err)
	}
	if len(methods) != 0 {
		t.Fatalf("expected below-min amount to hide method, got %d", len(methods))
	}
}

func TestPSPAmountPersistsExactDirectAndInverseObservationRates(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	if _, err := walletStore.DB.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url,
		idempotency_header_name, deposit_response_mapping
	) VALUES($1, 'noop-exact-fx', 'Exact FX PSP', 'https://psp.invalid', 'Idempotency-Key', '{}')`, tenantID); err != nil {
		t.Fatalf("seed PSP config: %v", err)
	}

	usdUnitID := testCurrencyUnitID(t, ctx, walletStore, "USD")
	aedUnitID := testCurrencyUnitID(t, ctx, walletStore, "AED")
	wallet, err := walletStore.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeSystem, OwnerID: "exact-fx",
		Currency: "USD", CurrencyUnitID: usdUnitID, KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}

	var sourceID int64
	if err := walletStore.DB.GetContext(ctx, &sourceID, `INSERT INTO fx_sources(
		code, display_name, provider, purpose, source_url, max_age_seconds, is_enabled
	) VALUES('exact-fx-test', 'Exact FX Test', 'test', 'reference', 'https://rates.invalid', 3600, TRUE)
	RETURNING id`); err != nil {
		t.Fatalf("seed FX source: %v", err)
	}
	var pairID int64
	if err := walletStore.DB.GetContext(ctx, &pairID, `INSERT INTO fx_source_pairs(
		source_id, base_currency_code, quote_currency_code, external_series, is_enabled
	) VALUES($1, 'USD', 'AED', 'USD/AED', TRUE) RETURNING id`, sourceID); err != nil {
		t.Fatalf("seed FX pair: %v", err)
	}
	if _, err := walletStore.DB.ExecContext(ctx, `INSERT INTO fx_source_pair_sides(source_pair_id, side)
		VALUES($1, 'mid')`, pairID); err != nil {
		t.Fatalf("seed FX side: %v", err)
	}

	conversionAt := time.Now().UTC().Truncate(time.Microsecond)
	observationAt := conversionAt.Add(-2 * time.Minute)
	if _, err := walletStore.DB.ExecContext(ctx, `INSERT INTO fx_observations(
		source_id, source_pair_id, external_series,
		base_currency_code, quote_currency_code, base_currency_unit_id, quote_currency_unit_id,
		rate, side, purpose, observation_at, retrieved_at, expires_at,
		raw_payload_sha256, source_revision
	) VALUES($1, $2, 'USD/AED', 'USD', 'AED', $3, $4,
		'1.0000000000000000004'::NUMERIC, 'mid', 'reference', $5, $6, $7,
		'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'excess-scale')`,
		sourceID, pairID, usdUnitID, aedUnitID, observationAt,
		conversionAt.Add(-time.Minute), observationAt.Add(time.Hour)); err == nil {
		t.Fatal("direct-SQL observation with excess precision was silently rounded")
	}
	observation, err := walletStore.CreateFXObservation(ctx, FXObservationInput{
		SourceID: sourceID, SourcePairID: pairID, ExternalSeries: "USD/AED",
		BaseCurrencyCode: "USD", QuoteCurrencyCode: "AED",
		BaseCurrencyUnitID: usdUnitID, QuoteCurrencyUnitID: aedUnitID,
		Rate: decimal.RequireFromString("3.75"), Side: FXSideMid, Purpose: FXPurposeReference,
		ObservationAt: observationAt, RetrievedAt: conversionAt.Add(-time.Minute),
		ExpiresAt:        observationAt.Add(time.Hour),
		RawPayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceRevision:   "exact-fx-test-v1",
	})
	if err != nil {
		t.Fatalf("create FX observation: %v", err)
	}
	// A conversion cannot precede the observation's database visibility even
	// when its publisher and retrieval timestamps are older.
	conversionAt = observation.CreatedAt

	txn, err := walletStore.CreatePSPTransaction(ctx, PSPTransaction{
		TenantID: tenantID, PSPProvider: "noop-exact-fx", IdempotencyKey: "exact-fx-txn",
		ClientReference: "exact-fx-txn", Direction: "outbound",
		WalletID:            uuid.NullUUID{UUID: wallet.ID, Valid: true},
		OwnerType:           sql.NullString{String: wallet.OwnerType, Valid: true},
		OwnerID:             sql.NullString{String: wallet.OwnerID, Valid: true},
		AllowReturnToSource: sql.NullBool{Bool: true, Valid: true},
		Amount:              100, Currency: "USD", CurrencyUnitID: usdUnitID, Status: "success",
	})
	if err != nil {
		t.Fatalf("create PSP transaction: %v", err)
	}
	if _, err := walletStore.DB.ExecContext(ctx, `UPDATE psp_transactions SET amount = amount + 1
		WHERE tenant_id = $1 AND id = $2`, tenantID, txn.ID); err == nil {
		t.Fatal("direct SQL mutated an immutable PSP transaction amount")
	}
	if _, err := walletStore.DB.ExecContext(ctx, `UPDATE psp_transactions SET fee_amount = 1
		WHERE tenant_id = $1 AND id = $2`, tenantID, txn.ID); err == nil {
		t.Fatal("direct SQL mutated an immutable PSP transaction fee")
	}
	if _, err := walletStore.DB.ExecContext(ctx, `INSERT INTO psp_transactions(
		tenant_id, psp_provider, idempotency_key, client_reference, direction,
		wallet_id, owner_type, owner_id, allow_return_to_source,
		amount, currency, currency_unit_version_id, status
	) VALUES($1, 'noop-exact-fx', 'negative-direct-sql', 'negative-direct-sql', 'outbound',
		$2, $3, $4, TRUE, -1, 'USD', $5, 'initiated')`,
		tenantID, wallet.ID, wallet.OwnerType, wallet.OwnerID, usdUnitID); err == nil {
		t.Fatal("direct SQL persisted a negative PSP transaction amount")
	}

	base := PSPTransactionAmount{
		TenantID: tenantID, PSPTransactionID: txn.ID,
		Amount: 100, FxSource: sql.NullString{String: "exact-fx-test", Valid: true},
		FxObservationID:                  sql.NullInt64{Int64: observation.ID, Valid: true},
		FxConversionAt:                   sql.NullTime{Time: conversionAt, Valid: true},
		FxObservationBaseCurrency:        sql.NullString{String: "USD", Valid: true},
		FxObservationQuoteCurrency:       sql.NullString{String: "AED", Valid: true},
		FxObservationBaseCurrencyUnitID:  sql.NullInt64{Int64: usdUnitID, Valid: true},
		FxObservationQuoteCurrencyUnitID: sql.NullInt64{Int64: aedUnitID, Valid: true},
	}
	direct := base
	direct.AmountKind = PSPAmountReported
	direct.Currency = "USD"
	direct.CurrencyUnitID = usdUnitID
	direct.FxRate = decimal.NullDecimal{Decimal: decimal.RequireFromString("3.75000000"), Valid: true}
	direct.FxRateNumerator = decimal.NullDecimal{Decimal: decimal.NewFromInt(15), Valid: true}
	direct.FxRateDenominator = decimal.NullDecimal{Decimal: decimal.NewFromInt(4), Valid: true}
	direct.FxBaseCurrency = sql.NullString{String: "USD", Valid: true}
	direct.FxQuoteCurrency = sql.NullString{String: "AED", Valid: true}
	direct.FxBaseCurrencyUnitID = sql.NullInt64{Int64: usdUnitID, Valid: true}
	direct.FxQuoteCurrencyUnitID = sql.NullInt64{Int64: aedUnitID, Valid: true}
	lateConversionAt := observation.CreatedAt.Add(-time.Microsecond)
	lateDirect := direct
	lateDirect.AmountKind = PSPAmountWalletCredit
	lateDirect.FxConversionAt = sql.NullTime{Time: lateConversionAt, Valid: true}
	if _, err := walletStore.AddPSPTransactionAmount(ctx, lateDirect); !errors.Is(err, ErrPSPFXProvenanceMismatch) {
		t.Fatalf("late-inserted observation Go provenance error = %v, want %v", err, ErrPSPFXProvenanceMismatch)
	}
	if _, err := walletStore.DB.ExecContext(ctx, `INSERT INTO psp_transaction_amounts(
		tenant_id, psp_transaction_id, amount_kind, amount, currency, currency_unit_version_id,
		fx_rate, fx_rate_numerator, fx_rate_denominator,
		fx_base_currency, fx_quote_currency,
		fx_base_currency_unit_version_id, fx_quote_currency_unit_version_id,
		fx_source, fx_observation_id, fx_conversion_at,
		fx_observation_base_currency, fx_observation_quote_currency,
		fx_observation_base_currency_unit_version_id,
		fx_observation_quote_currency_unit_version_id
	) VALUES($1, $2, 'wallet_credit', 100, 'USD', $3,
		3.75000000, 15, 4, 'USD', 'AED', $3, $4,
		'exact-fx-test', $5, $6, 'USD', 'AED', $3, $4)`,
		tenantID, txn.ID, usdUnitID, aedUnitID, observation.ID, lateConversionAt,
	); err == nil {
		t.Fatal("database accepted PSP FX provenance from an observation inserted after conversion time")
	}
	if _, err := walletStore.AddPSPTransactionAmount(ctx, direct); err != nil {
		t.Fatalf("persist direct exact observation rate: %v", err)
	}

	inverse := base
	inverse.AmountKind = PSPAmountSettlement
	inverse.Currency = "USD"
	inverse.CurrencyUnitID = usdUnitID
	inverse.FxRate = decimal.NullDecimal{Decimal: decimal.RequireFromString("0.26666667"), Valid: true}
	inverse.FxRateNumerator = decimal.NullDecimal{Decimal: decimal.NewFromInt(4), Valid: true}
	inverse.FxRateDenominator = decimal.NullDecimal{Decimal: decimal.NewFromInt(15), Valid: true}
	inverse.FxBaseCurrency = sql.NullString{String: "AED", Valid: true}
	inverse.FxQuoteCurrency = sql.NullString{String: "USD", Valid: true}
	inverse.FxBaseCurrencyUnitID = sql.NullInt64{Int64: aedUnitID, Valid: true}
	inverse.FxQuoteCurrencyUnitID = sql.NullInt64{Int64: usdUnitID, Valid: true}
	if _, err := walletStore.AddPSPTransactionAmount(ctx, inverse); err != nil {
		t.Fatalf("persist inverse exact observation rate: %v", err)
	}
	quote, err := walletStore.CreateMoneyConversionQuote(ctx, MoneyConversionQuoteInput{
		TenantID: tenantID, RequestedByUserID: 123, IdempotencyKey: "psp-inverse-quote",
		MaxQuotesPerObservation:       10,
		ObservationID:                 observation.ID,
		ObservationBaseCurrencyUnitID: usdUnitID, ObservationQuoteCurrencyUnitID: aedUnitID,
		ObservationBaseCurrencyCode: "USD", ObservationQuoteCurrencyCode: "AED",
		ObservationExpiresAt: observation.ExpiresAt,
		InputCurrencyUnitID:  aedUnitID, OutputCurrencyUnitID: usdUnitID,
		InputCurrencyCode: "AED", OutputCurrencyCode: "USD",
		InputMinorUnits: 375, OutputMinorUnits: 100, Inverse: true,
		RoundingMode: "half_even", ConversionAt: conversionAt, ExpiresAt: observation.ExpiresAt,
	})
	if err != nil {
		t.Fatalf("create inverse audit quote: %v", err)
	}
	quotedInverse := inverse
	quotedInverse.AmountKind = PSPAmountWalletDebit
	quotedInverse.FxQuoteID = uuid.NullUUID{UUID: quote.ID, Valid: true}
	if _, err := walletStore.AddPSPTransactionAmount(ctx, quotedInverse); err != nil {
		t.Fatalf("persist quote-bound inverse exact rate: %v", err)
	}
	wrongQuotedAmount := quotedInverse
	wrongQuotedAmount.AmountKind = PSPAmountWalletCredit
	wrongQuotedAmount.Amount++
	if _, err := walletStore.AddPSPTransactionAmount(ctx, wrongQuotedAmount); !errors.Is(err, ErrPSPFXProvenanceMismatch) {
		t.Fatalf("quote-bound wrong amount error = %v, want %v", err, ErrPSPFXProvenanceMismatch)
	}

	insertProviderRate := func(insertCtx context.Context, kind PSPAmountKind, rate, numerator, denominator string) error {
		_, insertErr := walletStore.DB.ExecContext(insertCtx, `INSERT INTO psp_transaction_amounts(
			tenant_id, psp_transaction_id, amount_kind, amount, currency, currency_unit_version_id,
			fx_rate, fx_rate_numerator, fx_rate_denominator,
			fx_base_currency, fx_quote_currency,
			fx_base_currency_unit_version_id, fx_quote_currency_unit_version_id,
			fx_source, fx_conversion_at
		) VALUES($1, $2, $3, 100, 'USD', $4,
			$5::NUMERIC, $6::NUMERIC, $7::NUMERIC,
			'USD', 'AED', $4, $8, 'provider-direct', $9)`,
			tenantID, txn.ID, kind, usdUnitID, rate, numerator, denominator, aedUnitID, conversionAt)
		return insertErr
	}

	for _, valid := range []struct {
		name                         string
		kind                         PSPAmountKind
		rate, numerator, denominator string
	}{
		{
			name: "exact half-away tie", kind: PSPAmountFee, rate: "1.23456790",
			numerator: "246913579", denominator: "200000000",
		},
		{
			name: "near tie does not double round", kind: PSPAmountNet, rate: "0.12345678",
			numerator:   "12345678499999999999999999999999999999",
			denominator: "99999999999999999999999999999999999999",
		},
		{
			name: "38 digit arithmetic", kind: PSPAmountOverpayment, rate: "1.00000000",
			numerator:   "99999999999999999999999999999999999999",
			denominator: "99999999999999999999999999999999999998",
		},
	} {
		t.Run(valid.name, func(t *testing.T) {
			if err := insertProviderRate(ctx, valid.kind, valid.rate, valid.numerator, valid.denominator); err != nil {
				t.Fatalf("insert valid direct-provider exact rate: %v", err)
			}
		})
	}

	for _, invalid := range []struct {
		name, rate, numerator, denominator string
	}{
		{name: "unreduced", rate: "3.75000000", numerator: "30", denominator: "8"},
		{name: "projection mismatch", rate: "3.74999999", numerator: "15", denominator: "4"},
		{name: "near tie rounded upward", rate: "0.12345679", numerator: "12345678499999999999999999999999999999", denominator: "99999999999999999999999999999999999999"},
		{name: "fractional numerator", rate: "1.00000000", numerator: "1.4", denominator: "1"},
		{name: "zero denominator", rate: "1.00000000", numerator: "1", denominator: "0"},
		{name: "negative numerator", rate: "1.00000000", numerator: "-1", denominator: "1"},
		{name: "numerator out of range", rate: "1.00000000", numerator: "100000000000000000000000000000000000000", denominator: "99999999999999999999999999999999999999"},
		{name: "denominator out of range", rate: "1.00000000", numerator: "99999999999999999999999999999999999999", denominator: "100000000000000000000000000000000000000"},
		{name: "not finite", rate: "NaN", numerator: "NaN", denominator: "NaN"},
	} {
		t.Run("reject "+invalid.name, func(t *testing.T) {
			insertCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := insertProviderRate(insertCtx, PSPAmountUnderpayment, invalid.rate, invalid.numerator, invalid.denominator); err == nil {
				t.Fatal("invalid direct-provider exact rate persisted")
			}
			if errors.Is(insertCtx.Err(), context.DeadlineExceeded) {
				t.Fatal("exact-rate validation did not terminate before its statement timeout")
			}
		})
	}
	var rejectedRows int
	if err := walletStore.DB.GetContext(ctx, &rejectedRows, `SELECT count(*) FROM psp_transaction_amounts
		WHERE tenant_id = $1 AND psp_transaction_id = $2 AND amount_kind = 'underpayment'`, tenantID, txn.ID); err != nil {
		t.Fatalf("count rejected exact rates: %v", err)
	}
	if rejectedRows != 0 {
		t.Fatalf("rejected exact rate rows = %d, want 0", rejectedRows)
	}
}

func TestHeldPSPTransactionIsPollable(t *testing.T) {
	ctx, store, tenantID := newWalletStoreIntegration(t)
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, idempotency_header_name, deposit_response_mapping
	) VALUES($1, 'pollable', 'Pollable PSP', 'https://psp.invalid', 'Idempotency-Key', '{}')`, tenantID); err != nil {
		t.Fatal(err)
	}
	wallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeSystem, OwnerID: "held-polling",
		Currency: "AED", CurrencyUnitID: testCurrencyUnitID(t, ctx, store, "AED"), KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := store.CreatePSPTransaction(ctx, PSPTransaction{
		TenantID: tenantID, PSPProvider: "pollable", IdempotencyKey: "held-polling",
		ClientReference: "held-polling", Direction: "outbound", Amount: 100, Currency: "AED",
		CurrencyUnitID: wallet.CurrencyUnitID,
		Status:         PSPStatusHeld, PSPTransactionID: sql.NullString{String: "provider-held", Valid: true},
		WalletID:            uuid.NullUUID{UUID: wallet.ID, Valid: true},
		OwnerType:           sql.NullString{String: wallet.OwnerType, Valid: true},
		OwnerID:             sql.NullString{String: wallet.OwnerID, Valid: true},
		AllowReturnToSource: sql.NullBool{Bool: true, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	pollable, err := store.ListPSPTransactionsForPolling(ctx, tenantID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pollable) != 1 || pollable[0].ID != transaction.ID {
		t.Fatalf("pollable held transactions = %+v, want id %d", pollable, transaction.ID)
	}

	firstToken := "held-polling-lock-1"
	acquired, err := store.TryAcquirePSPTransactionLock(
		ctx,
		tenantID,
		transaction.ClientReference,
		firstToken,
		time.Now().UTC().Add(time.Minute),
	)
	if err != nil || !acquired {
		t.Fatalf("acquire held transaction: acquired=%v err=%v", acquired, err)
	}
	acquired, err = store.TryAcquirePSPTransactionLock(
		ctx,
		tenantID,
		transaction.ClientReference,
		"held-polling-lock-2",
		time.Now().UTC().Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("compete for held transaction: %v", err)
	}
	if acquired {
		t.Fatal("second held transaction lease acquired while first lease is live")
	}

	polledAt := time.Now().UTC().Truncate(time.Microsecond)
	nextPollAt := polledAt.Add(time.Hour)
	if err := store.UpdatePSPTransactionStatus(ctx, tenantID, transaction.ClientReference, PSPStatusUpdate{
		Status:       PSPStatusPending,
		LastPolledAt: sql.NullTime{Time: polledAt, Valid: true},
		NextPollAt:   sql.NullTime{Time: nextPollAt, Valid: true},
		RetryCount:   1,
		LockToken:    sql.NullString{String: firstToken, Valid: true},
	}); err != nil {
		t.Fatalf("update acquired held transaction: %v", err)
	}
	updated, err := store.GetPSPTransactionByReference(ctx, tenantID, transaction.ClientReference)
	if err != nil {
		t.Fatalf("reload acquired held transaction: %v", err)
	}
	if updated.Status != PSPStatusPending || updated.RetryCount != 1 ||
		!updated.LastPolledAt.Valid || !updated.LastPolledAt.Time.Equal(polledAt) ||
		!updated.NextPollAt.Valid || !updated.NextPollAt.Time.Equal(nextPollAt) {
		t.Fatalf("updated held transaction = %+v", updated)
	}

	if _, err := store.DB.ExecContext(ctx, `UPDATE psp_transactions
		SET lock_token = NULL, lock_expires_at = NULL
		WHERE tenant_id = $1 AND id = $2`, tenantID, transaction.ID); err != nil {
		t.Fatalf("release held test lease: %v", err)
	}
	pollable, err = store.ListPSPTransactionsForPolling(ctx, tenantID, 10)
	if err != nil {
		t.Fatalf("list held transaction before next poll: %v", err)
	}
	if len(pollable) != 0 {
		t.Fatalf("future held transaction is pollable: %+v", pollable)
	}
	acquired, err = store.TryAcquirePSPTransactionLock(
		ctx,
		tenantID,
		transaction.ClientReference,
		"held-polling-lock-2",
		time.Now().UTC().Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("try acquiring future held transaction: %v", err)
	}
	if acquired {
		t.Fatal("future held transaction lease acquired before next_poll_at")
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

	const dbName = "wallet_ledger"
	dbURL, err := container.CreateDatabaseForRole(ctx, dbName, "wallet_ledger_migrate")
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

	if err := basestore.MigrateScope(ctx, db, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	provisionWalletStoreTestTenant(t, ctx, db, "tenant", "PSP Method Tenant")

	if _, err := db.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, idempotency_header_name, enabled_currencies,
		is_active, supports_deposit, supports_withdrawal, method_type, display_name,
		supported_regions, deposit_input_schema, presentation_schema,
		deposit_response_mapping
	) VALUES
		('tenant', 'alpha', 'Alpha Pay', 'https://alpha.example', 'Idempotency-Key',
		 ARRAY['USD'], FALSE, FALSE, FALSE, 'redirect', 'Alpha Pay',
		 ARRAY['US'], '{"kind":"redirect"}', '{"kind":"redirect"}', '{}'),
		('tenant', 'beta', 'Beta Pay', 'https://beta.example', 'Idempotency-Key',
		 ARRAY['AED'], TRUE, FALSE, TRUE, 'bank_transfer', 'Beta Pay',
		 ARRAY['AE'], '{"kind":"bank"}', '{"kind":"bank"}', '{}'),
		('tenant', 'gamma', 'Gamma Pay', 'https://gamma.example', 'Idempotency-Key',
		 ARRAY['USD'], TRUE, TRUE, FALSE, 'card', 'Gamma Pay',
		 ARRAY['AE'], '{"kind":"card"}', '{"kind":"card"}',
		 '{"client_reference":["client_reference"],"transaction_id":["transaction_id"],"status":["status"],"amount":["amount"],"currency":["currency"]}'),
		('tenant', 'zeta', 'Zeta Pay', 'https://zeta.example', 'Idempotency-Key',
		 ARRAY['AED'], TRUE, TRUE, FALSE, 'qr', 'Zeta Pay',
		 ARRAY['AE'], '{"kind":"qr"}', '{"kind":"qr"}',
		 '{"client_reference":["client_reference"],"transaction_id":["transaction_id"],"status":["status"],"amount":["amount"],"currency":["currency"]}')`); err != nil {
		t.Fatalf("insert psp configs: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO psp_config_overrides(
		tenant_id, provider_code, region, currency, direction, is_active,
		supports_deposit, supports_withdrawal, enabled_currencies, method_type,
		display_name, supported_regions, deposit_input_schema,
		presentation_schema
	) VALUES(
		'tenant', 'alpha', 'AE', 'AED', 'deposit', TRUE,
		TRUE, FALSE, ARRAY['AED'], 'qr', 'Alpha AE QR',
		ARRAY['AE'], '{"kind":"qr"}', '{"kind":"qr"}'
	)`); err != nil {
		t.Fatalf("insert psp override: %v", err)
	}

	s := New(db)
	aedUnitID := testCurrencyUnitID(t, ctx, s, "AED")
	if _, err := db.ExecContext(ctx, `INSERT INTO psp_amount_policies(
		tenant_id, provider_code, currency, currency_unit_version_id,
		direction, region, min_amount, max_amount
	) VALUES
		('tenant', 'alpha', 'AED', $1, 'deposit', 'AE', 100, 100000),
		('tenant', 'zeta', 'AED', $1, 'deposit', '', 100, 100000)`, aedUnitID); err != nil {
		t.Fatalf("insert PSP amount policies: %v", err)
	}
	methods, err := s.ListAvailablePSPMethods(ctx, PSPMethodFilter{
		TenantID:       "tenant",
		Direction:      "deposit",
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		Region:         "AE",
		Amount:         500,
		Limit:          1,
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
		TenantID:       "tenant",
		Direction:      "deposit",
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		Region:         "AE",
		Amount:         500,
		Limit:          1,
		Offset:         1,
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
