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
				"funding_source_withdrawal_reservations",
				"ledger_funding_links",
				"withdrawal_destinations",
				"workflow_decisions",
				"ledger_withdrawal_destination_links",
				"p2p_commands",
				"psp_configs",
				"deposit_intents",
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
			db := newMigrationAuthorityDB(t, test.scope)
			if err := MigrateScope(ctx, db, test.scope); err != nil {
				t.Fatalf("migrate %s: %v", test.scope, err)
			}
			for _, table := range test.tables {
				constraintName := "tenant_id_not_reserved"
				if table == "tenants" {
					constraintName = "tenant_id_canonical"
				}
				var exists bool
				if err := db.QueryRowContext(ctx, `SELECT EXISTS (
					SELECT 1
					FROM pg_constraint AS tenant_constraint
					JOIN pg_class AS relation ON relation.oid = tenant_constraint.conrelid
					JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
					WHERE namespace.nspname = current_schema()
						AND relation.relname = $1
						AND tenant_constraint.conname = $2
						AND tenant_constraint.contype = 'c'
				)`, table, constraintName).Scan(&exists); err != nil {
					t.Fatalf("inspect %s constraint: %v", table, err)
				}
				if !exists {
					t.Fatalf("%s has no %s check constraint", table, constraintName)
				}
			}
		})
	}
}
