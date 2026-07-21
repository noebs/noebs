package store

import (
	"testing"

	"github.com/pressly/goose/v3"
)

func TestWalletLedgerMigrationsRoundTrip(t *testing.T) {
	db := newMigrationAuthorityDB(t, MigrationScopeWalletLedger)
	if err := MigrateScope(t.Context(), db, MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate wallet ledger up: %v", err)
	}

	contract := migrationAuthorityContracts[MigrationScopeWalletLedger]
	gooseMigrationMu.Lock()
	defer gooseMigrationMu.Unlock()
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetTableName(contract.versionTable)
	goose.SetBaseFS(postgresMigrations)
	if err := goose.DownToContext(t.Context(), db.DB.DB, contract.migrationPath, 0); err != nil {
		t.Fatalf("migrate wallet ledger down: %v", err)
	}
}
