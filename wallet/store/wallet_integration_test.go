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
	ctx, store, tenantID := newWalletStoreIntegration(t)

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

func TestCreateOrResetUserTwoFADoesNotDisableEnabledSecret(t *testing.T) {
	ctx, store, tenantID := newWalletStoreIntegration(t)

	userID := int64(42)
	original, err := store.CreateOrResetUserTwoFA(ctx, tenantID, userID, "secret-1")
	if err != nil {
		t.Fatalf("create 2fa: %v", err)
	}
	if original.Enabled {
		t.Fatal("new 2fa record should start disabled")
	}
	enabledAt := time.Now().UTC()
	if err := store.SetUserTwoFAEnabled(ctx, tenantID, userID, true, enabledAt); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}
	if err := store.TouchUserTwoFALastUsed(ctx, tenantID, userID, enabledAt.Add(time.Second)); err != nil {
		t.Fatalf("touch 2fa last used: %v", err)
	}

	if _, err := store.CreateOrResetUserTwoFA(ctx, tenantID, userID, "secret-2"); !errors.Is(err, ErrUserTwoFAAlreadyEnabled) {
		t.Fatalf("reset enabled 2fa error = %v, want %v", err, ErrUserTwoFAAlreadyEnabled)
	}
	record, err := store.GetUserTwoFA(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("get 2fa after rejected reset: %v", err)
	}
	if !record.Enabled || record.Secret != "secret-1" {
		t.Fatalf("enabled 2fa mutated on rejected reset: enabled=%v secret=%q", record.Enabled, record.Secret)
	}
	if !record.LastUsedAt.Valid {
		t.Fatal("last_used_at should remain after rejected reset")
	}

	disabledAt := enabledAt.Add(2 * time.Second)
	if err := store.SetUserTwoFAEnabled(ctx, tenantID, userID, false, disabledAt); err != nil {
		t.Fatalf("disable 2fa: %v", err)
	}
	reset, err := store.CreateOrResetUserTwoFA(ctx, tenantID, userID, "secret-2")
	if err != nil {
		t.Fatalf("reset disabled 2fa: %v", err)
	}
	if reset.Enabled || reset.Secret != "secret-2" {
		t.Fatalf("disabled reset record = enabled:%v secret:%q", reset.Enabled, reset.Secret)
	}
	if reset.EnabledAt.Valid || reset.DisabledAt.Valid || reset.LastUsedAt.Valid {
		t.Fatalf("reset 2fa should clear enabled/disabled/last-used timestamps: %+v", reset)
	}
}

func newWalletStoreIntegration(t *testing.T) (context.Context, *Store, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	dbName := fmt.Sprintf("noebs_wallet_store_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	db, err := basestore.OpenFromConfig(dbURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	})

	tenantID := "tenant"
	if err := basestore.MigrateScope(ctx, db, tenantID, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return ctx, New(db), tenantID
}
