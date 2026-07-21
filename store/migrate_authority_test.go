package store

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/adonese/noebs/internal/testdb"
)

func TestMigrationAuthorityExcludesVersionTablesAndPublicFunctions(t *testing.T) {
	for scope := range migrationScopeTableNames {
		t.Run(scope, func(t *testing.T) {
			sql := migrationAuthoritySQL(scope)
			if strings.Count(sql, "BEGIN;") != 1 || strings.Count(sql, "COMMIT;") != 1 {
				t.Fatal("migration authority is not rendered as one transaction")
			}
			if !strings.Contains(sql, "REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM PUBLIC") {
				t.Fatal("migration authority does not revoke public function execution")
			}
			if !strings.Contains(sql, "REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM PUBLIC") {
				t.Fatal("migration authority does not reset existing table privileges")
			}
			for _, role := range migrationScopeApplicationRoles[scope] {
				if !strings.Contains(sql, quoteMigrationIdentifier(role)) {
					t.Fatalf("migration authority omits %s", role)
				}
			}
		})
	}
}

func TestMigrationAuthorityRepairsDependentGrantDrift(t *testing.T) {
	db := newMigrationAuthorityDB(t, MigrationScopeIdentityAuth)
	if err := MigrateScope(t.Context(), db, MigrationScopeIdentityAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "GRANT SELECT ON public.users TO identity_auth_runtime WITH GRANT OPTION"); err != nil {
		t.Fatal(err)
	}
	runtime := openMigrationAuthorityRoleDB(t, "identity_auth", "identity_auth_runtime")
	if _, err := runtime.ExecContext(t.Context(), "GRANT SELECT ON public.users TO card_vault_runtime"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	if err := MigrateScope(t.Context(), db, MigrationScopeIdentityAuth); err != nil {
		t.Fatalf("repair authority drift: %v", err)
	}
	assertTablePrivileges(t, db, "identity_auth_runtime", "users",
		[]string{"SELECT", "INSERT", "UPDATE", "DELETE"},
		[]string{"SELECT WITH GRANT OPTION", "TRUNCATE", "REFERENCES", "TRIGGER"},
	)
	assertTablePrivileges(t, db, "card_vault_runtime", "users", nil,
		[]string{"SELECT", "INSERT", "UPDATE", "DELETE"},
	)
}

func TestMigrationAuthorityRollbackPreservesPriorContract(t *testing.T) {
	db := newMigrationAuthorityDB(t, MigrationScopeIdentityAuth)
	if err := MigrateScope(t.Context(), db, MigrationScopeIdentityAuth); err != nil {
		t.Fatal(err)
	}
	original := migrationAuthorityContracts[MigrationScopeIdentityAuth]
	broken := original
	broken.specialGrants = append(
		append([]string(nil), original.specialGrants...),
		"GRANT SELECT ON TABLE public.authority_table_that_does_not_exist TO identity_auth_runtime",
	)
	migrationAuthorityContracts[MigrationScopeIdentityAuth] = broken
	defer func() { migrationAuthorityContracts[MigrationScopeIdentityAuth] = original }()

	if err := MigrateScope(t.Context(), db, MigrationScopeIdentityAuth); err == nil {
		t.Fatal("migration authority accepted an invalid exact grant")
	}
	assertTablePrivileges(t, db, "identity_auth_runtime", "users",
		[]string{"SELECT", "INSERT", "UPDATE", "DELETE"},
		[]string{"SELECT WITH GRANT OPTION", "TRUNCATE", "REFERENCES", "TRIGGER"},
	)
	var one int
	if err := db.GetContext(t.Context(), &one, "SELECT 1"); err != nil || one != 1 {
		t.Fatalf("connection is unusable after authority rollback: value=%d err=%v", one, err)
	}
}

func TestMigrationAuthorityRejectsWrongIdentityAndDatabase(t *testing.T) {
	db := newMigrationAuthorityDB(t, MigrationScopeIdentityAuth)
	if err := MigrateScope(t.Context(), db, MigrationScopeCardVault); err == nil {
		t.Fatal("card-vault migration accepted the identity-auth database/login")
	}
	if err := MigrateScope(t.Context(), db, MigrationScopeIdentityAuth); err != nil {
		t.Fatal(err)
	}
	runtime := openMigrationAuthorityRoleDB(t, "identity_auth", "identity_auth_runtime")
	t.Cleanup(func() { _ = runtime.Close() })
	if err := MigrateScope(t.Context(), runtime, MigrationScopeIdentityAuth); err == nil {
		t.Fatal("runtime login accepted as migration authority")
	}
}

func TestMigrationAuthorityRejectsUnexpectedSchemasAndObjectOwners(t *testing.T) {
	db := newMigrationAuthorityDB(t, MigrationScopeIdentityAuth)
	if err := MigrateScope(t.Context(), db, MigrationScopeIdentityAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "CREATE SCHEMA rogue_authority"); err != nil {
		t.Fatal(err)
	}
	if err := MigrateScope(t.Context(), db, MigrationScopeIdentityAuth); err == nil {
		t.Fatal("migration authority accepted an unexpected user schema")
	}
	if _, err := db.ExecContext(t.Context(), "DROP SCHEMA rogue_authority"); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(t.Context(), "GRANT CREATE ON SCHEMA public TO identity_auth_runtime"); err != nil {
		t.Fatal(err)
	}
	runtime := openMigrationAuthorityRoleDB(t, "identity_auth", "identity_auth_runtime")
	if _, err := runtime.ExecContext(t.Context(), "CREATE TABLE public.runtime_owned_drift (id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := MigrateScope(t.Context(), db, MigrationScopeIdentityAuth); err == nil {
		t.Fatal("migration authority accepted a runtime-owned public object")
	}
}

func TestWalletAuthorityClassifiesEveryPublicTable(t *testing.T) {
	db := newMigrationAuthorityDB(t, MigrationScopeWalletLedger)
	if err := MigrateScope(t.Context(), db, MigrationScopeWalletLedger); err != nil {
		t.Fatal(err)
	}
	var tables []string
	query := "SELECT relation.relname FROM pg_class relation " +
		"JOIN pg_namespace schema ON schema.oid = relation.relnamespace " +
		"WHERE schema.nspname = 'public' AND relation.relkind IN ('r', 'p') " +
		"AND relation.relname <> $1 ORDER BY relation.relname"
	if err := db.SelectContext(t.Context(), &tables, query, migrationScopeTableNames[MigrationScopeWalletLedger]); err != nil {
		t.Fatal(err)
	}
	expected := []string{
		"balance_holds",
		"currencies",
		"currency_unit_versions",
		"deposit_intents",
		"exchange_rates",
		"fee_configs",
		"funding_source_withdrawal_reservations",
		"funding_sources",
		"fx_observations",
		"fx_source_pair_sides",
		"fx_source_pairs",
		"fx_sources",
		"ledger_entries",
		"ledger_funding_links",
		"ledger_transactions",
		"ledger_withdrawal_destination_links",
		"manual_transfer_approvals",
		"manual_transfers",
		"money_conversion_quotes",
		"operator_identities",
		"p2p_commands",
		"psp_amount_policies",
		"psp_config_overrides",
		"psp_configs",
		"psp_interactions",
		"psp_transaction_amounts",
		"psp_transactions",
		"tenants",
		"transaction_limit_period_usage",
		"transaction_limit_reservations",
		"transaction_limits",
		"wallet_audit_log",
		"wallets",
		"withdrawal_destinations",
		"workflow_decisions",
	}
	if !slices.Equal(tables, expected) {
		t.Fatalf("wallet public tables = %v, want exact classified set %v", tables, expected)
	}
}

func TestWalletRuntimeAndWorkerSQLAuthority(t *testing.T) {
	db := newMigrationAuthorityDB(t, MigrationScopeWalletLedger)
	if err := MigrateScope(t.Context(), db, MigrationScopeWalletLedger); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO tenants(id, name) VALUES ('tenant-authority', 'Authority Test')`); err != nil {
		t.Fatal(err)
	}
	var observationID int64
	if err := db.GetContext(t.Context(), &observationID, `
		WITH timing AS (
			SELECT clock_timestamp() - interval '1 minute' AS observation_at,
			       clock_timestamp() - interval '30 seconds' AS retrieved_at
		)
		INSERT INTO fx_observations(
			source_id, source_pair_id, external_series,
			base_currency_code, quote_currency_code,
			base_currency_unit_id, quote_currency_unit_id,
			rate, side, purpose, observation_at, published_at,
			retrieved_at, expires_at, raw_payload_sha256, source_revision
		)
		SELECT source.id, pair.id, pair.external_series,
		       pair.base_currency_code, pair.quote_currency_code,
		       base_unit.id, quote_unit.id,
		       1.08, 'mid', source.purpose, timing.observation_at, NULL,
		       timing.retrieved_at,
		       timing.observation_at + make_interval(secs => source.max_age_seconds),
		       repeat('a', 64), 'runtime-authority-fixture'
		FROM fx_sources source
		JOIN fx_source_pairs pair ON pair.source_id = source.id
		JOIN currency_unit_versions base_unit
		  ON base_unit.currency_code = pair.base_currency_code AND base_unit.valid_to IS NULL
		JOIN currency_unit_versions quote_unit
		  ON quote_unit.currency_code = pair.quote_currency_code AND quote_unit.valid_to IS NULL
		CROSS JOIN timing
		WHERE source.code = 'ecb-reference'
		  AND pair.external_series = 'D.USD.EUR.SP00.A'
		RETURNING id
	`); err != nil {
		t.Fatalf("seed runtime quote observation: %v", err)
	}

	runtime := openMigrationAuthorityRoleDB(t, "wallet_ledger", "wallet_ledger_runtime")
	t.Cleanup(func() { _ = runtime.Close() })
	worker := openMigrationAuthorityRoleDB(t, "wallet_ledger", "wallet_ledger_worker")
	t.Cleanup(func() { _ = worker.Close() })
	// Quote-backed PSP amount validation reads the referenced quote both in Go
	// and in the INSERT trigger. Exercise the runtime role grant explicitly so
	// owner-backed integration tests cannot hide a production permission gap.
	var quoteCount int
	if err := worker.GetContext(t.Context(), &quoteCount, `SELECT count(*) FROM money_conversion_quotes`); err != nil {
		t.Fatalf("worker read money conversion quotes: %v", err)
	}
	var quoteID string
	if err := runtime.GetContext(t.Context(), &quoteID, `
		INSERT INTO money_conversion_quotes(
			tenant_id, requested_by_user_id, idempotency_key,
			observation_id, observation_base_currency_unit_id,
			observation_quote_currency_unit_id,
			observation_base_currency_code, observation_quote_currency_code,
			observation_expires_at, input_currency_unit_id,
			output_currency_unit_id, input_currency_code, output_currency_code,
			input_minor_units, output_minor_units, inverse, rounding_mode,
			conversion_at, expires_at
		)
		SELECT 'tenant-authority', 101, 'runtime-authority-quote',
		       observation.id, observation.base_currency_unit_id,
		       observation.quote_currency_unit_id,
		       observation.base_currency_code, observation.quote_currency_code,
		       observation.expires_at, observation.base_currency_unit_id,
		       observation.quote_currency_unit_id,
		       observation.base_currency_code, observation.quote_currency_code,
		       100, 108, FALSE, 'half_even', observation.created_at,
		       observation.expires_at
		FROM fx_observations observation
		WHERE observation.id = $1
		RETURNING id::text
	`, observationID); err != nil {
		t.Fatalf("runtime insert money conversion quote through snapshot trigger: %v", err)
	}
	if quoteID == "" {
		t.Fatal("runtime quote insert returned an empty id")
	}

	var operatorID int64
	if err := runtime.GetContext(t.Context(), &operatorID, `
		INSERT INTO operator_identities(issuer, subject)
		VALUES ('https://issuer.example', 'runtime-operator')
		RETURNING id
	`); err != nil {
		t.Fatalf("runtime insert operator identity: %v", err)
	}
	var approverID int64
	if err := runtime.GetContext(t.Context(), &approverID, `
		INSERT INTO operator_identities(issuer, subject)
		VALUES ('https://issuer.example', 'runtime-approver')
		RETURNING id
	`); err != nil {
		t.Fatalf("runtime insert approving operator identity: %v", err)
	}
	var userWalletID string
	if err := runtime.GetContext(t.Context(), &userWalletID, `
		INSERT INTO wallets(
			tenant_id, owner_type, owner_id, user_id, currency, currency_unit_version_id, kyc_tier
		) VALUES (
			'tenant-authority', 'user', '101', 101, 'USD',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'USD' AND valid_to IS NULL),
			'verified'
		)
		RETURNING id::text
	`); err != nil {
		t.Fatalf("runtime insert wallet: %v", err)
	}
	var configID int64
	if err := runtime.GetContext(t.Context(), &configID, `
		INSERT INTO fee_configs(
			tenant_id, transaction_type, currency, currency_unit_version_id,
			percentage_fee, created_by_operator_id
		) VALUES (
			'tenant-authority', 'p2p', 'USD',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'USD' AND valid_to IS NULL),
			0, $1
		)
		RETURNING id
	`, operatorID); err != nil {
		t.Fatalf("runtime insert fee config: %v", err)
	}
	var rateID int64
	if err := runtime.GetContext(t.Context(), &rateID, `
		INSERT INTO exchange_rates(
			tenant_id, base_currency, base_currency_unit_version_id,
			quote_currency, quote_currency_unit_version_id,
			buy_rate, sell_rate, set_by_operator_id
		) VALUES (
			'tenant-authority', 'USD',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'USD' AND valid_to IS NULL),
			'AED',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'AED' AND valid_to IS NULL),
			3.67, 3.68, $1
		)
		RETURNING id
	`, operatorID); err != nil {
		t.Fatalf("runtime insert exchange rate: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO transaction_limits(
			tenant_id, kyc_tier, transaction_type, currency, currency_unit_version_id,
			daily_limit, monthly_limit, per_transaction_limit
		) VALUES (
			'tenant-authority', 'verified', 'p2p', 'USD',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'USD' AND valid_to IS NULL),
			1000, 10000, 500
		)
	`); err != nil {
		t.Fatal(err)
	}
	var manualTransferID int64
	if err := runtime.GetContext(t.Context(), &manualTransferID, `
		INSERT INTO manual_transfers(
			tenant_id, workflow_id, idempotency_key, transfer_type, wallet_id,
			amount, currency, currency_unit_version_id, reason, requested_by_operator_id,
			approval_timeout_seconds, decision_deadline_at
		) VALUES (
			'tenant-authority', 'manual-authority', 'manual-authority', 'credit', $1,
			25, 'USD',
			(SELECT currency_unit_version_id FROM wallets WHERE tenant_id = 'tenant-authority' AND id = $1),
			'authority test', $2, 300, clock_timestamp() + interval '5 minutes'
		)
		RETURNING id
	`, userWalletID, operatorID); err != nil {
		t.Fatalf("runtime insert manual transfer: %v", err)
	}
	decisionTx, err := runtime.BeginTxx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decisionTx.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock($1)`, int64(9122026)); err != nil {
		_ = decisionTx.Rollback()
		t.Fatalf("runtime acquire decision lock: %v", err)
	}
	var manualStatus string
	if err := decisionTx.GetContext(t.Context(), &manualStatus, `
		SELECT status FROM manual_transfers
		WHERE tenant_id = 'tenant-authority' AND id = $1 AND workflow_id = 'manual-authority'
	`, manualTransferID); err != nil {
		_ = decisionTx.Rollback()
		t.Fatalf("runtime read decision target: %v", err)
	}
	if manualStatus != "pending" {
		_ = decisionTx.Rollback()
		t.Fatalf("runtime decision target status = %q, want pending", manualStatus)
	}
	if _, err := decisionTx.ExecContext(t.Context(), `
		INSERT INTO workflow_decisions(
			tenant_id, workflow_id, decision_kind, subject_id, approved,
			decided_by_operator_id, proof_of_payment
		) VALUES ('tenant-authority', 'manual-authority', 'manual_transfer', $1, TRUE, $2, 'proof-1')
	`, manualTransferID, approverID); err != nil {
		_ = decisionTx.Rollback()
		t.Fatalf("runtime insert workflow decision: %v", err)
	}
	if err := decisionTx.Commit(); err != nil {
		t.Fatal(err)
	}
	executeWalletAuthoritySQL(t, worker, `
		UPDATE manual_transfers
		SET status = 'approved', approved_by_operator_id = $1,
			proof_of_payment = 'proof-1', approved_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND id = $2
	`, approverID, manualTransferID)

	var systemWalletID string
	if err := worker.GetContext(t.Context(), &systemWalletID, `
		INSERT INTO wallets(
			tenant_id, owner_type, owner_id, currency, currency_unit_version_id, kyc_tier
		) VALUES (
			'tenant-authority', 'system', 'settlement', 'USD',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'USD' AND valid_to IS NULL),
			'verified'
		)
		RETURNING id::text
	`); err != nil {
		t.Fatalf("worker insert wallet: %v", err)
	}
	executeWalletAuthoritySQL(t, worker, `
		UPDATE wallets
		SET balance = 1000, available_balance = 1000, version = version + 1, updated_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND id = $1
	`, userWalletID)

	var fundingSourceID int64
	if err := worker.GetContext(t.Context(), &fundingSourceID, `
		INSERT INTO funding_sources(
			tenant_id, wallet_id, source_type, psp_provider, external_reference,
			verification_status, verified_at, verified_by, currency, source_details,
			supports_withdrawal, withdrawal_method
		) VALUES (
			'tenant-authority', $1, 'bank_account', 'test-psp', 'source-1',
			'verified', clock_timestamp(), 'worker', 'USD', '{}'::jsonb, TRUE, '{}'::jsonb
		)
		RETURNING id
	`, userWalletID); err != nil {
		t.Fatalf("worker insert funding source: %v", err)
	}
	var destinationID int64
	if err := runtime.GetContext(t.Context(), &destinationID, `
		INSERT INTO withdrawal_destinations(
			tenant_id, wallet_id, destination_type, psp_provider,
			destination_details, currency, linked_funding_source_id
		) VALUES ('tenant-authority', $1, 'bank_account', 'test-psp', '{}'::jsonb, 'USD', $2)
		RETURNING id
	`, userWalletID, fundingSourceID); err != nil {
		t.Fatalf("runtime insert withdrawal destination: %v", err)
	}
	executeWalletAuthoritySQL(t, runtime, `
		UPDATE withdrawal_destinations SET is_active = TRUE, updated_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND id = $1
	`, destinationID)

	var ledgerTransactionID int64
	if err := worker.GetContext(t.Context(), &ledgerTransactionID, `
		INSERT INTO ledger_transactions(
			tenant_id, idempotency_key, currency, currency_unit_version_id,
			reference_type, reference_id, status
		) VALUES (
			'tenant-authority', 'authority-ledger', 'USD',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'USD' AND valid_to IS NULL),
			'p2p', 'authority-ledger', 'completed'
		)
		RETURNING id
	`); err != nil {
		t.Fatalf("worker insert ledger transaction: %v", err)
	}
	var debitEntryID, creditEntryID int64
	if err := worker.GetContext(t.Context(), &debitEntryID, `
		INSERT INTO ledger_entries(
			tenant_id, transaction_id, wallet_id, entry_type, amount,
			currency, currency_unit_version_id, balance_after, wallet_sequence, status
		) VALUES (
			'tenant-authority', $1, $2, 'debit', 100, 'USD',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'USD' AND valid_to IS NULL),
			900, 2, 'completed'
		)
		RETURNING id
	`, ledgerTransactionID, userWalletID); err != nil {
		t.Fatalf("worker insert debit entry: %v", err)
	}
	if err := worker.GetContext(t.Context(), &creditEntryID, `
		INSERT INTO ledger_entries(
			tenant_id, transaction_id, wallet_id, entry_type, amount,
			currency, currency_unit_version_id, balance_after, wallet_sequence, status
		) VALUES (
			'tenant-authority', $1, $2, 'credit', 100, 'USD',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'USD' AND valid_to IS NULL),
			100, 1, 'completed'
		)
		RETURNING id
	`, ledgerTransactionID, systemWalletID); err != nil {
		t.Fatalf("worker insert credit entry: %v", err)
	}
	executeWalletAuthoritySQL(t, worker, `UPDATE ledger_entries SET counter_entry_id = $1 WHERE id = $2`, creditEntryID, debitEntryID)
	executeWalletAuthoritySQL(t, worker, `UPDATE ledger_entries SET counter_entry_id = $1 WHERE id = $2`, debitEntryID, creditEntryID)
	executeWalletAuthoritySQL(t, worker, `
		UPDATE wallets
		SET balance = 900, available_balance = 900, version = version + 1, updated_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND id = $1
	`, userWalletID)
	executeWalletAuthoritySQL(t, worker, `
		UPDATE wallets
		SET balance = 100, available_balance = 100, version = version + 1, updated_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND id = $1
	`, systemWalletID)

	var holdID int64
	if err := worker.GetContext(t.Context(), &holdID, `
		INSERT INTO balance_holds(
			tenant_id, wallet_id, amount, amount_remaining, reason, reference_type,
			reference_id, idempotency_key, expires_at
		) VALUES ('tenant-authority', $1, 50, 50, 'authority test', 'withdrawal', 'hold-1', 'hold-1', clock_timestamp() + interval '1 hour')
		RETURNING id
	`, userWalletID); err != nil {
		t.Fatalf("worker insert balance hold: %v", err)
	}
	executeWalletAuthoritySQL(t, worker, `
		UPDATE balance_holds
		SET status = 'released', amount_remaining = 0, released_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND id = $1
	`, holdID)
	executeWalletAuthoritySQL(t, worker, `
		INSERT INTO wallet_audit_log(tenant_id, event_type, actor_type, actor_id, action)
		VALUES ('tenant-authority', 'ledger_posted', 'worker', 'wallet-worker', 'post')
	`)
	executeWalletAuthoritySQL(t, worker, `
		UPDATE funding_sources
		SET total_funded = 100, last_funded_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND id = $1
	`, fundingSourceID)
	executeWalletAuthoritySQL(t, worker, `
		UPDATE withdrawal_destinations
		SET last_used_at = clock_timestamp(), total_withdrawn = 100, updated_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND id = $1
	`, destinationID)

	executeWalletAuthoritySQL(t, worker, `
		INSERT INTO transaction_limit_period_usage(
			tenant_id, wallet_id, transaction_type, currency, period_kind, period_start
		) VALUES
			('tenant-authority', $1, 'p2p', 'USD', 'daily', current_date),
			('tenant-authority', $1, 'p2p', 'USD', 'monthly', date_trunc('month', current_date)::date)
	`, userWalletID)
	executeWalletAuthoritySQL(t, worker, `
		UPDATE transaction_limit_period_usage
		SET reserved_amount = 100, updated_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND wallet_id = $1
	`, userWalletID)
	var limitReservationID int64
	if err := worker.GetContext(t.Context(), &limitReservationID, `
		INSERT INTO transaction_limit_reservations(
			tenant_id, command_id, wallet_id, transaction_type, currency, amount,
			daily_period_start, monthly_period_start, status
		) VALUES (
			'tenant-authority', 'limit-command-1', $1, 'p2p', 'USD', 100,
			current_date, date_trunc('month', current_date)::date, 'reserved'
		)
		RETURNING id
	`, userWalletID); err != nil {
		t.Fatalf("worker insert limit reservation: %v", err)
	}
	executeWalletAuthoritySQL(t, worker, `
		UPDATE transaction_limit_reservations
		SET status = 'consumed', ledger_transaction_id = $1, consumed_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND id = $2
	`, ledgerTransactionID, limitReservationID)
	executeWalletAuthoritySQL(t, worker, `
		UPDATE transaction_limit_period_usage
		SET reserved_amount = 0, consumed_amount = 100, updated_at = clock_timestamp()
		WHERE tenant_id = 'tenant-authority' AND wallet_id = $1
	`, userWalletID)

	var count int
	if err := runtime.GetContext(t.Context(), &count, `
		SELECT count(*) FROM ledger_entries entry
		JOIN ledger_transactions ledger_tx
		  ON ledger_tx.tenant_id = entry.tenant_id AND ledger_tx.id = entry.transaction_id
		WHERE entry.tenant_id = 'tenant-authority'
	`); err != nil || count != 2 {
		t.Fatalf("runtime read ledger history: count=%d err=%v", count, err)
	}
	if err := runtime.GetContext(t.Context(), &count, `SELECT count(*) FROM transaction_limits WHERE tenant_id = 'tenant-authority'`); err != nil || count != 1 {
		t.Fatalf("runtime read limits: count=%d err=%v", count, err)
	}
	if err := runtime.GetContext(t.Context(), &count, `SELECT count(*) FROM wallet_audit_log WHERE tenant_id = 'tenant-authority'`); err != nil || count != 1 {
		t.Fatalf("runtime read audit log: count=%d err=%v", count, err)
	}
	if err := worker.GetContext(t.Context(), &count, `
		SELECT count(*) FROM transaction_limit_reservations
		WHERE tenant_id = 'tenant-authority' AND status = 'consumed'
	`); err != nil || count != 1 {
		t.Fatalf("worker read limit reservations: count=%d err=%v", count, err)
	}
	lockTx, err := worker.BeginTxx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	lockingStatements := []struct {
		statement string
		args      []any
	}{
		{statement: `SELECT id::text FROM wallets WHERE tenant_id = 'tenant-authority' AND id = $1 FOR UPDATE`, args: []any{userWalletID}},
		{statement: `SELECT id::text FROM balance_holds WHERE tenant_id = 'tenant-authority' AND id = $1 FOR UPDATE`, args: []any{holdID}},
		{statement: `SELECT id::text FROM funding_sources WHERE tenant_id = 'tenant-authority' AND id = $1 FOR UPDATE`, args: []any{fundingSourceID}},
		{statement: `SELECT reserved_amount::text FROM transaction_limit_period_usage WHERE tenant_id = 'tenant-authority' AND wallet_id = $1 AND period_kind = 'daily' FOR UPDATE`, args: []any{userWalletID}},
		{statement: `SELECT id::text FROM transaction_limit_reservations WHERE tenant_id = 'tenant-authority' AND id = $1 FOR UPDATE`, args: []any{limitReservationID}},
	}
	for _, statement := range lockingStatements {
		var value string
		if err := lockTx.GetContext(t.Context(), &value, statement.statement, statement.args...); err != nil {
			_ = lockTx.Rollback()
			t.Fatalf("worker lock owned row: %v", err)
		}
	}
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	attacks := []struct {
		name      string
		principal *DB
		statement string
		args      []any
	}{
		{name: "runtime-delete-wallet", principal: runtime, statement: `DELETE FROM wallets WHERE tenant_id = 'tenant-authority' AND id = $1`, args: []any{userWalletID}},
		{name: "runtime-rewrite-wallet-balance", principal: runtime, statement: `UPDATE wallets SET balance = balance WHERE tenant_id = 'tenant-authority' AND id = $1`, args: []any{userWalletID}},
		{name: "runtime-rewrite-destination-usage", principal: runtime, statement: `UPDATE withdrawal_destinations SET total_withdrawn = total_withdrawn WHERE tenant_id = 'tenant-authority' AND id = $1`, args: []any{destinationID}},
		{name: "runtime-rewrite-manual-status", principal: runtime, statement: `UPDATE manual_transfers SET status = status WHERE tenant_id = 'tenant-authority' AND id = $1`, args: []any{manualTransferID}},
		{name: "worker-delete-ledger", principal: worker, statement: `DELETE FROM ledger_transactions WHERE tenant_id = 'tenant-authority' AND id = $1`, args: []any{ledgerTransactionID}},
		{name: "worker-rewrite-ledger-status", principal: worker, statement: `UPDATE ledger_transactions SET status = status WHERE tenant_id = 'tenant-authority' AND id = $1`, args: []any{ledgerTransactionID}},
		{name: "worker-rewrite-entry-amount", principal: worker, statement: `UPDATE ledger_entries SET amount = amount WHERE tenant_id = 'tenant-authority' AND id = $1`, args: []any{debitEntryID}},
		{name: "worker-reassign-wallet", principal: worker, statement: `UPDATE wallets SET owner_id = owner_id WHERE tenant_id = 'tenant-authority' AND id = $1`, args: []any{userWalletID}},
		{name: "worker-rewrite-limit-period", principal: worker, statement: `UPDATE transaction_limit_period_usage SET period_start = period_start WHERE tenant_id = 'tenant-authority' AND wallet_id = $1`, args: []any{userWalletID}},
		{name: "worker-rewrite-limit-amount", principal: worker, statement: `UPDATE transaction_limit_reservations SET amount = amount WHERE tenant_id = 'tenant-authority' AND id = $1`, args: []any{limitReservationID}},
		{name: "worker-rewrite-manual-amount", principal: worker, statement: `UPDATE manual_transfers SET amount = amount WHERE tenant_id = 'tenant-authority' AND id = $1`, args: []any{manualTransferID}},
		{name: "worker-delete-audit", principal: worker, statement: `DELETE FROM wallet_audit_log WHERE tenant_id = 'tenant-authority'`},
	}
	for _, attack := range attacks {
		t.Run(attack.name, func(t *testing.T) {
			_, err := attack.principal.ExecContext(t.Context(), attack.statement, attack.args...)
			if err == nil {
				t.Fatal("statement unexpectedly succeeded")
			}
			assertInsufficientPrivilege(t, err)
		})
	}
}

func executeWalletAuthoritySQL(t *testing.T, db *DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

func TestFreshMigrationsKeepApplicationRolesOutOfMigrationAuthority(t *testing.T) {
	tests := []struct {
		scope         string
		runtimeRole   string
		businessTable string
		sequence      string
	}{
		{MigrationScopeIdentityAuth, "identity_auth_runtime", "users", "users_id_seq"},
		{MigrationScopeCardVault, "card_vault_runtime", "cards", "cards_id_seq"},
		{MigrationScopeEBSAdapter, "ebs_adapter_runtime", "transactions", "transactions_id_seq"},
		{MigrationScopeAdminReporting, "admin_reporting_runtime", "transactions", "transactions_id_seq"},
		{MigrationScopeNotificationChat, "notification_chat_runtime", "chats", ""},
		{MigrationScopeWalletLedger, "wallet_ledger_runtime", "wallets", "operator_identities_id_seq"},
		{MigrationScopeWorkloadAuth, "workload_auth_runtime", "workload_request_nonces", ""},
		{MigrationScopeGatewayAuth, "gateway_auth_runtime", "backoffice_auth_flows", ""},
	}
	for _, test := range tests {
		t.Run(test.scope, func(t *testing.T) {
			db := newMigrationAuthorityDB(t, test.scope)
			if err := MigrateScope(t.Context(), db, test.scope); err != nil {
				t.Fatalf("migrate %s: %v", test.scope, err)
			}
			for _, role := range migrationScopeApplicationRoles[test.scope] {
				for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
					if hasTablePrivilege(t, db, role, migrationScopeTableNames[test.scope], privilege) {
						t.Fatalf("%s has %s on migration table %s", role, privilege, migrationScopeTableNames[test.scope])
					}
				}
				if hasSchemaPrivilege(t, db, role, "public", "CREATE") {
					t.Fatalf("%s has CREATE on public schema", role)
				}
				if hasSchemaPrivilege(t, db, role, "public", "USAGE WITH GRANT OPTION") {
					t.Fatalf("%s has schema USAGE with grant option", role)
				}
				assertNoRoleMembership(t, db, role)
			}
			assertNoPublicFunctionExecution(t, db)

			runtimeRole := test.runtimeRole
			switch test.scope {
			case MigrationScopeEBSAdapter:
				assertTablePrivileges(t, db, runtimeRole, test.businessTable, []string{"SELECT", "INSERT", "UPDATE", "DELETE"}, []string{"TRUNCATE", "REFERENCES", "TRIGGER", "SELECT WITH GRANT OPTION"})
				eventsRole := "ebs_adapter_events"
				assertTablePrivileges(t, db, eventsRole, "transaction_events", []string{"SELECT"}, []string{"INSERT", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "SELECT WITH GRANT OPTION"})
				for _, column := range []string{"publish_attempts", "updated_at", "published_at", "last_error"} {
					if !hasColumnPrivilege(t, db, eventsRole, "transaction_events", column, "UPDATE") {
						t.Fatalf("%s lacks UPDATE on transaction_events.%s", eventsRole, column)
					}
				}
				if hasColumnPrivilege(t, db, eventsRole, "transaction_events", "payload", "UPDATE") {
					t.Fatalf("%s can update transaction_events.payload", eventsRole)
				}
			case MigrationScopeAdminReporting:
				assertTablePrivileges(t, db, runtimeRole, test.businessTable, []string{"SELECT", "INSERT", "UPDATE", "DELETE"}, []string{"TRUNCATE", "REFERENCES", "TRIGGER", "SELECT WITH GRANT OPTION"})
				projector := "admin_reporting_projector"
				assertTablePrivileges(t, db, projector, "transactions", []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "SELECT WITH GRANT OPTION"})
				assertSequenceUsageOnly(t, db, projector, "transactions_id_seq")
			case MigrationScopeWalletLedger:
				assertWalletCorePrivileges(t, db)
				assertWalletDecisionPrivileges(t, db)
				assertWalletImmutableCommandPrivileges(t, db)
				assertWalletWithdrawalReservationPrivileges(t, db)
				assertWalletPSPPrivileges(t, db)
			case MigrationScopeWorkloadAuth:
				assertTablePrivileges(t, db, runtimeRole, test.businessTable, []string{"INSERT"}, []string{"SELECT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "INSERT WITH GRANT OPTION"})
				cleanup := "workload_auth_cleanup"
				assertTablePrivileges(t, db, cleanup, test.businessTable, []string{"DELETE"}, []string{"SELECT", "INSERT", "UPDATE", "TRUNCATE", "REFERENCES", "TRIGGER", "DELETE WITH GRANT OPTION"})
				if !hasColumnPrivilege(t, db, cleanup, test.businessTable, "expires_at", "SELECT") || hasColumnPrivilege(t, db, cleanup, test.businessTable, "nonce", "SELECT") {
					t.Fatalf("%s does not have exact expires_at read authority", cleanup)
				}
			case MigrationScopeGatewayAuth:
				assertTablePrivileges(t, db, runtimeRole, test.businessTable, []string{"SELECT", "INSERT", "UPDATE", "DELETE"}, []string{"TRUNCATE", "REFERENCES", "TRIGGER", "SELECT WITH GRANT OPTION"})
				cleanup := "gateway_auth_cleanup"
				assertTablePrivileges(t, db, cleanup, test.businessTable, []string{"DELETE"}, []string{"SELECT", "INSERT", "UPDATE", "TRUNCATE", "REFERENCES", "TRIGGER", "DELETE WITH GRANT OPTION"})
				if !hasColumnPrivilege(t, db, cleanup, test.businessTable, "expires_at", "SELECT") || hasColumnPrivilege(t, db, cleanup, test.businessTable, "state_hash", "SELECT") {
					t.Fatalf("%s does not have exact expires_at read authority", cleanup)
				}
			default:
				assertTablePrivileges(t, db, runtimeRole, test.businessTable, []string{"SELECT", "INSERT", "UPDATE", "DELETE"}, []string{"TRUNCATE", "REFERENCES", "TRIGGER", "SELECT WITH GRANT OPTION"})
			}
			if test.sequence != "" {
				assertSequenceUsageOnly(t, db, runtimeRole, test.sequence)
			}
		})
	}
}

func newMigrationAuthorityDB(t *testing.T, scope string) *DB {
	t.Helper()
	ctx := context.Background()
	validationPostgresOnce.Do(func() {
		validationPostgres, validationPostgresErr = testdb.StartPostgresContainer(ctx)
	})
	if validationPostgresErr != nil {
		if testdb.IsContainerRuntimeUnavailable(validationPostgresErr) {
			t.Skipf("container runtime unavailable: %v", validationPostgresErr)
		}
		t.Fatalf("start postgres: %v", validationPostgresErr)
	}
	contract := migrationAuthorityContracts[scope]
	databaseName := contract.database
	migrateRole := strings.ReplaceAll(scope, "-", "_") + "_migrate"
	databaseURL, err := validationPostgres.CreateDatabaseForRole(ctx, databaseName, migrateRole)
	if err != nil {
		t.Fatalf("create %s database: %v", scope, err)
	}
	db, err := OpenFromConfig(databaseURL, DriverPostgres)
	if err != nil {
		t.Fatalf("open %s database: %v", scope, err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = validationPostgres.DropDatabase(context.Background(), databaseName)
	})
	return db
}

func openMigrationAuthorityRoleDB(t *testing.T, databaseName, role string) *DB {
	t.Helper()
	databaseURL, err := validationPostgres.DatabaseURLForRole(databaseName, role)
	if err != nil {
		t.Fatalf("resolve %s database URL: %v", role, err)
	}
	db, err := OpenFromConfig(databaseURL, DriverPostgres)
	if err != nil {
		t.Fatalf("open %s database: %v", role, err)
	}
	return db
}

func assertWalletCorePrivileges(t *testing.T, db *DB) {
	t.Helper()
	tables := []struct {
		name           string
		runtime        []string
		worker         []string
		runtimeUpdates []string
		workerUpdates  []string
	}{
		{name: "operator_identities", runtime: []string{"SELECT", "INSERT"}, worker: []string{"SELECT"}},
		{
			name: "wallets", runtime: []string{"SELECT", "INSERT"}, worker: []string{"SELECT", "INSERT"},
			workerUpdates: []string{"available_balance", "balance", "updated_at", "version"},
		},
		{name: "ledger_transactions", runtime: []string{"SELECT"}, worker: []string{"SELECT", "INSERT"}},
		{
			name: "ledger_entries", runtime: []string{"SELECT"}, worker: []string{"SELECT", "INSERT"},
			workerUpdates: []string{"counter_entry_id"},
		},
		{
			name: "balance_holds", worker: []string{"SELECT", "INSERT"},
			workerUpdates: []string{"amount_remaining", "captured_at", "committed_at", "expired_at", "released_at", "status"},
		},
		{name: "fee_configs", runtime: []string{"SELECT", "INSERT"}, worker: []string{"SELECT"}},
		{name: "exchange_rates", runtime: []string{"SELECT", "INSERT"}, worker: []string{"SELECT"}},
		{name: "transaction_limits", runtime: []string{"SELECT"}, worker: []string{"SELECT"}},
		{
			name: "transaction_limit_period_usage", worker: []string{"SELECT", "INSERT"},
			workerUpdates: []string{"consumed_amount", "reserved_amount", "updated_at"},
		},
		{
			name: "transaction_limit_reservations", worker: []string{"SELECT", "INSERT"},
			workerUpdates: []string{"consumed_at", "ledger_transaction_id", "released_at", "status"},
		},
		{name: "wallet_audit_log", runtime: []string{"SELECT"}, worker: []string{"INSERT"}},
		{
			name: "funding_sources", runtime: []string{"SELECT"}, worker: []string{"SELECT", "INSERT"},
			workerUpdates: []string{
				"last_funded_at", "last_withdrawn_at", "supports_withdrawal", "total_funded", "total_withdrawn",
				"updated_at", "verification_status", "verified_at", "verified_by", "withdrawal_method",
			},
		},
		{name: "ledger_funding_links", worker: []string{"SELECT", "INSERT"}},
		{
			name: "withdrawal_destinations", runtime: []string{"SELECT", "INSERT"}, worker: []string{"SELECT"},
			runtimeUpdates: []string{"is_active", "updated_at"},
			workerUpdates:  []string{"last_used_at", "total_withdrawn", "updated_at"},
		},
		{name: "ledger_withdrawal_destination_links", worker: []string{"SELECT", "INSERT"}},
	}
	for _, table := range tables {
		assertExactTablePrivileges(t, db, "wallet_ledger_runtime", table.name, table.runtime)
		assertExactUpdateColumns(t, db, "wallet_ledger_runtime", table.name, table.runtimeUpdates)
		assertExactTablePrivileges(t, db, "wallet_ledger_worker", table.name, table.worker)
		assertExactUpdateColumns(t, db, "wallet_ledger_worker", table.name, table.workerUpdates)
		assertExactTablePrivileges(t, db, "wallet_ledger_webhook", table.name, nil)
		assertExactUpdateColumns(t, db, "wallet_ledger_webhook", table.name, nil)
	}

	sequences := []struct {
		name    string
		runtime bool
		worker  bool
	}{
		{name: "operator_identities_id_seq", runtime: true},
		{name: "ledger_transactions_id_seq", worker: true},
		{name: "ledger_entries_id_seq", worker: true},
		{name: "balance_holds_id_seq", worker: true},
		{name: "fee_configs_id_seq", runtime: true},
		{name: "exchange_rates_id_seq", runtime: true},
		{name: "transaction_limits_id_seq"},
		{name: "transaction_limit_reservations_id_seq", worker: true},
		{name: "wallet_audit_log_id_seq", worker: true},
		{name: "funding_sources_id_seq", worker: true},
		{name: "ledger_funding_links_id_seq", worker: true},
		{name: "withdrawal_destinations_id_seq", runtime: true},
		{name: "ledger_withdrawal_destination_links_id_seq", worker: true},
	}
	for _, sequence := range sequences {
		assertExpectedSequenceUsage(t, db, "wallet_ledger_runtime", sequence.name, sequence.runtime)
		assertExpectedSequenceUsage(t, db, "wallet_ledger_worker", sequence.name, sequence.worker)
		assertNoSequencePrivileges(t, db, "wallet_ledger_webhook", sequence.name)
	}
}

func assertWalletPSPPrivileges(t *testing.T, db *DB) {
	t.Helper()
	assertTablePrivileges(t, db, "wallet_ledger_runtime", "psp_configs", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE"})
	assertTablePrivileges(t, db, "wallet_ledger_runtime", "psp_config_overrides", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE"})
	assertTablePrivileges(t, db, "wallet_ledger_runtime", "deposit_intents", []string{"SELECT", "INSERT"}, []string{"DELETE"})
	assertExactUpdateColumns(t, db, "wallet_ledger_runtime", "deposit_intents", []string{"run_id"})
	assertSequenceUsageOnly(t, db, "wallet_ledger_runtime", "deposit_intents_id_seq")
	assertTablePrivileges(t, db, "wallet_ledger_runtime", "psp_transactions", []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE"})
	assertExactUpdateColumns(t, db, "wallet_ledger_runtime", "psp_transactions", nil)
	assertSequenceUsageOnly(t, db, "wallet_ledger_runtime", "psp_transactions_id_seq")

	assertTablePrivileges(t, db, "wallet_ledger_worker", "psp_configs", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE"})
	assertTablePrivileges(t, db, "wallet_ledger_worker", "psp_config_overrides", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE"})
	assertTablePrivileges(t, db, "wallet_ledger_worker", "deposit_intents", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE"})
	assertTablePrivileges(t, db, "wallet_ledger_worker", "psp_transactions", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE"})
	assertExactUpdateColumns(t, db, "wallet_ledger_worker", "psp_transactions", []string{
		"confirmed_at", "last_error_at", "last_error_type", "last_polled_at", "lock_expires_at",
		"lock_token", "next_poll_at", "psp_transaction_id", "raw_response", "response_code",
		"response_message", "retry_count", "status", "workflow_signal_delivered_at", "workflow_signal_payload",
	})
	assertTablePrivileges(t, db, "wallet_ledger_worker", "psp_transaction_amounts", []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE"})
	assertSequenceUsageOnly(t, db, "wallet_ledger_worker", "psp_transaction_amounts_id_seq")
	assertTablePrivileges(t, db, "wallet_ledger_worker", "psp_interactions", []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE"})
	assertSequenceUsageOnly(t, db, "wallet_ledger_worker", "psp_interactions_id_seq")

	webhook := "wallet_ledger_webhook"
	assertTablePrivileges(t, db, webhook, "psp_configs", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE"})
	assertTablePrivileges(t, db, webhook, "psp_config_overrides", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE"})
	assertTablePrivileges(t, db, webhook, "deposit_intents", nil, []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertTablePrivileges(t, db, webhook, "psp_transactions", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE"})
	assertExactUpdateColumns(t, db, webhook, "psp_transactions", []string{
		"confirmed_at", "psp_transaction_id", "raw_response", "response_code", "response_message",
		"status", "workflow_signal_payload",
	})
	assertTablePrivileges(t, db, webhook, "psp_interactions", []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE"})
	assertSequenceUsageOnly(t, db, webhook, "psp_interactions_id_seq")
	for _, table := range []string{"psp_transaction_amounts", "ledger_transactions", "ledger_entries", "balance_holds"} {
		assertTablePrivileges(t, db, webhook, table, nil, []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	}
}

func assertWalletDecisionPrivileges(t *testing.T, db *DB) {
	t.Helper()
	assertTablePrivileges(t, db, "wallet_ledger_runtime", "workflow_decisions", []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertTablePrivileges(t, db, "wallet_ledger_worker", "workflow_decisions", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertTablePrivileges(t, db, "wallet_ledger_webhook", "workflow_decisions", nil, []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
}

func assertWalletImmutableCommandPrivileges(t *testing.T, db *DB) {
	t.Helper()
	assertTablePrivileges(t, db, "wallet_ledger_runtime", "manual_transfers", []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertExactUpdateColumns(t, db, "wallet_ledger_runtime", "manual_transfers", nil)
	assertSequenceUsageOnly(t, db, "wallet_ledger_runtime", "manual_transfers_id_seq")
	assertTablePrivileges(t, db, "wallet_ledger_worker", "manual_transfers", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertExactUpdateColumns(t, db, "wallet_ledger_worker", "manual_transfers", []string{
		"approved_at", "approved_by_operator_id", "completed_at", "proof_of_payment", "rejection_reason", "status",
	})
	assertNoSequencePrivileges(t, db, "wallet_ledger_worker", "manual_transfers_id_seq")

	assertTablePrivileges(t, db, "wallet_ledger_runtime", "manual_transfer_approvals", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertNoSequencePrivileges(t, db, "wallet_ledger_runtime", "manual_transfer_approvals_id_seq")
	assertTablePrivileges(t, db, "wallet_ledger_worker", "manual_transfer_approvals", []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertSequenceUsageOnly(t, db, "wallet_ledger_worker", "manual_transfer_approvals_id_seq")

	assertTablePrivileges(t, db, "wallet_ledger_runtime", "p2p_commands", []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertExactUpdateColumns(t, db, "wallet_ledger_runtime", "p2p_commands", []string{"run_id"})
	assertTablePrivileges(t, db, "wallet_ledger_worker", "p2p_commands", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertExactUpdateColumns(t, db, "wallet_ledger_worker", "p2p_commands", nil)
}

func assertWalletWithdrawalReservationPrivileges(t *testing.T, db *DB) {
	t.Helper()
	const (
		table    = "funding_source_withdrawal_reservations"
		sequence = "funding_source_withdrawal_reservations_id_seq"
	)
	assertTablePrivileges(t, db, "wallet_ledger_runtime", table, nil, []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertExactUpdateColumns(t, db, "wallet_ledger_runtime", table, nil)
	assertNoSequencePrivileges(t, db, "wallet_ledger_runtime", sequence)
	assertTablePrivileges(t, db, "wallet_ledger_worker", table, []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertExactUpdateColumns(t, db, "wallet_ledger_worker", table, []string{"consumed_at", "ledger_entry_id", "released_at", "status"})
	assertSequenceUsageOnly(t, db, "wallet_ledger_worker", sequence)
	assertTablePrivileges(t, db, "wallet_ledger_webhook", table, nil, []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertExactUpdateColumns(t, db, "wallet_ledger_webhook", table, nil)
	assertNoSequencePrivileges(t, db, "wallet_ledger_webhook", sequence)
}

func assertExactUpdateColumns(t *testing.T, db *DB, role, table string, expected []string) {
	t.Helper()
	var actual []string
	if err := db.Select(&actual, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		  AND has_column_privilege($2, 'public.' || table_name, column_name, 'UPDATE')
		ORDER BY column_name
	`, table, role); err != nil {
		t.Fatalf("list %s UPDATE columns on %s: %v", role, table, err)
	}
	if !slices.Equal(actual, expected) {
		t.Fatalf("%s UPDATE columns on %s = %v, want exact %v", role, table, actual, expected)
	}
}

func assertExactTablePrivileges(t *testing.T, db *DB, role, table string, expected []string) {
	t.Helper()
	for _, privilege := range []string{
		"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "SELECT WITH GRANT OPTION",
	} {
		actual := hasTablePrivilege(t, db, role, table, privilege)
		want := slices.Contains(expected, privilege)
		if actual != want {
			t.Fatalf("%s %s on %s = %t, want %t", role, privilege, table, actual, want)
		}
	}
}

func assertTablePrivileges(t *testing.T, db *DB, role, table string, allowed, denied []string) {
	t.Helper()
	for _, privilege := range allowed {
		if !hasTablePrivilege(t, db, role, table, privilege) {
			t.Fatalf("%s lacks %s on %s", role, privilege, table)
		}
	}
	for _, privilege := range denied {
		if hasTablePrivilege(t, db, role, table, privilege) {
			t.Fatalf("%s has %s on %s", role, privilege, table)
		}
	}
}

func hasTablePrivilege(t *testing.T, db *DB, role, table, privilege string) bool {
	t.Helper()
	var result bool
	if err := db.Get(&result, `SELECT has_table_privilege($1, $2, $3)`, role, "public."+table, privilege); err != nil {
		t.Fatalf("check %s %s on %s: %v", role, privilege, table, err)
	}
	return result
}

func hasSequencePrivilege(t *testing.T, db *DB, role, sequence, privilege string) bool {
	t.Helper()
	var result bool
	if err := db.Get(&result, `SELECT has_sequence_privilege($1, $2, $3)`, role, "public."+sequence, privilege); err != nil {
		t.Fatalf("check %s %s on %s: %v", role, privilege, sequence, err)
	}
	return result
}

func assertSequenceUsageOnly(t *testing.T, db *DB, role, sequence string) {
	t.Helper()
	if !hasSequencePrivilege(t, db, role, sequence, "USAGE") {
		t.Fatalf("%s lacks USAGE on %s", role, sequence)
	}
	for _, privilege := range []string{"SELECT", "UPDATE", "USAGE WITH GRANT OPTION"} {
		if hasSequencePrivilege(t, db, role, sequence, privilege) {
			t.Fatalf("%s has %s on sequence %s", role, privilege, sequence)
		}
	}
}

func assertExpectedSequenceUsage(t *testing.T, db *DB, role, sequence string, expected bool) {
	t.Helper()
	if expected {
		assertSequenceUsageOnly(t, db, role, sequence)
		return
	}
	assertNoSequencePrivileges(t, db, role, sequence)
}

func assertNoSequencePrivileges(t *testing.T, db *DB, role, sequence string) {
	t.Helper()
	for _, privilege := range []string{"SELECT", "UPDATE", "USAGE"} {
		if hasSequencePrivilege(t, db, role, sequence, privilege) {
			t.Fatalf("%s has %s on sequence %s", role, privilege, sequence)
		}
	}
}

func hasColumnPrivilege(t *testing.T, db *DB, role, table, column, privilege string) bool {
	t.Helper()
	var result bool
	if err := db.Get(&result, `SELECT has_column_privilege($1, $2, $3, $4)`, role, "public."+table, column, privilege); err != nil {
		t.Fatalf("check %s %s on %s.%s: %v", role, privilege, table, column, err)
	}
	return result
}

func assertNoRoleMembership(t *testing.T, db *DB, role string) {
	t.Helper()
	var count int
	if err := db.Get(&count, `
		SELECT count(*)
		FROM pg_auth_members membership
		JOIN pg_roles member ON member.oid = membership.member
		JOIN pg_roles granted ON granted.oid = membership.roleid
		WHERE member.rolname = $1 OR granted.rolname = $1
	`, role); err != nil {
		t.Fatalf("check %s memberships: %v", role, err)
	}
	if count != 0 {
		t.Fatalf("%s has role membership authority", role)
	}
}

func assertNoPublicFunctionExecution(t *testing.T, db *DB) {
	t.Helper()
	var count int
	if err := db.Get(&count, `
		SELECT count(*)
		FROM pg_proc function
		JOIN pg_namespace schema ON schema.oid = function.pronamespace
		CROSS JOIN LATERAL aclexplode(COALESCE(function.proacl, acldefault('f', function.proowner))) privilege
		WHERE schema.nspname = 'public'
		  AND privilege.grantee = 0
		  AND privilege.privilege_type = 'EXECUTE'
	`); err != nil {
		t.Fatalf("check public function execution: %v", err)
	}
	if count != 0 {
		t.Fatalf("public schema exposes %d functions to PUBLIC", count)
	}
}

func hasSchemaPrivilege(t *testing.T, db *DB, role, schema, privilege string) bool {
	t.Helper()
	var result bool
	if err := db.Get(&result, `SELECT has_schema_privilege($1, $2, $3)`, role, schema, privilege); err != nil {
		t.Fatalf("check %s %s on schema %s: %v", role, privilege, schema, err)
	}
	return result
}
