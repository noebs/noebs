package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestStoreCreateTransactionWithEventOutboxLifecycle(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	tenantID := "tenant-events"
	if err := MigrateScope(ctx, db, tenantID, MigrationScopeEBSAdapter); err != nil {
		t.Fatalf("migrate ebs-adapter: %v", err)
	}
	storeSvc := New(db)
	if err := storeSvc.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}

	event := TransactionEventCreate{
		Topic:     "noebs-ebs-transactions-v1",
		EventKey:  tenantID + ":tx-1",
		EventType: "ebs.transaction.recorded.v1",
		Payload:   []byte(`{"type":"ebs.transaction.recorded.v1"}`),
	}
	if err := storeSvc.CreateTransactionWithEvent(ctx, tenantID, ebs_fields.EBSResponse{UUID: "tx-1", PAN: "9222081700009999"}, event); err != nil {
		t.Fatalf("create transaction with event: %v", err)
	}
	if err := storeSvc.CreateTransactionWithEvent(ctx, tenantID, ebs_fields.EBSResponse{UUID: "tx-1", PAN: "9222081700009999"}, event); err != nil {
		t.Fatalf("replay transaction with event: %v", err)
	}
	if err := storeSvc.CreateTransactionWithEvent(ctx, tenantID, ebs_fields.EBSResponse{UUID: "tx-1", TerminalID: "terminal-mismatch"}, event); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("mismatched transaction replay error = %v, want %v", err, ErrDuplicateTransaction)
	}

	events, err := storeSvc.ClaimPendingTransactionEvents(ctx, 10)
	if err != nil {
		t.Fatalf("claim events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if events[0].Topic != event.Topic || events[0].EventKey != event.EventKey || events[0].EventType != event.EventType {
		t.Fatalf("event = %+v, want topic/key/type from create request", events[0])
	}
	var transactions int
	if err := db.DB.GetContext(ctx, &transactions, db.DB.Rebind(`SELECT COUNT(*) FROM transactions WHERE tenant_id = ? AND uuid = ?`), tenantID, "tx-1"); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if transactions != 1 {
		t.Fatalf("transactions = %d, want 1", transactions)
	}
	if err := storeSvc.MarkTransactionEventPublished(ctx, events[0].ID); err != nil {
		t.Fatalf("mark published: %v", err)
	}
	events, err = storeSvc.ClaimPendingTransactionEvents(ctx, 10)
	if err != nil {
		t.Fatalf("claim after publish: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events after publish = %d, want 0", len(events))
	}
	if err := storeSvc.MarkTransactionEventPublished(ctx, eventsIDNotFound); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing published id error = %v, want %v", err, sql.ErrNoRows)
	}
}

func TestStoreCreateTransactionWithEventRejectsMissingInputs(t *testing.T) {
	storeSvc := &Store{}
	event := TransactionEventCreate{Topic: "topic", EventKey: "key", EventType: "type", Payload: []byte(`{}`)}
	if err := storeSvc.CreateTransactionWithEvent(context.Background(), "", ebs_fields.EBSResponse{}, event); !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("missing tenant error = %v, want %v", err, ErrMissingTenantID)
	}
	if err := storeSvc.CreateTransactionWithEvent(context.Background(), "tenant-a", ebs_fields.EBSResponse{}, event); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("missing transaction uuid error = %v, want %v", err, ErrMissingUUID)
	}
	if err := storeSvc.CreateTransactionWithEvent(context.Background(), "tenant-a", ebs_fields.EBSResponse{UUID: "tx-1"}, TransactionEventCreate{}); !errors.Is(err, ErrMissingEventTopic) {
		t.Fatalf("missing topic error = %v, want %v", err, ErrMissingEventTopic)
	}
	if _, err := storeSvc.ClaimPendingTransactionEvents(context.Background(), 0); !errors.Is(err, ErrMissingData) {
		t.Fatalf("invalid claim limit error = %v, want %v", err, ErrMissingData)
	}
	if err := storeSvc.MarkTransactionEventPublished(context.Background(), 0); !errors.Is(err, ErrMissingEventID) {
		t.Fatalf("missing publish event id error = %v, want %v", err, ErrMissingEventID)
	}
	if err := storeSvc.MarkTransactionEventPublishFailed(context.Background(), 0, errors.New("publish failed")); !errors.Is(err, ErrMissingEventID) {
		t.Fatalf("missing failed event id error = %v, want %v", err, ErrMissingEventID)
	}
}

func TestStoreUpsertTransactionProjection(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	tenantID := "tenant-projection"
	if err := MigrateScope(ctx, db, tenantID, MigrationScopeAdminReporting); err != nil {
		t.Fatalf("migrate admin-reporting: %v", err)
	}
	storeSvc := New(db)
	if err := storeSvc.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}

	if err := storeSvc.UpsertTransactionProjection(ctx, tenantID, ebs_fields.EBSResponse{UUID: "projection-1", TerminalID: "terminal-a", PAN: "9222081700009999"}); err != nil {
		t.Fatalf("upsert projection: %v", err)
	}
	if err := storeSvc.UpsertTransactionProjection(ctx, tenantID, ebs_fields.EBSResponse{UUID: "projection-1", TerminalID: "terminal-b"}); err != nil {
		t.Fatalf("update projection: %v", err)
	}
	got, err := storeSvc.GetTransactionByUUID(ctx, tenantID, "projection-1")
	if err != nil {
		t.Fatalf("get projection: %v", err)
	}
	if got.TerminalID != "terminal-b" {
		t.Fatalf("terminal = %q, want terminal-b", got.TerminalID)
	}
}

const eventsIDNotFound = 9223372036854775807
