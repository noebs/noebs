package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestGetTransactionsUsesParticipantProjection(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	service := &Service{
		Store: storeSvc,
		NoebsConfig: ebs_fields.NoebsConfig{
			KafkaTransactionTopic: testKafkaTransactionTopic,
		},
	}
	ctx, err := WithTransactionActor(context.Background(), 42)
	if err != nil {
		t.Fatalf("bind transaction actor: %v", err)
	}
	if err := service.recordTransaction(ctx, tenantID, ebs_fields.EBSResponse{
		UUID:            "tx-1",
		PAN:             "9222081700000000",
		ResponseCode:    0,
		ResponseMessage: "approved",
	}); err != nil {
		t.Fatalf("record transaction: %v", err)
	}
	if err := service.recordTransaction(context.Background(), tenantID, ebs_fields.EBSResponse{
		UUID: "unmarked-transaction", PAN: "9222081700000000",
	}); !errors.Is(err, ErrMissingTransactionOwnership) {
		t.Fatalf("unmarked transaction error = %v, want %v", err, ErrMissingTransactionOwnership)
	}
	var unmarkedWrites int
	if err := db.GetContext(context.Background(), &unmarkedWrites, db.Rebind(`SELECT COUNT(*) FROM transactions WHERE tenant_id = ? AND uuid = ?`), tenantID, "unmarked-transaction"); err != nil {
		t.Fatalf("count unmarked writes: %v", err)
	}
	if unmarkedWrites != 0 {
		t.Fatalf("unmarked transaction writes = %d, want 0", unmarkedWrites)
	}
	if err := service.recordTransaction(noConsumerTransactionContext(), tenantID, ebs_fields.EBSResponse{
		UUID: "legacy-without-owner", PAN: "9222081700000000",
	}); err != nil {
		t.Fatalf("record public transaction without an owner: %v", err)
	}

	transactions, err := service.GetTransactionsForUserID(context.Background(), tenantID, 42)
	if err != nil {
		t.Fatalf("get transactions: %v", err)
	}
	if len(transactions) != 1 || transactions[0].UUID != "tx-1" || transactions[0].PAN != "922208*****0000" {
		t.Fatalf("transactions = %+v", transactions)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM users LIMIT 1"); err == nil {
		t.Fatalf("ebs-adapter scope must not create identity tables")
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil {
		t.Fatalf("ebs-adapter scope must not create card-vault tables")
	}
}

func TestGetTransactionsRejectsMissingUserID(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	service := &Service{Store: storeSvc}

	if _, err := service.GetTransactionsForUserID(context.Background(), tenantID, 0); !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("missing user_id error = %v", err)
	}
}

func TestGetTransactionByUUIDForUserEnforcesParticipantOwnership(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	service := &Service{
		Store: storeSvc,
		NoebsConfig: ebs_fields.NoebsConfig{
			KafkaTransactionTopic: testKafkaTransactionTopic,
		},
	}
	ownedCtx, err := WithTransactionActor(context.Background(), 42)
	if err != nil {
		t.Fatalf("bind owned actor: %v", err)
	}
	otherCtx, err := WithTransactionActor(context.Background(), 84)
	if err != nil {
		t.Fatalf("bind other actor: %v", err)
	}
	if err := service.recordTransaction(ownedCtx, tenantID, ebs_fields.EBSResponse{UUID: "owned-transaction", PAN: "9222081700000000"}); err != nil {
		t.Fatalf("record owned transaction: %v", err)
	}
	if err := service.recordTransaction(otherCtx, tenantID, ebs_fields.EBSResponse{UUID: "other-transaction", PAN: "9222089999900000"}); err != nil {
		t.Fatalf("record other transaction: %v", err)
	}

	owned, err := service.GetTransactionByUUIDForUser(context.Background(), tenantID, 42, "owned-transaction")
	if err != nil {
		t.Fatalf("owned transaction: %v", err)
	}
	if owned.UUID != "owned-transaction" {
		t.Fatalf("owned UUID = %q, want owned-transaction", owned.UUID)
	}

	for _, uuid := range []string{"other-transaction", "missing-transaction"} {
		if _, err := service.GetTransactionByUUIDForUser(context.Background(), tenantID, 42, uuid); !errors.Is(err, ErrTransactionNotFound) {
			t.Fatalf("lookup %s error = %v, want %v", uuid, err, ErrTransactionNotFound)
		}
	}
}

func TestGetTransactionByUUIDForUserRequiresAuthenticatedIdentity(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	ctx := context.Background()
	if _, err := service.GetTransactionByUUIDForUser(ctx, "tenant", 0, "transaction-uuid"); !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("missing user id error = %v, want %v", err, store.ErrInvalidUserID)
	}
	if _, err := service.GetTransactionByUUIDForUser(ctx, "tenant", 42, " "); !errors.Is(err, store.ErrMissingUUID) {
		t.Fatalf("missing uuid error = %v, want %v", err, store.ErrMissingUUID)
	}
}
