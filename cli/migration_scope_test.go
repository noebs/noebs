package main

import (
	"context"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/store"
)

func TestServiceMigrationRolesRunOwnedScopes(t *testing.T) {
	if testPostgres == nil {
		t.Skip("PostgreSQL test environment unavailable")
	}
	catalog, err := tenantcatalog.LoadFile("../deploy/kubernetes/keycloak-authority/tenant-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	roles := map[serviceRole][]string{
		serviceRoleIdentityAuthMigrate: {
			"tenants",
			"users",
			"kyc",
			"passports",
		},
		serviceRoleCardVaultMigrate: {
			"tenants",
			"cards",
			"card_enrollment_intents",
			"card_funded_operation_claims",
		},
		serviceRoleEBSAdapterMigrate: {
			"tenants",
			"cache_billers",
			"transactions",
			"transaction_events",
			"meter_names",
		},
		serviceRoleAdminReportingMigrate: {
			"tenants",
			"transactions",
			"merchant_issues",
		},
		serviceRoleNotificationMigrate: {
			"tenants",
			"chats",
			"contacts",
			"push_data",
		},
		serviceRoleWalletLedgerMigrate: {
			"tenants",
			"wallets",
			"ledger_transactions",
			"ledger_entries",
			"balance_holds",
			"fee_configs",
			"funding_sources",
			"funding_source_withdrawal_reservations",
			"psp_configs",
			"psp_transactions",
			"psp_transaction_amounts",
			"psp_config_overrides",
			"psp_interactions",
		},
		serviceRoleGatewayAuthMigrate: {
			"tenants",
			"backoffice_auth_flows",
			"backoffice_sessions",
			"wallet_transaction_authorization_intents",
			"wallet_transaction_authorization_flows",
		},
	}
	forbiddenTables := map[serviceRole][]string{
		serviceRoleIdentityAuthMigrate: {
			"auth_accounts",
			"api_keys",
			"login_metrics",
			"auth_rate_limits",
			"otp_challenges",
			"used_refresh_tokens",
			"password_recovery_credentials",
		},
	}

	for role, tables := range roles {
		role := role
		tables := tables
		forbidden := forbiddenTables[role]
		t.Run(string(role), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()

			spec, ok := postgresRoleSpecForService(role)
			if !ok {
				t.Fatalf("%s has no Postgres role spec", role)
			}
			dbName := spec.database
			var dbURL string
			if dbName == testDBName {
				dbURL, err = testPostgres.DatabaseURLForRole(dbName, spec.username)
			} else {
				dbURL, err = testPostgres.CreateDatabaseForRole(ctx, dbName, spec.username)
			}
			if err != nil {
				t.Fatalf("create database: %v", err)
			}
			if dbName != testDBName {
				t.Cleanup(func() {
					cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cleanupCancel()
					_ = testPostgres.DropDatabase(cleanupCtx, dbName)
				})
			}

			db, err := store.OpenFromConfig(dbURL, store.DriverPostgres)
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })

			scope, ok := role.migrationScope()
			if !ok {
				t.Fatalf("%s has no migration scope", role)
			}
			if err := store.MigrateScope(ctx, db, scope); err != nil {
				t.Fatalf("MigrateScope() error = %v", err)
			}
			databaseCatalog := catalog
			if dbName == testDBName {
				databaseCatalog = runtimeTenantCatalog
			}
			if err := store.New(db).ProvisionTenantCatalog(ctx, databaseCatalog); err != nil {
				t.Fatalf("ProvisionTenantCatalog() error = %v", err)
			}
			assertProvisionedTenantCatalog(t, ctx, db, databaseCatalog)
			for _, table := range tables {
				if !postgresTableExists(t, ctx, db, table) {
					t.Fatalf("expected table %s", table)
				}
			}
			for _, table := range forbidden {
				if postgresTableExists(t, ctx, db, table) {
					t.Fatalf("forbidden table %s exists in %s migration scope", table, role)
				}
			}
			if role == serviceRoleWalletLedgerMigrate {
				assertNoColumnDefault(t, ctx, db, "wallets", "owner_type")
				assertNoColumnDefault(t, ctx, db, "wallets", "currency")
				assertNoColumnDefault(t, ctx, db, "wallets", "kyc_tier")
				assertNoColumnDefault(t, ctx, db, "fee_configs", "currency")
				assertNoColumnDefault(t, ctx, db, "transaction_limits", "currency")
			}
		})
	}
}

func assertProvisionedTenantCatalog(t *testing.T, ctx context.Context, db *store.DB, catalog tenantcatalog.Catalog) {
	t.Helper()
	var rows []struct {
		ID   string `db:"id"`
		Name string `db:"name"`
	}
	if err := db.SelectContext(ctx, &rows, "SELECT id, name FROM tenants ORDER BY id"); err != nil {
		t.Fatal(err)
	}
	want := catalog.All()
	if len(rows) != len(want) {
		t.Fatalf("tenant rows = %#v, want %#v", rows, want)
	}
	for index, tenant := range want {
		if rows[index].ID != string(tenant.ID) || rows[index].Name != tenant.Name {
			t.Fatalf("tenant rows = %#v, want %#v", rows, want)
		}
	}
}

func assertNoColumnDefault(t *testing.T, ctx context.Context, db *store.DB, tableName, columnName string) {
	t.Helper()
	var defaultValue *string
	if err := db.DB.QueryRowContext(ctx, `
		SELECT column_default
		FROM information_schema.columns
		WHERE table_schema = current_schema()
			AND table_name = $1
			AND column_name = $2
	`, tableName, columnName).Scan(&defaultValue); err != nil {
		t.Fatalf("read %s.%s default: %v", tableName, columnName, err)
	}
	if defaultValue != nil {
		t.Fatalf("%s.%s default = %q, want no DB-layer currency default", tableName, columnName, *defaultValue)
	}
}

func postgresTableExists(t *testing.T, ctx context.Context, db *store.DB, table string) bool {
	t.Helper()
	var exists bool
	if err := db.DB.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = $1
	)`, table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return exists
}
