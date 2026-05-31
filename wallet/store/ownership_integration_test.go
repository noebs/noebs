package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
)

func TestOwnershipVerificationCreateReplaysAreExact(t *testing.T) {
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

	dbName := fmt.Sprintf("noebs_wallet_ownership_%d", time.Now().UnixNano())
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

	const tenantID = "tenant-ownership"
	if err := basestore.MigrateScope(ctx, db, tenantID, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate wallet ledger: %v", err)
	}
	if err := basestore.New(db).EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}

	store := New(db)
	wallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:  tenantID,
		OwnerType: OwnerTypeUser,
		OwnerID:   "user-1",
		UserID:    1,
		Currency:  "AED",
		KYCTier:   KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}
	destination, err := store.CreateWithdrawalDestination(ctx, WithdrawalDestination{
		TenantID:                    tenantID,
		WalletID:                    wallet.ID,
		DestinationType:             "bank_account",
		DestinationDetails:          []byte(`{"account_last4":"4321"}`),
		Currency:                    "AED",
		OwnershipStatus:             DestinationOwnershipStatusPending,
		OwnershipVerificationMethod: sql.NullString{String: "micro_deposit", Valid: true},
		IsActive:                    true,
	})
	if err != nil {
		t.Fatalf("create withdrawal destination: %v", err)
	}

	request := OwnershipVerification{
		TenantID:         tenantID,
		DestinationID:    destination.ID,
		VerificationType: "micro_deposit",
		Status:           OwnershipVerificationStatusPending,
		MaxAttempts:      3,
		ExpiresAt:        time.Now().UTC().Add(time.Hour),
		WorkflowID:       sql.NullString{String: "workflow-1", Valid: true},
		ReferenceID:      sql.NullString{String: "reference-1", Valid: true},
	}
	created, err := store.CreateOwnershipVerification(ctx, request)
	if err != nil {
		t.Fatalf("create ownership verification: %v", err)
	}
	replayed, err := store.CreateOwnershipVerification(ctx, request)
	if err != nil {
		t.Fatalf("replay ownership verification: %v", err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("replayed verification id = %d, want %d", replayed.ID, created.ID)
	}

	completedAt := time.Now().UTC()
	if err := store.UpdateOwnershipVerificationStatus(ctx, tenantID, created.ID, OwnershipVerificationStatusVerified, completedAt); err != nil {
		t.Fatalf("complete ownership verification: %v", err)
	}
	if err := store.UpdateOwnershipVerificationStatus(ctx, tenantID, created.ID, OwnershipVerificationStatusVerified, completedAt); err != nil {
		t.Fatalf("replay ownership verification completion: %v", err)
	}
	if err := store.UpdateOwnershipVerificationStatus(ctx, tenantID, created.ID, OwnershipVerificationStatusFailed, completedAt.Add(time.Second)); !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("rewrite terminal verification error = %v, want %v", err, ErrInvalidStatusTransition)
	}
	if err := store.UpdateOwnershipVerificationStatus(ctx, tenantID, created.ID, OwnershipVerificationStatusPending, completedAt.Add(time.Second)); !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("reopen terminal verification error = %v, want %v", err, ErrInvalidStatusTransition)
	}
	current, err := store.GetOwnershipVerification(ctx, tenantID, created.ID)
	if err != nil {
		t.Fatalf("get ownership verification: %v", err)
	}
	if current.Status != OwnershipVerificationStatusVerified {
		t.Fatalf("verification status = %q, want %q", current.Status, OwnershipVerificationStatusVerified)
	}

	mismatched := request
	mismatched.MaxAttempts = 5
	if _, err := store.CreateOwnershipVerification(ctx, mismatched); !errors.Is(err, ErrDuplicateVerification) {
		t.Fatalf("mismatched verification replay error = %v, want %v", err, ErrDuplicateVerification)
	}

	referenceMismatch := request
	referenceMismatch.WorkflowID = sql.NullString{String: "workflow-2", Valid: true}
	if _, err := store.CreateOwnershipVerification(ctx, referenceMismatch); !errors.Is(err, ErrDuplicateVerification) {
		t.Fatalf("reference duplicate verification error = %v, want %v", err, ErrDuplicateVerification)
	}
}
