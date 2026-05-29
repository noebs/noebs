package activity

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/pquerna/otp/totp"
)

func TestVerifyUserTOTPUsesWalletOwnedTwoFA(t *testing.T) {
	storeSvc, tenantID := newWalletActivityStore(t)
	userID := int64(42)
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "noebs",
		AccountName: "user-42",
	})
	if err != nil {
		t.Fatalf("generate totp secret: %v", err)
	}
	if _, err := storeSvc.CreateOrResetUserTwoFA(t.Context(), tenantID, userID, key.Secret()); err != nil {
		t.Fatalf("create 2fa: %v", err)
	}
	if err := storeSvc.SetUserTwoFAEnabled(t.Context(), tenantID, userID, true, time.Now().UTC()); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}
	code, err := totp.GenerateCode(key.Secret(), time.Now().UTC())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}

	ok, err := NewSecurityActivities(storeSvc).VerifyUserTOTP(t.Context(), tenantID, userID, code)
	if err != nil {
		t.Fatalf("verify totp: %v", err)
	}
	if !ok {
		t.Fatal("totp verification returned false")
	}
	record, err := storeSvc.GetUserTwoFA(t.Context(), tenantID, userID)
	if err != nil {
		t.Fatalf("get 2fa after verification: %v", err)
	}
	if !record.LastUsedAt.Valid {
		t.Fatal("last_used_at was not recorded")
	}
}

func TestVerifyUserTOTPRequiresWalletOwnedTwoFA(t *testing.T) {
	storeSvc, tenantID := newWalletActivityStore(t)

	_, err := NewSecurityActivities(storeSvc).VerifyUserTOTP(t.Context(), tenantID, 42, "123456")
	if !errors.Is(err, walletstore.ErrUserTwoFANotFound) {
		t.Fatalf("missing wallet-owned 2fa error = %v, want %v", err, walletstore.ErrUserTwoFANotFound)
	}
}

func TestVerifyUserTOTPValidatesTenantBeforeStore(t *testing.T) {
	activities := NewSecurityActivities(&walletstore.Store{})
	cases := []struct {
		name     string
		tenantID string
		wantErr  error
	}{
		{"missing", "", walletstore.ErrMissingTenantID},
		{"invalid", "default", walletstore.ErrInvalidTenantID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := activities.VerifyUserTOTP(t.Context(), tc.tenantID, 42, "123456")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("VerifyUserTOTP() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func newWalletActivityStore(t *testing.T) (*walletstore.Store, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = container.Terminate(cleanupCtx)
	})

	dbName := fmt.Sprintf("wallet_activity_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	db, err := basestore.OpenFromConfig(dbURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = container.DropDatabase(cleanupCtx, dbName)
	})

	tenantID := "tenant-a"
	if err := basestore.MigrateScope(ctx, db, tenantID, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate wallet ledger scope: %v", err)
	}
	if err := basestore.New(db).EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	return walletstore.New(db), tenantID
}
