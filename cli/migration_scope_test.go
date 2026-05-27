package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adonese/noebs/store"
)

func TestServiceMigrationRolesRunOwnedScopes(t *testing.T) {
	if testPostgres == nil {
		t.Skip("postgres testcontainer unavailable")
	}
	roles := map[serviceRole][]string{
		serviceRoleIdentityAuthMigrate: {
			"tenants",
			"users",
			"auth_accounts",
			"api_keys",
			"login_metrics",
			"kyc",
			"passports",
		},
		serviceRoleCardVaultMigrate: {
			"tenants",
			"cards",
			"cache_cards",
			"tokens",
		},
		serviceRoleEBSAdapterMigrate: {
			"tenants",
			"cache_billers",
			"transactions",
			"meter_names",
		},
		serviceRolePSPWebhookMigrate: {
			"tenants",
			"psp_configs",
			"psp_transactions",
			"psp_config_overrides",
			"psp_interactions",
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
		serviceRoleBeneficiaryMigrate: {
			"tenants",
			"beneficiaries",
		},
		serviceRoleWalletLedgerMigrate: {
			"tenants",
			"wallets",
			"ledger_transactions",
			"ledger_entries",
			"balance_holds",
			"fee_configs",
			"funding_sources",
			"wallet_user_2fa",
		},
	}
	forbiddenTables := map[serviceRole][]string{
		serviceRoleCardVaultMigrate: {
			"push_data",
		},
	}

	for role, tables := range roles {
		role := role
		tables := tables
		forbidden := forbiddenTables[role]
		t.Run(string(role), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			dbName := fmt.Sprintf("noebs_%s_%d", strings.ReplaceAll(string(role), "-", "_"), time.Now().UnixNano())
			dbURL, err := testPostgres.CreateDatabase(ctx, dbName)
			if err != nil {
				t.Fatalf("create database: %v", err)
			}
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				_ = testPostgres.DropDatabase(cleanupCtx, dbName)
			})

			db, err := store.OpenFromConfig(dbURL, store.DriverPostgres)
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })

			scope, ok := role.migrationScope()
			if !ok {
				t.Fatalf("%s has no migration scope", role)
			}
			if err := store.MigrateScope(ctx, db, "test-tenant", scope); err != nil {
				t.Fatalf("MigrateScope() error = %v", err)
			}
			if err := store.New(db).EnsureTenant(ctx, "test-tenant"); err != nil {
				t.Fatalf("EnsureTenant() error = %v", err)
			}
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
		})
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
