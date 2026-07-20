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
	db := newMigrationAuthorityDB(t, MigrationScopeEBSAdapter)
	tenantID := "tenant-events"
	if err := MigrateScope(ctx, db, MigrationScopeEBSAdapter); err != nil {
		t.Fatalf("migrate ebs-adapter: %v", err)
	}
	storeSvc := New(db)
	provisionTestTenant(t, ctx, storeSvc, tenantID, "Event Tenant")

	event := TransactionEventCreate{
		Topic:     "noebs-ebs-transactions-v1",
		EventKey:  tenantID + ":tx-1",
		EventType: "ebs.transaction.recorded.v1",
		Payload:   []byte(`{"type":"ebs.transaction.recorded.v1"}`),
	}
	participants := []TransactionParticipant{
		{UserID: 42, Role: TransactionParticipantActor},
		{UserID: 84, Role: TransactionParticipantRecipient},
	}
	if err := storeSvc.CreateTransactionWithEvent(ctx, tenantID, ebs_fields.EBSResponse{UUID: "tx-1", PAN: "9222081700009999"}, event, participants); err != nil {
		t.Fatalf("create transaction with event: %v", err)
	}
	if err := storeSvc.CreateTransactionWithEvent(ctx, tenantID, ebs_fields.EBSResponse{UUID: "tx-1", PAN: "9222081700009999"}, event, []TransactionParticipant{participants[1], participants[0]}); err != nil {
		t.Fatalf("replay transaction with event: %v", err)
	}
	if err := storeSvc.CreateTransactionWithEvent(ctx, tenantID, ebs_fields.EBSResponse{UUID: "tx-1", PAN: "9222081700009999"}, event, participants[:1]); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("mismatched participant replay error = %v, want %v", err, ErrDuplicateTransaction)
	}
	if err := storeSvc.CreateTransactionWithEvent(ctx, tenantID, ebs_fields.EBSResponse{UUID: "tx-1", TerminalID: "terminal-mismatch"}, event, participants); !errors.Is(err, ErrDuplicateTransaction) {
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
	var storedParticipantCount int
	if err := db.DB.GetContext(ctx, &storedParticipantCount, db.DB.Rebind(`SELECT COUNT(*) FROM transaction_participants WHERE tenant_id = ?`), tenantID); err != nil {
		t.Fatalf("count participants: %v", err)
	}
	if storedParticipantCount != 2 {
		t.Fatalf("participants = %d, want 2", storedParticipantCount)
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
	if err := storeSvc.CreateTransactionWithEvent(context.Background(), "", ebs_fields.EBSResponse{}, event, nil); !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("missing tenant error = %v, want %v", err, ErrMissingTenantID)
	}
	if err := storeSvc.CreateTransactionWithEvent(context.Background(), "tenant-a", ebs_fields.EBSResponse{}, event, nil); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("missing transaction uuid error = %v, want %v", err, ErrMissingUUID)
	}
	if err := storeSvc.CreateTransactionWithEvent(context.Background(), "tenant-a", ebs_fields.EBSResponse{UUID: "tx-1"}, TransactionEventCreate{}, nil); !errors.Is(err, ErrMissingEventTopic) {
		t.Fatalf("missing topic error = %v, want %v", err, ErrMissingEventTopic)
	}
	if err := storeSvc.CreateTransactionWithEvent(context.Background(), "tenant-a", ebs_fields.EBSResponse{UUID: "tx-1"}, event, []TransactionParticipant{{UserID: 0, Role: TransactionParticipantActor}}); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("invalid participant error = %v, want %v", err, ErrInvalidUserID)
	}
	if err := storeSvc.CreateTransactionWithEvent(context.Background(), "tenant-a", ebs_fields.EBSResponse{UUID: "tx-1"}, event, []TransactionParticipant{{UserID: 42, Role: TransactionParticipantActor}, {UserID: 42, Role: TransactionParticipantActor}}); !errors.Is(err, ErrDuplicateParticipant) {
		t.Fatalf("duplicate participant error = %v, want %v", err, ErrDuplicateParticipant)
	}
	if err := storeSvc.CreateTransactionWithEvent(context.Background(), "tenant-a", ebs_fields.EBSResponse{UUID: "tx-1"}, event, []TransactionParticipant{{UserID: 42, Role: "viewer"}}); !errors.Is(err, ErrInvalidParticipantRole) {
		t.Fatalf("invalid participant role error = %v, want %v", err, ErrInvalidParticipantRole)
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
	db := newMigrationAuthorityDB(t, MigrationScopeAdminReporting)
	tenantID := "tenant-projection"
	if err := MigrateScope(ctx, db, MigrationScopeAdminReporting); err != nil {
		t.Fatalf("migrate admin-reporting: %v", err)
	}
	storeSvc := New(db)
	provisionTestTenant(t, ctx, storeSvc, tenantID, "Projection Tenant")

	first := ebs_fields.EBSResponse{UUID: "projection-1", TerminalID: "terminal-a", PAN: "9222081700009999"}
	if err := storeSvc.UpsertTransactionProjection(ctx, tenantID, first); err != nil {
		t.Fatalf("upsert projection: %v", err)
	}
	if err := storeSvc.UpsertTransactionProjection(ctx, tenantID, first); err != nil {
		t.Fatalf("exact projection replay: %v", err)
	}
	if err := storeSvc.UpsertTransactionProjection(ctx, tenantID, ebs_fields.EBSResponse{UUID: "projection-1", TerminalID: "terminal-b"}); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("mismatched projection replay error = %v, want %v", err, ErrDuplicateTransaction)
	}
	got, err := storeSvc.GetTransactionByUUID(ctx, tenantID, "projection-1")
	if err != nil {
		t.Fatalf("get projection: %v", err)
	}
	if got.TerminalID != "terminal-a" {
		t.Fatalf("terminal = %q, want terminal-a", got.TerminalID)
	}
}

const eventsIDNotFound = 9223372036854775807
