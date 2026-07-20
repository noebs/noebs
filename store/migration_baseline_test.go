package store

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestPostgresMigrationsAreFreshCanonicalBaselines(t *testing.T) {
	expectedFiles := map[string]string{
		MigrationScopeIdentityAuth:     "001_identity_auth.sql",
		MigrationScopeCardVault:        "001_card_vault.sql",
		MigrationScopeEBSAdapter:       "001_ebs_adapter.sql",
		MigrationScopeAdminReporting:   "001_admin_reporting.sql",
		MigrationScopeNotificationChat: "001_notification_chat.sql",
		MigrationScopeWalletLedger:     "001_wallet_ledger.sql",
		MigrationScopeWorkloadAuth:     "001_workload_auth.sql",
		MigrationScopeGatewayAuth:      "001_gateway_auth.sql",
	}
	allFiles, err := fs.Glob(postgresMigrations, "migrations/postgres/*/*.sql")
	if err != nil {
		t.Fatalf("list PostgreSQL migrations: %v", err)
	}
	if len(allFiles) != len(expectedFiles) {
		t.Fatalf("PostgreSQL migrations = %v, want one file for each of %d scopes", allFiles, len(expectedFiles))
	}
	for scope, expectedFile := range expectedFiles {
		t.Run(scope, func(t *testing.T) {
			path := migrationScopePaths[scope]
			entries, err := postgresMigrations.ReadDir(path)
			if err != nil {
				t.Fatalf("read %s migrations: %v", scope, err)
			}
			if len(entries) != 1 || entries[0].Name() != expectedFile {
				t.Fatalf("%s migrations = %v, want only %s", scope, migrationEntryNames(entries), expectedFile)
			}
			migration, err := postgresMigrations.ReadFile(path + "/" + expectedFile)
			if err != nil {
				t.Fatalf("read %s migration: %v", scope, err)
			}
			assertCanonicalFreshMigration(t, string(migration))
		})
	}
}

func migrationEntryNames(entries []fs.DirEntry) []string {
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}

var transitionalMigrationSQL = regexp.MustCompile(`(?im)\b(?:IF\s+(?:NOT\s+)?EXISTS|ALTER\s+TABLE|DELETE\s+FROM|ALTER\s+DEFAULT\s+PRIVILEGES|GRANT)\b`)

func assertCanonicalFreshMigration(t *testing.T, migration string) {
	t.Helper()
	const up = "-- +goose Up"
	const down = "-- +goose Down"
	if strings.Count(migration, up) != 1 || strings.Count(migration, down) != 1 || strings.Index(migration, up) > strings.Index(migration, down) {
		t.Fatal("migration must have exactly one ordered Goose Up and Down section")
	}
	if forbidden := transitionalMigrationSQL.FindString(migration); forbidden != "" {
		t.Fatalf("migration contains transitional SQL %q", forbidden)
	}
	upSQL := strings.SplitN(strings.SplitN(migration, up, 2)[1], down, 2)[0]
	if strings.Contains(strings.ToUpper(upSQL), "DROP TABLE") {
		t.Fatal("fresh migration creates and drops schema in its Up section")
	}
}
