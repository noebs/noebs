package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/statusevent"
	"github.com/google/uuid"
)

func TestWalletStatusRecoveryReplaysStableHistoryAndInvalidatesOldLeases(t *testing.T) {
	ctx, s, tenant := newWalletStoreIntegration(t)
	transaction := lifecycleTestTransaction(t, ctx, s, tenant, "recovery")
	command := lifecycleTestCommand(transaction, insertWalletOperator(t, ctx, s, "recovery-operator"))
	command.Status = PSPStatusFailed
	command.SettlementReference = ""
	if _, err := s.ResolvePSPTransaction(ctx, command); err != nil {
		t.Fatal(err)
	}
	publisher, err := NewStatusEventOutbox(s, "status", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first, err := publisher.ClaimPendingTransactionEvents(ctx, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	if err := publisher.MarkTransactionEventPublished(ctx, first[0].ID); err != nil {
		t.Fatal(err)
	}
	second, err := publisher.ClaimPendingTransactionEvents(ctx, 10)
	if err != nil || len(second) != 1 {
		t.Fatalf("second=%+v error=%v", second, err)
	}
	recovery, err := os.ReadFile("../../infra/scripts/recovery/wallet_ledger.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, string(recovery)); err != nil {
		t.Fatal(err)
	}
	if err := publisher.MarkTransactionEventPublished(ctx, second[0].ID); !errors.Is(err, ErrStatusEventLeaseLost) {
		t.Fatalf("old publisher could acknowledge recovered event: %v", err)
	}
	for _, original := range append(first, second...) {
		replay, err := publisher.ClaimPendingTransactionEvents(ctx, 10)
		if err != nil || len(replay) != 1 || replay[0].ID != original.ID || !bytes.Equal(replay[0].Payload, original.Payload) {
			t.Fatalf("recovered event=%+v error=%v", replay, err)
		}
		if err := publisher.MarkTransactionEventPublished(ctx, replay[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	assertLifecycleHistory(t, ctx, s, tenant, transaction.ClientReference, 2, "failed", "offline")
}

func TestManualResolutionValidatesBeforeDatabaseAccess(t *testing.T) {
	valid := PSPManualResolution{TenantID: "tenant", ClientReference: "ref", IdempotencyKey: "command", OperatorID: 1, ExpectedVersion: 1, Status: PSPStatusSuccess, FulfillmentMethod: FulfillmentOffline, Reason: "Bank confirmed transfer", EvidenceReference: "evidence/receipt", SettlementReference: "bank-123"}
	tests := []struct {
		name   string
		mutate func(*PSPManualResolution)
		want   error
	}{
		{"tenant", func(c *PSPManualResolution) { c.TenantID = "" }, ErrMissingTenantID},
		{"reference", func(c *PSPManualResolution) { c.ClientReference = "" }, ErrMissingClientReference},
		{"idempotency", func(c *PSPManualResolution) { c.IdempotencyKey = "" }, ErrMissingIdempotencyKey},
		{"operator", func(c *PSPManualResolution) { c.OperatorID = 0 }, ErrMissingOperatorID},
		{"version", func(c *PSPManualResolution) { c.ExpectedVersion = 0 }, ErrMissingStatusVersion},
		{"status", func(c *PSPManualResolution) { c.Status = "pending" }, ErrInvalidStatus},
		{"method", func(c *PSPManualResolution) { c.FulfillmentMethod = "" }, ErrInvalidFulfillmentMethod},
		{"reason", func(c *PSPManualResolution) { c.Reason = " " }, ErrMissingReason},
		{"evidence", func(c *PSPManualResolution) { c.EvidenceReference = "" }, ErrMissingEvidenceReference},
		{"settlement", func(c *PSPManualResolution) { c.SettlementReference = "" }, ErrMissingSettlementReference},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := valid
			tt.mutate(&command)
			original := command
			_, err := (&Store{}).ResolvePSPTransaction(context.Background(), command)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if command != original {
				t.Fatal("validation mutated command")
			}
		})
	}
	for _, status := range []string{PSPStatusInitiated, PSPStatusHeld, PSPStatusSuccess, PSPStatusFailed, PSPStatusCancelled} {
		if CanResolvePSPTransaction(&PSPTransaction{Status: status, WorkflowID: sql.NullString{String: "workflow", Valid: true}}) {
			t.Fatalf("manual resolution allowed for %s", status)
		}
	}
}

func TestTransactionLifecycleManualResolutionAndSettlement(t *testing.T) {
	ctx, s, tenantID := newWalletStoreIntegration(t)
	transaction := lifecycleTestTransaction(t, ctx, s, tenantID, "offline")
	operator := insertWalletOperator(t, ctx, s, "resolver")
	command := lifecycleTestCommand(transaction, operator)
	stale := command
	stale.ExpectedVersion++
	if _, err := s.ResolvePSPTransaction(ctx, stale); !errors.Is(err, ErrStatusVersionConflict) {
		t.Fatalf("stale resolution = %v", err)
	}
	resolved, err := s.ResolvePSPTransaction(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.LifecycleStatus != "processing" || resolved.Substatus != "settlement_pending" || resolved.StatusVersion != 2 || resolved.SettledAt.Valid {
		t.Fatalf("resolved lifecycle = %+v", resolved)
	}
	signal, err := ParsePSPWorkflowSignal(resolved.WorkflowSignalPayload)
	if err != nil {
		t.Fatal(err)
	}
	if signal.ProviderTxID != command.SettlementReference || signal.Amount != transaction.Amount || signal.Currency != transaction.Currency {
		t.Fatalf("workflow signal = %+v", signal)
	}
	replayed, err := s.ResolvePSPTransaction(ctx, command)
	if err != nil || replayed.StatusVersion != resolved.StatusVersion {
		t.Fatalf("replay = %+v, %v", replayed, err)
	}
	changed := command
	changed.EvidenceReference = "evidence/different"
	if _, err := s.ResolvePSPTransaction(ctx, changed); !errors.Is(err, ErrManualResolutionConflict) {
		t.Fatalf("changed evidence = %v", err)
	}
	if _, err := s.ApplyExternalPSPStatus(ctx, tenantID, transaction.ClientReference, PSPStatusUpdate{Status: PSPStatusFailed}, &PSPWorkflowSignal{Status: PSPStatusFailed}); !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("late callback = %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE psp_transactions SET status = 'pending' WHERE client_reference = 'offline'`); err == nil {
		t.Fatal("database allowed terminal regression")
	}

	settlementSQL := s.DB.Rebind(`INSERT INTO ledger_transactions(tenant_id,idempotency_key,currency,currency_unit_version_id,reference_type,reference_id,status)
	 VALUES(?,?,?,?,'withdrawal',?,'completed')`)
	tx, err := s.DB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, settlementSQL, tenantID, "settlement", transaction.Currency, transaction.CurrencyUnitID, transaction.ClientReference); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertLifecycleHistory(t, ctx, s, tenantID, transaction.ClientReference, 2, "processing", "settlement_pending")
	if _, err := s.DB.ExecContext(ctx, settlementSQL, tenantID, "settlement", transaction.Currency, transaction.CurrencyUnitID, transaction.ClientReference); err != nil {
		t.Fatal(err)
	}
	assertLifecycleHistory(t, ctx, s, tenantID, transaction.ClientReference, 3, "completed", "offline")
	completed, err := s.GetPSPTransactionByReference(ctx, tenantID, transaction.ClientReference)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.SettledAt.Valid || completed.LifecycleStatus != "completed" || completed.Substatus != "offline" {
		t.Fatalf("completed = %+v", completed)
	}
	history, err := s.ListPSPTransactionStatusEvents(ctx, tenantID, transaction.ClientReference, 20)
	if err != nil {
		t.Fatal(err)
	}
	if history[0].OperatorID.Int64 != operator || history[0].EvidenceReference.String != command.EvidenceReference || history[0].UserID.Int64 != 42 {
		t.Fatalf("history evidence = %+v", history[0])
	}
	other, err := s.ListPSPTransactionStatusEvents(ctx, "other-tenant", transaction.ClientReference, 20)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-tenant history = %+v, %v", other, err)
	}
}

func TestManualResolutionRollsBackIfEventCannotBeRecorded(t *testing.T) {
	ctx, s, tenantID := newWalletStoreIntegration(t)
	transaction := lifecycleTestTransaction(t, ctx, s, tenantID, "rollback")
	command := lifecycleTestCommand(transaction, insertWalletOperator(t, ctx, s, "resolver"))
	if _, err := s.DB.ExecContext(ctx, `CREATE FUNCTION fail_status_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected event failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER fail_status_event BEFORE INSERT ON transaction_status_events FOR EACH ROW EXECUTE FUNCTION fail_status_event()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolvePSPTransaction(ctx, command); err == nil {
		t.Fatal("expected event failure")
	}
	stored, err := s.GetPSPTransactionByReference(ctx, tenantID, transaction.ClientReference)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != PSPStatusPending || stored.StatusVersion != 1 || len(stored.WorkflowSignalPayload) != 0 {
		t.Fatalf("partial resolution persisted: %+v", stored)
	}
	var commands int
	if err := s.DB.GetContext(ctx, &commands, `SELECT count(*) FROM psp_manual_resolutions`); err != nil {
		t.Fatal(err)
	}
	if commands != 0 {
		t.Fatal("resolution evidence escaped transaction rollback")
	}
}

func TestConcurrentManualResolutionsChooseOneOutcome(t *testing.T) {
	ctx, s, tenantID := newWalletStoreIntegration(t)
	transaction := lifecycleTestTransaction(t, ctx, s, tenantID, "concurrent")
	command := lifecycleTestCommand(transaction, insertWalletOperator(t, ctx, s, "resolver"))
	conflicting := command
	conflicting.IdempotencyKey = "failed-command"
	conflicting.Status = PSPStatusFailed
	conflicting.SettlementReference = ""
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for _, candidate := range []PSPManualResolution{command, conflicting} {
		wg.Add(1)
		go func(c PSPManualResolution) {
			defer wg.Done()
			_, err := s.ResolvePSPTransaction(ctx, c)
			errorsCh <- err
		}(candidate)
	}
	wg.Wait()
	close(errorsCh)
	succeeded, conflicts := 0, 0
	for err := range errorsCh {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrManualResolutionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", succeeded, conflicts)
	}
	history, err := s.ListPSPTransactionStatusEvents(ctx, tenantID, transaction.ClientReference, 20)
	if err != nil || len(history) != 2 {
		t.Fatalf("history count=%d error=%v", len(history), err)
	}
}

func TestStatusOutboxOrdersAndFencesClaims(t *testing.T) {
	ctx, s, tenantID := newWalletStoreIntegration(t)
	transaction := lifecycleTestTransaction(t, ctx, s, tenantID, "ordered")
	command := lifecycleTestCommand(transaction, insertWalletOperator(t, ctx, s, "resolver"))
	if _, err := s.ResolvePSPTransaction(ctx, command); err != nil {
		t.Fatal(err)
	}
	first, err := NewStatusEventOutbox(s, "noebs.status.changed.v1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStatusEventOutbox(s, "noebs.status.changed.v1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	events, err := first.ClaimPendingTransactionEvents(ctx, 20)
	if err != nil || len(events) != 1 {
		t.Fatalf("first claim = %+v, %v", events, err)
	}
	var payload struct {
		EventID string `json:"event_id"`
		Version int64  `json:"version"`
		UserID  int64  `json:"user_id"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.EventID == "" || payload.Version != 1 || payload.UserID != 42 {
		t.Fatalf("payload = %+v", payload)
	}
	if _, err := statusevent.Parse(events[0].Payload); err != nil {
		t.Fatalf("wallet event violates the notification consumer contract: %v", err)
	}
	blocked, err := second.ClaimPendingTransactionEvents(ctx, 20)
	if err != nil || len(blocked) != 0 {
		t.Fatalf("claim bypassed prior event lease: %+v, %v", blocked, err)
	}
	if _, err := s.DB.ExecContext(ctx, s.DB.Rebind(`UPDATE transaction_status_events SET claimed_until = clock_timestamp() - interval '1 second' WHERE id = ?`), events[0].ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := second.ClaimPendingTransactionEvents(ctx, 20)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != events[0].ID {
		t.Fatalf("reclaimed = %+v, %v", reclaimed, err)
	}
	if err := first.MarkTransactionEventPublished(ctx, events[0].ID); !errors.Is(err, ErrStatusEventLeaseLost) {
		t.Fatalf("stale mark = %v", err)
	}
	if err := second.MarkTransactionEventPublished(ctx, reclaimed[0].ID); err != nil {
		t.Fatal(err)
	}
	next, err := second.ClaimPendingTransactionEvents(ctx, 20)
	if err != nil || len(next) != 1 || next[0].ID == events[0].ID {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if err := second.MarkTransactionEventPublishFailed(ctx, next[0].ID, errors.New("broker unavailable")); err != nil {
		t.Fatal(err)
	}
	retry, err := first.ClaimPendingTransactionEvents(ctx, 20)
	if err != nil || len(retry) != 1 || retry[0].ID != next[0].ID {
		t.Fatalf("retry = %+v, %v", retry, err)
	}
}

func lifecycleTestTransaction(t *testing.T, ctx context.Context, s *Store, tenantID, reference string) *PSPTransaction {
	t.Helper()
	if _, err := s.DB.ExecContext(ctx, s.DB.Rebind(`INSERT INTO psp_configs(tenant_id,provider_code,provider_name,api_base_url,idempotency_header_name,deposit_response_mapping)
	 VALUES(?,'noop','No-op PSP','https://psp.invalid','Idempotency-Key','{}') ON CONFLICT DO NOTHING`), tenantID); err != nil {
		t.Fatal(err)
	}
	wallet, err := s.EnsureWallet(ctx, EnsureWalletParams{TenantID: tenantID, OwnerType: OwnerTypeUser, OwnerID: "42", UserID: 42, Currency: "USD", CurrencyUnitID: testCurrencyUnitID(t, ctx, s, "USD"), KYCTier: KYCTierUnverified})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := s.CreatePSPTransaction(ctx, PSPTransaction{TenantID: tenantID, PSPProvider: "noop", IdempotencyKey: reference, ClientReference: reference,
		Direction: "outbound", WalletID: uuid.NullUUID{UUID: wallet.ID, Valid: true}, OwnerType: sql.NullString{String: wallet.OwnerType, Valid: true}, OwnerID: sql.NullString{String: wallet.OwnerID, Valid: true}, AllowReturnToSource: sql.NullBool{Bool: true, Valid: true},
		Amount: 100, Currency: wallet.Currency, CurrencyUnitID: wallet.CurrencyUnitID, Status: PSPStatusPending, WorkflowID: sql.NullString{String: "workflow-" + reference, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	return transaction
}

func lifecycleTestCommand(transaction *PSPTransaction, operator int64) PSPManualResolution {
	return PSPManualResolution{TenantID: transaction.TenantID, ClientReference: transaction.ClientReference, IdempotencyKey: "resolve-" + transaction.ClientReference, OperatorID: operator, ExpectedVersion: transaction.StatusVersion,
		Status: PSPStatusSuccess, FulfillmentMethod: FulfillmentOffline, Reason: "Bank confirmed fulfillment", EvidenceReference: "receipts/" + transaction.ClientReference, SettlementReference: "bank-" + transaction.ClientReference}
}

func assertLifecycleHistory(t *testing.T, ctx context.Context, s *Store, tenantID, reference string, count int, status, substatus string) {
	t.Helper()
	history, err := s.ListPSPTransactionStatusEvents(ctx, tenantID, reference, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != count || history[0].Status != status || history[0].Substatus != substatus {
		t.Fatalf("history = %+v", history)
	}
}
