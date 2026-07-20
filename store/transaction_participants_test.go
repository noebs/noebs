package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

func TestTransactionParticipantsIsolateMaskedPANCollisionsWithoutCardOwnershipState(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	tenantID := "tenant-participant-isolation"
	if err := MigrateScope(ctx, db, MigrationScopeEBSAdapter); err != nil {
		t.Fatalf("migrate %s: %v", MigrationScopeEBSAdapter, err)
	}
	storeSvc := New(db, WithDataKey("participant-test-data-key"))
	provisionTestTenant(t, ctx, storeSvc, tenantID, "Participant Isolation Tenant")

	const (
		firstUserID  int64 = 101
		secondUserID int64 = 202
		firstPAN           = "9222081700000000"
		secondPAN          = "9222089999900000"
	)
	create := func(uuid, pan string, participants []TransactionParticipant) {
		t.Helper()
		event := TransactionEventCreate{
			Topic:     "transactions",
			EventKey:  tenantID + ":" + uuid,
			EventType: "transaction.recorded",
			Payload:   []byte(`{"uuid":"` + uuid + `"}`),
		}
		if err := storeSvc.CreateTransactionWithEvent(ctx, tenantID, ebs_fields.EBSResponse{UUID: uuid, PAN: pan}, event, participants); err != nil {
			t.Fatalf("create transaction %s: %v", uuid, err)
		}
	}
	create("first-transaction", firstPAN, []TransactionParticipant{{UserID: firstUserID, Role: TransactionParticipantActor}})
	create("second-transaction", secondPAN, []TransactionParticipant{{UserID: secondUserID, Role: TransactionParticipantActor}})
	create("self-transaction", firstPAN, []TransactionParticipant{
		{UserID: 303, Role: TransactionParticipantActor},
		{UserID: 303, Role: TransactionParticipantRecipient},
	})
	if err := storeSvc.CreateTransaction(ctx, tenantID, ebs_fields.EBSResponse{UUID: "legacy-transaction", PAN: firstPAN}); err != nil {
		t.Fatalf("create legacy transaction: %v", err)
	}

	firstStored, err := storeSvc.GetTransactionByUUID(ctx, tenantID, "first-transaction")
	if err != nil {
		t.Fatalf("get first stored transaction: %v", err)
	}
	secondStored, err := storeSvc.GetTransactionByUUID(ctx, tenantID, "second-transaction")
	if err != nil {
		t.Fatalf("get second stored transaction: %v", err)
	}
	if firstStored.PAN != secondStored.PAN {
		t.Fatalf("test PANs did not collide after masking: %q != %q", firstStored.PAN, secondStored.PAN)
	}

	firstHistory, err := storeSvc.GetTransactionsByParticipantUserID(ctx, tenantID, firstUserID)
	if err != nil {
		t.Fatalf("get first history: %v", err)
	}
	if len(firstHistory) != 1 || firstHistory[0].UUID != "first-transaction" {
		t.Fatalf("first history = %+v, want only first-transaction", firstHistory)
	}
	secondHistory, err := storeSvc.GetTransactionsByParticipantUserID(ctx, tenantID, secondUserID)
	if err != nil {
		t.Fatalf("get second history: %v", err)
	}
	if len(secondHistory) != 1 || secondHistory[0].UUID != "second-transaction" {
		t.Fatalf("second history = %+v, want only second-transaction", secondHistory)
	}
	selfHistory, err := storeSvc.GetTransactionsByParticipantUserID(ctx, tenantID, 303)
	if err != nil {
		t.Fatalf("get self-transfer history: %v", err)
	}
	if len(selfHistory) != 1 || selfHistory[0].UUID != "self-transaction" {
		t.Fatalf("self-transfer history = %+v, want one self-transaction", selfHistory)
	}
	var selfRoles int
	if err := db.DB.GetContext(ctx, &selfRoles, db.DB.Rebind(`SELECT COUNT(*)
		FROM transaction_participants participants
		JOIN transactions ON transactions.id = participants.transaction_id
		WHERE participants.tenant_id = ? AND transactions.uuid = ? AND participants.user_id = ?`), tenantID, "self-transaction", 303); err != nil {
		t.Fatalf("count self-transfer roles: %v", err)
	}
	if selfRoles != 2 {
		t.Fatalf("self-transfer roles = %d, want 2", selfRoles)
	}
	var firstTransactionID int64
	if err := db.DB.GetContext(ctx, &firstTransactionID, db.DB.Rebind(`SELECT id FROM transactions WHERE tenant_id = ? AND uuid = ?`), tenantID, "first-transaction"); err != nil {
		t.Fatalf("get first transaction id: %v", err)
	}
	crossTenantStmt := db.DB.Rebind(`INSERT INTO transaction_participants(transaction_id, tenant_id, user_id, role, created_at)
		VALUES(?, ?, ?, ?, ?)`)
	if _, err := db.DB.ExecContext(ctx, crossTenantStmt, firstTransactionID, "other-tenant", 404, TransactionParticipantActor, time.Now().UTC()); err == nil {
		t.Fatal("cross-tenant transaction participant insert succeeded")
	}

	if _, err := storeSvc.GetTransactionByUUIDForParticipantUserID(ctx, tenantID, firstUserID, "second-transaction"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign detail error = %v, want %v", err, sql.ErrNoRows)
	}
	if _, err := storeSvc.GetTransactionByUUIDForParticipantUserID(ctx, tenantID, firstUserID, "legacy-transaction"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy detail error = %v, want %v", err, sql.ErrNoRows)
	}

	firstHistory, err = storeSvc.GetTransactionsByParticipantUserID(ctx, tenantID, firstUserID)
	if err != nil {
		t.Fatalf("get first history without card ownership state: %v", err)
	}
	if len(firstHistory) != 1 || firstHistory[0].UUID != "first-transaction" {
		t.Fatalf("history without card ownership state = %+v, want first-transaction", firstHistory)
	}
}
