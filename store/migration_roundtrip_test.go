package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func migrateTestScopeThroughVersion(t *testing.T, db *DB, scope string, version int64) {
	t.Helper()
	contract := migrationAuthorityContracts[scope]
	gooseMigrationMu.Lock()
	defer gooseMigrationMu.Unlock()
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetTableName(contract.versionTable)
	goose.SetBaseFS(postgresMigrations)
	if err := goose.UpToContext(t.Context(), db.DB.DB, contract.migrationPath, version); err != nil {
		t.Fatalf("migrate %s through %d: %v", scope, version, err)
	}
}

func TestDurableLifecycleMigrationsRejectDestructiveRollback(t *testing.T) {
	for _, tc := range []struct {
		scope   string
		version int64
		table   string
	}{
		{MigrationScopeWalletLedger, 7, "transaction_status_events"},
		{MigrationScopeWalletLedger, 8, "transaction_status_events"},
		{MigrationScopeIdentityAuth, 4, "identity_verifications"},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.scope, tc.version), func(t *testing.T) {
			db := newMigrationAuthorityDB(t, tc.scope)
			migrateTestScopeThroughVersion(t, db, tc.scope, tc.version-1)
			migrateTestScopeThroughVersion(t, db, tc.scope, tc.version)
			contract := migrationAuthorityContracts[tc.scope]
			gooseMigrationMu.Lock()
			defer gooseMigrationMu.Unlock()
			if err := goose.SetDialect("postgres"); err != nil {
				t.Fatal(err)
			}
			goose.SetTableName(contract.versionTable)
			goose.SetBaseFS(postgresMigrations)
			if err := goose.DownContext(t.Context(), db.DB.DB, contract.migrationPath); err == nil || !strings.Contains(err.Error(), "use a forward migration") {
				t.Fatalf("destructive rollback error=%v", err)
			}
			version, err := goose.GetDBVersionContext(t.Context(), db.DB.DB)
			if err != nil || version != tc.version {
				t.Fatalf("failed rollback changed migration version: version=%d error=%v", version, err)
			}
			var exists bool
			if err := db.GetContext(t.Context(), &exists, `SELECT to_regclass($1) IS NOT NULL`, tc.table); err != nil || !exists {
				t.Fatalf("durable table dropped: exists=%t error=%v", exists, err)
			}
		})
	}
}
