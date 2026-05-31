package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
)

func TestEnsureWalletRejectsMismatchedReplay(t *testing.T) {
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

	tenantID := "tenant"
	if err := basestore.MigrateScope(ctx, db, tenantID, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	store := New(db)
	params := EnsureWalletParams{
		TenantID:  tenantID,
		OwnerType: OwnerTypeUser,
		OwnerID:   "user-42",
		UserID:    42,
		Currency:  "USD",
		KYCTier:   KYCTierUnverified,
	}
	created, err := store.EnsureWallet(ctx, params)
	if err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}
	replayed, err := store.EnsureWallet(ctx, params)
	if err != nil {
		t.Fatalf("replay wallet: %v", err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("replayed wallet id = %s, want %s", replayed.ID, created.ID)
	}

	userMismatch := params
	userMismatch.UserID = 99
	if _, err := store.EnsureWallet(ctx, userMismatch); !errors.Is(err, ErrDuplicateWallet) {
		t.Fatalf("user mismatch replay error = %v, want %v", err, ErrDuplicateWallet)
	}

	ownerMismatch := params
	ownerMismatch.OwnerID = "user-99"
	if _, err := store.EnsureWallet(ctx, ownerMismatch); !errors.Is(err, ErrDuplicateWallet) {
		t.Fatalf("owner mismatch replay error = %v, want %v", err, ErrDuplicateWallet)
	}

	kycMismatch := params
	kycMismatch.KYCTier = "verified"
	if _, err := store.EnsureWallet(ctx, kycMismatch); !errors.Is(err, ErrDuplicateWallet) {
		t.Fatalf("kyc mismatch replay error = %v, want %v", err, ErrDuplicateWallet)
	}
}
