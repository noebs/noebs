package store

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

var tenantScopedMigrationScopes = []string{
	MigrationScopeIdentityAuth,
	MigrationScopeCardVault,
	MigrationScopeEBSAdapter,
	MigrationScopeAdminReporting,
	MigrationScopeNotificationChat,
	MigrationScopeWalletLedger,
	MigrationScopeGatewayAuth,
}

func TestTenantScopedMigrationsEnforceCatalogAndCompositeForeignKeys(t *testing.T) {
	for _, scope := range tenantScopedMigrationScopes {
		t.Run(scope, func(t *testing.T) {
			db := newMigrationAuthorityDB(t, scope)
			if err := MigrateScope(t.Context(), db, scope); err != nil {
				t.Fatalf("migrate %s: %v", scope, err)
			}

			var tenantTables []string
			if err := db.SelectContext(t.Context(), &tenantTables, `
				SELECT DISTINCT relation.relname
				FROM pg_class relation
				JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
				JOIN pg_attribute column_definition ON column_definition.attrelid = relation.oid
				WHERE namespace.nspname = current_schema()
				  AND relation.relkind IN ('r', 'p')
				  AND column_definition.attname = 'tenant_id'
				  AND NOT column_definition.attisdropped
				ORDER BY relation.relname
			`); err != nil {
				t.Fatalf("list tenant tables: %v", err)
			}
			if len(tenantTables) == 0 {
				t.Fatal("tenant scope has no tenant-bearing tables")
			}
			for _, table := range tenantTables {
				var protected bool
				if err := db.GetContext(t.Context(), &protected, `
					SELECT EXISTS (
						SELECT 1
						FROM pg_constraint foreign_key
						JOIN pg_class child ON child.oid = foreign_key.conrelid
						JOIN pg_class parent ON parent.oid = foreign_key.confrelid
						JOIN pg_namespace namespace ON namespace.oid = child.relnamespace
						WHERE namespace.nspname = current_schema()
						  AND child.relname = $1
						  AND parent.relname = 'tenants'
						  AND foreign_key.contype = 'f'
						  AND array_length(foreign_key.conkey, 1) = 1
						  AND array_length(foreign_key.confkey, 1) = 1
						  AND (SELECT attname FROM pg_attribute WHERE attrelid = child.oid AND attnum = foreign_key.conkey[1]) = 'tenant_id'
						  AND (SELECT attname FROM pg_attribute WHERE attrelid = parent.oid AND attnum = foreign_key.confkey[1]) = 'id'
					)
				`, table); err != nil {
					t.Fatalf("inspect %s tenant foreign key: %v", table, err)
				}
				if !protected {
					t.Fatalf("%s.%s does not reference tenants(id)", scope, table)
				}
			}

			var unsafeForeignKeys []string
			if err := db.SelectContext(t.Context(), &unsafeForeignKeys, `
				SELECT child.relname || '.' || foreign_key.conname || ' -> ' || parent.relname
				FROM pg_constraint foreign_key
				JOIN pg_class child ON child.oid = foreign_key.conrelid
				JOIN pg_class parent ON parent.oid = foreign_key.confrelid
				JOIN pg_namespace namespace ON namespace.oid = child.relnamespace
				WHERE namespace.nspname = current_schema()
				  AND foreign_key.contype = 'f'
				  AND EXISTS (
					SELECT 1 FROM pg_attribute
					WHERE attrelid = child.oid AND attname = 'tenant_id' AND NOT attisdropped
				  )
				  AND EXISTS (
					SELECT 1 FROM pg_attribute
					WHERE attrelid = parent.oid AND attname = 'tenant_id' AND NOT attisdropped
				  )
				  AND NOT EXISTS (
					SELECT 1
					FROM unnest(foreign_key.conkey) WITH ORDINALITY child_key(attnum, position)
					JOIN unnest(foreign_key.confkey) WITH ORDINALITY parent_key(attnum, position)
					  USING (position)
					JOIN pg_attribute child_column
					  ON child_column.attrelid = child.oid AND child_column.attnum = child_key.attnum
					JOIN pg_attribute parent_column
					  ON parent_column.attrelid = parent.oid AND parent_column.attnum = parent_key.attnum
					WHERE child_column.attname = 'tenant_id' AND parent_column.attname = 'tenant_id'
				  )
				ORDER BY 1
			`); err != nil {
				t.Fatalf("inspect tenant relations: %v", err)
			}
			if len(unsafeForeignKeys) != 0 {
				t.Fatalf("tenant-scoped foreign keys omit tenant binding: %v", unsafeForeignKeys)
			}

			assertCanonicalTenantTable(t, db)
		})
	}
}

func TestApplicationRolesCannotMutateTenantCatalogs(t *testing.T) {
	expectedReaders := map[string][]string{
		MigrationScopeIdentityAuth:     {"identity_auth_runtime"},
		MigrationScopeCardVault:        {"card_vault_runtime"},
		MigrationScopeEBSAdapter:       {"ebs_adapter_events", "ebs_adapter_runtime"},
		MigrationScopeAdminReporting:   {"admin_reporting_projector", "admin_reporting_runtime"},
		MigrationScopeNotificationChat: {"notification_chat_runtime"},
		MigrationScopeWalletLedger:     {"wallet_ledger_runtime", "wallet_ledger_webhook", "wallet_ledger_worker"},
		MigrationScopeGatewayAuth:      {"gateway_auth_runtime"},
	}
	for _, scope := range tenantScopedMigrationScopes {
		t.Run(scope, func(t *testing.T) {
			db := newMigrationAuthorityDB(t, scope)
			if err := MigrateScope(t.Context(), db, scope); err != nil {
				t.Fatalf("migrate %s: %v", scope, err)
			}
			if _, err := db.ExecContext(t.Context(), `INSERT INTO tenants(id, name, created_at) VALUES ('tenant-authority', 'Tenant Authority', NOW())`); err != nil {
				t.Fatalf("provision tenant: %v", err)
			}
			readers := expectedReaders[scope]
			slices.Sort(readers)
			for _, role := range readers {
				role := role
				t.Run(role, func(t *testing.T) {
					runtime := openMigrationAuthorityRoleDB(t, migrationAuthorityContracts[scope].database, role)
					defer func() { _ = runtime.Close() }()
					var name string
					if err := runtime.GetContext(t.Context(), &name, `SELECT name FROM tenants WHERE id = 'tenant-authority'`); err != nil || name != "Tenant Authority" {
						t.Fatalf("read tenant catalog: name=%q err=%v", name, err)
					}
					_, err := runtime.ExecContext(t.Context(), `INSERT INTO tenants(id, name, created_at) VALUES ('tenant-rogue', 'Tenant Rogue', NOW())`)
					assertInsufficientPrivilege(t, err)
					_, err = runtime.ExecContext(t.Context(), `UPDATE tenants SET name = 'Mutated' WHERE id = 'tenant-authority'`)
					assertInsufficientPrivilege(t, err)
					_, err = runtime.ExecContext(t.Context(), `DELETE FROM tenants WHERE id = 'tenant-authority'`)
					assertInsufficientPrivilege(t, err)
				})
			}
		})
	}
}

func TestWalletCommandAuthorityCannotRewriteImmutableBindings(t *testing.T) {
	db := newMigrationAuthorityDB(t, MigrationScopeWalletLedger)
	if err := MigrateScope(t.Context(), db, MigrationScopeWalletLedger); err != nil {
		t.Fatal(err)
	}
	assertTablePrivileges(t, db, "wallet_ledger_runtime", "p2p_commands", []string{"SELECT", "INSERT"}, []string{"UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertExactUpdateColumns(t, db, "wallet_ledger_runtime", "p2p_commands", []string{"run_id"})
	assertTablePrivileges(t, db, "wallet_ledger_worker", "p2p_commands", []string{"SELECT"}, []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})
	assertExactUpdateColumns(t, db, "wallet_ledger_worker", "p2p_commands", nil)
	assertTablePrivileges(t, db, "wallet_ledger_webhook", "p2p_commands", nil, []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"})

	for _, role := range []string{"wallet_ledger_runtime", "wallet_ledger_worker", "wallet_ledger_webhook"} {
		for _, column := range []string{
			"wallet_id", "owner_type", "owner_id", "withdrawal_destination_id",
			"allow_return_to_source", "approval_timeout_seconds", "decision_deadline_at", "raw_request",
		} {
			if hasColumnPrivilege(t, db, role, "psp_transactions", column, "UPDATE") {
				t.Fatalf("%s can rewrite immutable psp_transactions.%s", role, column)
			}
		}
	}
}

func TestWalletPSPConfigRequiresDispatchIdempotencyHeader(t *testing.T) {
	db := migratedTenantAttackDB(t, MigrationScopeWalletLedger)
	for _, test := range []struct {
		name   string
		header any
		code   string
	}{
		{name: "null", header: nil, code: "23502"},
		{name: "blank", header: " ", code: "23514"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := db.ExecContext(t.Context(), `INSERT INTO psp_configs(
				tenant_id, provider_code, provider_name, api_base_url, idempotency_header_name, deposit_response_mapping
			) VALUES ('tenant-a', $1, 'Unsafe PSP', 'https://psp.invalid', $2, '{}')`, "unsafe-"+test.name, test.header)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != test.code {
				t.Fatalf("config insert error = %v, want SQLSTATE %s", err, test.code)
			}
		})
	}
}

func TestTenantScopedTablesRejectUnknownTenants(t *testing.T) {
	attacks := map[string]string{
		MigrationScopeIdentityAuth: `INSERT INTO users(
			tenant_id, issuer, subject, fullname, created_at, updated_at
		) VALUES ('tenant-missing', 'https://issuer.example', 'subject', 'Missing Tenant', NOW(), NOW())`,
		MigrationScopeCardVault: `INSERT INTO cards(
			tenant_id, card_id, user_id, pan_fingerprint, pan_ciphertext, pan_key_version,
			masked_pan, expiry, status, verification_method, verified_at, created_at, updated_at
		) VALUES (
			'tenant-missing', '10000000-0000-0000-0000-000000000001', 1, 'fingerprint',
			'ciphertext', 1, '****1234', '1228', 'active', 'rail', NOW(), NOW(), NOW()
		)`,
		MigrationScopeEBSAdapter: `INSERT INTO cache_billers(
			tenant_id, mobile, biller_id, created_at, updated_at
		) VALUES ('tenant-missing', '249000000000', 'biller', NOW(), NOW())`,
		MigrationScopeAdminReporting: `INSERT INTO merchant_issues(
			tenant_id, terminal_id, reported_at, created_at
		) VALUES ('tenant-missing', 'terminal', NOW(), NOW())`,
		MigrationScopeNotificationChat: `INSERT INTO contacts(
			tenant_id, owner_user_id, contact_user_id
		) VALUES ('tenant-missing', 1, 2)`,
		MigrationScopeWalletLedger: `INSERT INTO wallets(
			tenant_id, owner_type, owner_id, currency, currency_unit_version_id, kyc_tier
		) VALUES (
			'tenant-missing', 'user', 'subject', 'AED',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'AED' AND valid_to IS NULL),
			'unverified'
		)`,
		MigrationScopeGatewayAuth: `INSERT INTO wallet_transaction_authorization_intents(
			intent_hash, browser_start_hash, tenant_id, issuer, subject, operation, request_digest,
			idempotency_key, created_at, expires_at
		) VALUES (
			decode(repeat('01', 32), 'hex'), decode(repeat('02', 32), 'hex'), 'tenant-missing',
			'https://issuer.example', 'subject', 'wallet.p2p', decode(repeat('03', 32), 'hex'),
			'idem', NOW(), NOW() + interval '5 minutes'
		)`,
	}
	for _, scope := range tenantScopedMigrationScopes {
		t.Run(scope, func(t *testing.T) {
			db := newMigrationAuthorityDB(t, scope)
			if err := MigrateScope(t.Context(), db, scope); err != nil {
				t.Fatal(err)
			}
			_, err := db.ExecContext(t.Context(), attacks[scope])
			assertForeignKeyViolation(t, err)
		})
	}
}

func TestCompositeTenantForeignKeysRejectCrossTenantParents(t *testing.T) {
	t.Run("identity user projection", func(t *testing.T) {
		db := migratedTenantAttackDB(t, MigrationScopeIdentityAuth)
		var userID int64
		if err := db.GetContext(t.Context(), &userID, `INSERT INTO users(
			tenant_id, issuer, subject, fullname, created_at, updated_at
		) VALUES ('tenant-a', 'https://issuer.example', 'subject-a', 'Subject A', NOW(), NOW()) RETURNING id`); err != nil {
			t.Fatal(err)
		}
		_, err := db.ExecContext(t.Context(), `INSERT INTO kyc(tenant_id, user_id, created_at, updated_at)
			VALUES ('tenant-b', $1, NOW(), NOW())`, userID)
		assertForeignKeyViolation(t, err)
	})

	t.Run("card ownership", func(t *testing.T) {
		db := migratedTenantAttackDB(t, MigrationScopeCardVault)
		const cardID = "10000000-0000-0000-0000-000000000001"
		if _, err := db.ExecContext(t.Context(), `INSERT INTO cards(
			tenant_id, card_id, user_id, pan_fingerprint, pan_ciphertext, pan_key_version,
			masked_pan, expiry, status, verification_method, verified_at, created_at, updated_at
		) VALUES (
			'tenant-a', $1, 1, 'fingerprint-a', 'ciphertext', 1,
			'****1234', '1228', 'active', 'rail', NOW(), NOW(), NOW()
		)`, cardID); err != nil {
			t.Fatal(err)
		}
		_, err := db.ExecContext(t.Context(), `INSERT INTO card_funded_operation_claims(
			tenant_id, rail_uuid, user_id, card_id, purpose, body_claim, rail_tran_date_time, claimed_at
		) VALUES (
			'tenant-b', '20000000-0000-0000-0000-000000000001', 1, $1,
			'purchase', 'v1:' || repeat('a', 64), '260720154700', NOW()
		)`, cardID)
		assertForeignKeyViolation(t, err)
	})

	t.Run("EBS transaction event", func(t *testing.T) {
		db := migratedTenantAttackDB(t, MigrationScopeEBSAdapter)
		var transactionID int64
		if err := db.GetContext(t.Context(), &transactionID, `INSERT INTO transactions(
			tenant_id, created_at, updated_at
		) VALUES ('tenant-a', NOW(), NOW()) RETURNING id`); err != nil {
			t.Fatal(err)
		}
		_, err := db.ExecContext(t.Context(), `INSERT INTO transaction_events(
			transaction_id, tenant_id, topic, event_key, event_type, payload, created_at, updated_at
		) VALUES ($1, 'tenant-b', 'topic', 'key', 'created', '{}', NOW(), NOW())`, transactionID)
		assertForeignKeyViolation(t, err)
	})

	t.Run("wallet command and withdrawal", func(t *testing.T) {
		db := migratedTenantAttackDB(t, MigrationScopeWalletLedger)
		const (
			walletA = "30000000-0000-0000-0000-000000000001"
			walletB = "30000000-0000-0000-0000-000000000002"
		)
		if _, err := db.ExecContext(t.Context(), `INSERT INTO wallets(
			id, tenant_id, owner_type, owner_id, currency, currency_unit_version_id, kyc_tier
		) VALUES
			(
				$1, 'tenant-a', 'user', 'subject-a', 'AED',
				(SELECT id FROM currency_unit_versions WHERE currency_code = 'AED' AND valid_to IS NULL),
				'unverified'
			),
			(
				$2, 'tenant-b', 'user', 'subject-b', 'AED',
				(SELECT id FROM currency_unit_versions WHERE currency_code = 'AED' AND valid_to IS NULL),
				'unverified'
			)`, walletA, walletB); err != nil {
			t.Fatal(err)
		}
		_, err := db.ExecContext(t.Context(), `INSERT INTO p2p_commands(
			tenant_id, idempotency_key, workflow_id, from_wallet_id, to_wallet_id,
			from_owner_type, from_owner_id, to_owner_type, to_owner_id, command
		) VALUES (
			'tenant-b', 'cross-p2p', 'cross-p2p', $1, $2,
			'user', 'subject-a', 'user', 'subject-b', '{}'
		)`, walletA, walletB)
		assertForeignKeyViolation(t, err)

		if _, err := db.ExecContext(t.Context(), `INSERT INTO psp_configs(
			tenant_id, provider_code, provider_name, api_base_url, idempotency_header_name, deposit_response_mapping
		) VALUES ('tenant-b', 'provider-b', 'Provider B', 'https://provider.example', 'Idempotency-Key', '{}')`); err != nil {
			t.Fatal(err)
		}
		_, err = db.ExecContext(t.Context(), `INSERT INTO psp_transactions(
			tenant_id, psp_provider, idempotency_key, client_reference, direction, amount, currency,
			currency_unit_version_id, status, wallet_id, owner_type, owner_id, allow_return_to_source
		) VALUES (
			'tenant-b', 'provider-b', 'cross-withdrawal', 'cross-withdrawal', 'outbound', 100, 'AED',
			(SELECT id FROM currency_unit_versions WHERE currency_code = 'AED' AND valid_to IS NULL),
			'initiated', $1, 'user', 'subject-a', TRUE
		)`, walletA)
		assertForeignKeyViolation(t, err)
	})
}

func migratedTenantAttackDB(t *testing.T, scope string) *DB {
	t.Helper()
	db := newMigrationAuthorityDB(t, scope)
	if err := MigrateScope(t.Context(), db, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO tenants(id, name, created_at) VALUES
		('tenant-a', 'Tenant A', NOW()), ('tenant-b', 'Tenant B', NOW())`); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertCanonicalTenantTable(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO tenants(id, name, created_at) VALUES ('tenant-valid', 'Tenant Valid', NOW())`); err != nil {
		t.Fatalf("insert canonical tenant: %v", err)
	}
	invalid := []struct {
		id   string
		name string
	}{
		{id: "default", name: "Reserved"},
		{id: "Tenant-Upper", name: "Upper"},
		{id: "tenant_with_underscore", name: "Underscore"},
		{id: "tenant--gap", name: "Gap"},
		{id: "1tenant", name: "Leading Digit"},
		{id: strings.Repeat("a", 64), name: "Too Long"},
		{id: "tenant-empty-name", name: ""},
		{id: "tenant-padded-name", name: " Padded "},
	}
	for _, tenant := range invalid {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO tenants(id, name, created_at) VALUES ($1, $2, NOW())`, tenant.id, tenant.name); err == nil {
			t.Fatalf("tenant catalog accepted id=%q name=%q", tenant.id, tenant.name)
		}
	}
}

func assertInsufficientPrivilege(t *testing.T, err error) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
		t.Fatalf("error = %v, want PostgreSQL insufficient_privilege", err)
	}
}

func assertForeignKeyViolation(t *testing.T, err error) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23503" {
		t.Fatalf("error = %v, want PostgreSQL foreign_key_violation", err)
	}
}
