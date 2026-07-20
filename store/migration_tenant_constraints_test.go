package store

import (
	"context"
	"testing"
)

func TestTenantConstraintsBelongToTheirMigrationScope(t *testing.T) {
	tests := []struct {
		name   string
		scope  string
		tables []string
	}{
		{
			name:  "wallet ledger",
			scope: MigrationScopeWalletLedger,
			tables: []string{
				"tenants",
				"wallets",
				"ledger_transactions",
				"ledger_entries",
				"balance_holds",
				"fee_configs",
				"exchange_rates",
				"transaction_limits",
				"manual_transfers",
				"manual_transfer_approvals",
				"wallet_audit_log",
				"funding_sources",
				"ledger_funding_links",
				"withdrawal_destinations",
				"ownership_verifications",
				"ledger_withdrawal_destination_links",
			},
		},
		{
			name:  "psp webhook",
			scope: MigrationScopePSPWebhook,
			tables: []string{
				"tenants",
				"psp_configs",
				"psp_transactions",
				"psp_transaction_amounts",
				"psp_config_overrides",
				"psp_interactions",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := newValidationDB(t)
			if err := MigrateScope(ctx, db, test.scope); err != nil {
				t.Fatalf("migrate %s: %v", test.scope, err)
			}
			for _, table := range test.tables {
				var exists bool
				if err := db.QueryRowContext(ctx, `SELECT EXISTS (
					SELECT 1
					FROM pg_constraint AS tenant_constraint
					JOIN pg_class AS relation ON relation.oid = tenant_constraint.conrelid
					JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
					WHERE namespace.nspname = current_schema()
						AND relation.relname = $1
						AND tenant_constraint.conname = 'tenant_id_not_reserved'
						AND tenant_constraint.contype = 'c'
				)`, table).Scan(&exists); err != nil {
					t.Fatalf("inspect %s constraint: %v", table, err)
				}
				if !exists {
					t.Fatalf("%s has no tenant_id_not_reserved check constraint", table)
				}
			}
		})
	}
}
