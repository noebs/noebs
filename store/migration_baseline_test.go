package store

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestPostgresMigrationsAreFreshCanonicalBaselines(t *testing.T) {
	expectedFiles := map[string][]string{
		MigrationScopeIdentityAuth:     {"001_identity_auth.sql"},
		MigrationScopeCardVault:        {"001_card_vault.sql"},
		MigrationScopeEBSAdapter:       {"001_ebs_adapter.sql"},
		MigrationScopeAdminReporting:   {"001_admin_reporting.sql"},
		MigrationScopeNotificationChat: {"001_notification_chat.sql"},
		MigrationScopeWalletLedger:     {"001_wallet_ledger.sql", "002_groosh_money.sql"},
		MigrationScopeWorkloadAuth:     {"001_workload_auth.sql"},
		MigrationScopeGatewayAuth:      {"001_gateway_auth.sql"},
	}
	allFiles, err := fs.Glob(postgresMigrations, "migrations/postgres/*/*.sql")
	if err != nil {
		t.Fatalf("list PostgreSQL migrations: %v", err)
	}
	expectedFileCount := 0
	for _, files := range expectedFiles {
		expectedFileCount += len(files)
	}
	if len(allFiles) != expectedFileCount {
		t.Fatalf("PostgreSQL migrations = %v, want the exact %d reviewed files", allFiles, expectedFileCount)
	}
	for scope, expectedScopeFiles := range expectedFiles {
		t.Run(scope, func(t *testing.T) {
			path := migrationScopePaths[scope]
			entries, err := postgresMigrations.ReadDir(path)
			if err != nil {
				t.Fatalf("read %s migrations: %v", scope, err)
			}
			if !slices.Equal(migrationEntryNames(entries), expectedScopeFiles) {
				t.Fatalf("%s migrations = %v, want exact reviewed set %v", scope, migrationEntryNames(entries), expectedScopeFiles)
			}
			for _, expectedFile := range expectedScopeFiles {
				migration, err := postgresMigrations.ReadFile(path + "/" + expectedFile)
				if err != nil {
					t.Fatalf("read %s migration: %v", scope, err)
				}
				if strings.HasPrefix(expectedFile, "001_") {
					assertCanonicalFreshMigration(t, string(migration))
				} else {
					assertCanonicalAdditiveMigration(t, string(migration))
				}
			}
		})
	}
}

func assertCanonicalAdditiveMigration(t *testing.T, migration string) {
	t.Helper()
	const up = "-- +goose Up"
	const down = "-- +goose Down"
	if strings.Count(migration, up) != 1 || strings.Count(migration, down) != 1 || strings.Index(migration, up) > strings.Index(migration, down) {
		t.Fatal("migration must have exactly one ordered Goose Up and Down section")
	}
	upSQL := strings.ToUpper(strings.SplitN(strings.SplitN(migration, up, 2)[1], down, 2)[0])
	for _, destructive := range []string{"DROP TABLE", "DROP COLUMN", "TRUNCATE ", "DELETE FROM"} {
		if strings.Contains(upSQL, destructive) {
			t.Fatalf("additive migration Up contains destructive SQL %q", destructive)
		}
	}
}

func TestGrooshMoneyMigrationPinsEveryLedgerValueToAnImmutableUnit(t *testing.T) {
	migration, err := postgresMigrations.ReadFile("migrations/postgres/wallet_ledger/002_groosh_money.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(migration)
	if strings.Count(sql, "-- +goose StatementBegin") != strings.Count(sql, "-- +goose StatementEnd") {
		t.Fatal("Groosh migration has unbalanced Goose statement annotations")
	}
	for _, required := range []string{
		"CREATE TABLE currency_unit_versions",
		"currency_unit_versions_one_current",
		"enforce_currency_unit_version_interval",
		"overlapping currency unit version interval",
		"currency_unit_versions_wallet_transition",
		"ALTER TABLE wallets ADD COLUMN currency_unit_version_id BIGINT",
		"wallets_open_currency_unit_required",
		"ALTER TABLE ledger_transactions ADD COLUMN currency_unit_version_id BIGINT",
		"ALTER TABLE ledger_entries ADD COLUMN currency_unit_version_id BIGINT",
		"wallets_currency_unit_fk",
		"ledger_transactions_currency_unit_fk",
		"ledger_entries_wallet_money_fk",
		"ledger_entries_transaction_money_fk",
		"enforce_persisted_money_identity_immutable",
		"wallets_money_identity_immutable",
		"deposit_intents_currency_unit_fk",
		"deposit_intents_wallet_money_fk",
		"manual_transfers_currency_unit_fk",
		"manual_transfers_wallet_currency_fk",
		"manual_transfers_amount_positive",
		"manual_transfers_money_fact_immutable",
		"psp_transactions_currency_unit_fk",
		"psp_transactions_amount_positive",
		"psp_transactions_fee_nonnegative",
		"psp_transactions_net_nonnegative",
		"psp_transactions_money_fact_immutable",
		"psp_transactions_deposit_intent_currency_fk",
		"psp_transaction_amounts_currency_unit_fk",
		"psp_transaction_amounts_fx_base_currency_unit_fk",
		"psp_transaction_amounts_fx_quote_currency_unit_fk",
		"psp_transaction_amounts_fx_observation_fk",
		"psp_transaction_amounts_fx_quote_fk",
		"ADD COLUMN fx_rate_numerator NUMERIC",
		"ADD COLUMN fx_rate_denominator NUMERIC",
		"PSP FX exact rate fraction is not reduced",
		"psp_transaction_amounts_fx_identity_complete",
		"psp_transaction_amounts_money_identity_immutable",
		"psp_configs_legacy_amount_bounds_disabled",
		"psp_config_overrides_legacy_amount_bounds_disabled",
		"CREATE TABLE psp_amount_policies",
		"psp_amount_policies_immutable",
		"transaction_limit_reservations_wallet_currency_fk",
		"CREATE TABLE fx_source_pair_sides",
		"FOREIGN KEY (source_pair_id, side)",
		"observation_base_currency_unit_id",
		"observation_expires_at",
		"CHECK (expires_at = observation_expires_at)",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("Groosh migration is missing invariant %q", required)
		}
	}
	for _, forbidden := range []string{
		"currency_unit_version_id BIGINT DEFAULT",
		"base_currency_unit_id BIGINT DEFAULT",
		"quote_currency_unit_id BIGINT DEFAULT",
		"CLDR-49",
	} {
		if strings.Contains(strings.ToUpper(sql), strings.ToUpper(forbidden)) {
			t.Errorf("Groosh migration silently defaults a money identity via %q", forbidden)
		}
	}
	if !strings.Contains(sql, "('MRU', 2, 2, 2, 20") ||
		!strings.Contains(sql, "MRU-AMI-2017-12-27") {
		t.Error("Groosh migration must pin MRU cash rounding to 0.20 with Mauritanian primary-source provenance")
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
