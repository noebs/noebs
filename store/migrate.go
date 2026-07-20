package store

import (
	"context"
	"embed"
	"fmt"
	"strings"
	"sync"

	"github.com/adonese/noebs/internal/postgresauthority"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/postgres/*/*.sql
var postgresMigrations embed.FS

var gooseMigrationMu sync.Mutex

const (
	MigrationScopeIdentityAuth     = "identity-auth"
	MigrationScopeCardVault        = "card-vault"
	MigrationScopeEBSAdapter       = "ebs-adapter"
	MigrationScopeAdminReporting   = "admin-reporting"
	MigrationScopeNotificationChat = "notification-chat"
	MigrationScopeWalletLedger     = "wallet-ledger"
	MigrationScopeWorkloadAuth     = "workload-auth"
	MigrationScopeGatewayAuth      = "gateway-auth"
)

type migrationAuthorityContract struct {
	database      string
	migrationPath string
	versionTable  string
	schemaRoles   []string
	broadDMLRoles []string
	sequenceRoles []string
	specialGrants []string
}

var migrationAuthorityContracts = map[string]migrationAuthorityContract{
	MigrationScopeIdentityAuth: {
		database:      "identity_auth",
		migrationPath: "migrations/postgres/identity_auth",
		versionTable:  "goose_db_version_identity_auth",
		schemaRoles:   []string{"identity_auth_runtime"},
		broadDMLRoles: []string{"identity_auth_runtime"},
		sequenceRoles: []string{"identity_auth_runtime"},
	},
	MigrationScopeCardVault: {
		database:      "card_vault",
		migrationPath: "migrations/postgres/card_vault",
		versionTable:  "goose_db_version_card_vault",
		schemaRoles:   []string{"card_vault_runtime"},
		broadDMLRoles: []string{"card_vault_runtime"},
		sequenceRoles: []string{"card_vault_runtime"},
	},
	MigrationScopeEBSAdapter: {
		database:      "ebs_adapter",
		migrationPath: "migrations/postgres/ebs_adapter",
		versionTable:  "goose_db_version_ebs_adapter",
		schemaRoles:   []string{"ebs_adapter_runtime", "ebs_adapter_events"},
		broadDMLRoles: []string{"ebs_adapter_runtime"},
		sequenceRoles: []string{"ebs_adapter_runtime"},
		specialGrants: []string{
			`GRANT SELECT ON TABLE public.tenants TO ebs_adapter_events`,
			`GRANT SELECT ON TABLE public.transaction_events TO ebs_adapter_events`,
			`GRANT UPDATE (publish_attempts, updated_at, published_at, last_error) ON TABLE public.transaction_events TO ebs_adapter_events`,
		},
	},
	MigrationScopeAdminReporting: {
		database:      "admin_reporting",
		migrationPath: "migrations/postgres/admin_reporting",
		versionTable:  "goose_db_version_admin_reporting",
		schemaRoles:   []string{"admin_reporting_runtime", "admin_reporting_projector"},
		broadDMLRoles: []string{"admin_reporting_runtime"},
		sequenceRoles: []string{"admin_reporting_runtime"},
		specialGrants: []string{
			`GRANT SELECT ON TABLE public.tenants TO admin_reporting_projector`,
			`GRANT SELECT, INSERT ON TABLE public.transactions TO admin_reporting_projector`,
			`GRANT USAGE ON SEQUENCE public.transactions_id_seq TO admin_reporting_projector`,
		},
	},
	MigrationScopeNotificationChat: {
		database:      "notification_chat",
		migrationPath: "migrations/postgres/notification_chat",
		versionTable:  "goose_db_version_notification_chat",
		schemaRoles:   []string{"notification_chat_runtime"},
		broadDMLRoles: []string{"notification_chat_runtime"},
		sequenceRoles: []string{"notification_chat_runtime"},
	},
	MigrationScopeWalletLedger: {
		database:      "wallet_ledger",
		migrationPath: "migrations/postgres/wallet_ledger",
		versionTable:  "goose_db_version_wallet_ledger",
		schemaRoles: []string{
			"wallet_ledger_runtime",
			"wallet_ledger_worker",
			"wallet_ledger_webhook",
		},
		specialGrants: walletAuthorityGrants(),
	},
	MigrationScopeWorkloadAuth: {
		database:      "workload_auth",
		migrationPath: "migrations/postgres/workload_auth",
		versionTable:  "goose_db_version_workload_auth",
		schemaRoles:   []string{"workload_auth_runtime", "workload_auth_cleanup"},
		specialGrants: []string{
			`GRANT INSERT ON TABLE public.workload_request_nonces TO workload_auth_runtime`,
			`GRANT SELECT (expires_at), DELETE ON TABLE public.workload_request_nonces TO workload_auth_cleanup`,
		},
	},
	MigrationScopeGatewayAuth: {
		database:      "gateway_auth",
		migrationPath: "migrations/postgres/gateway_auth",
		versionTable:  "goose_db_version_gateway_auth",
		schemaRoles:   []string{"gateway_auth_runtime", "gateway_auth_cleanup"},
		specialGrants: []string{
			`GRANT SELECT ON TABLE public.tenants TO gateway_auth_runtime`,
			`GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.backoffice_auth_flows, public.backoffice_sessions, public.wallet_transaction_authorization_intents, public.wallet_transaction_authorization_flows TO gateway_auth_runtime`,
			`GRANT SELECT (expires_at), DELETE ON TABLE public.backoffice_auth_flows TO gateway_auth_cleanup`,
			`GRANT SELECT (refresh_expires_at, idle_expires_at, absolute_expires_at), DELETE ON TABLE public.backoffice_sessions TO gateway_auth_cleanup`,
			`GRANT SELECT (expires_at), DELETE ON TABLE public.wallet_transaction_authorization_intents TO gateway_auth_cleanup`,
		},
	},
}

var migrationScopePaths = map[string]string{}
var migrationScopeTableNames = map[string]string{}
var migrationScopeApplicationRoles = map[string][]string{}

func init() {
	for scope, contract := range migrationAuthorityContracts {
		migrationScopePaths[scope] = contract.migrationPath
		migrationScopeTableNames[scope] = contract.versionTable
		migrationScopeApplicationRoles[scope] = append([]string(nil), contract.schemaRoles...)
	}
}

// MigrateScope applies one database's embedded migrations, then atomically
// replaces execution-role authority with that database's exact contract.
func MigrateScope(ctx context.Context, db *DB, scope string) error {
	if db == nil || db.DB == nil {
		return fmt.Errorf("db is nil")
	}
	contract, ok := migrationAuthorityContracts[scope]
	if !ok {
		return fmt.Errorf("unknown migration scope %q", scope)
	}
	if db.Driver != DriverPostgres {
		return fmt.Errorf("unsupported migration driver %q (postgres only)", db.Driver)
	}
	migrationRole, ok := postgresauthority.MigrationRole(contract.database)
	if !ok {
		return fmt.Errorf("migration authority is missing for database %q", contract.database)
	}
	if err := requireMigrationAuthority(ctx, db, contract.database, migrationRole.Name); err != nil {
		return err
	}
	if err := runGooseMigrations(ctx, db, contract); err != nil {
		return err
	}
	if err := convergeMigrationAuthority(ctx, db, scope); err != nil {
		return fmt.Errorf("converge %s migration authority: %w", scope, err)
	}
	return nil
}

func runGooseMigrations(ctx context.Context, db *DB, contract migrationAuthorityContract) error {
	gooseMigrationMu.Lock()
	defer gooseMigrationMu.Unlock()
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	goose.SetTableName(contract.versionTable)
	goose.SetBaseFS(postgresMigrations)
	return goose.UpContext(ctx, db.DB.DB, contract.migrationPath)
}

func requireMigrationAuthority(ctx context.Context, db *DB, expectedDatabase, migrationRole string) error {
	var currentDatabase, sessionUser, currentUser, databaseOwner string
	err := db.QueryRowxContext(ctx, `
		SELECT current_database(), session_user, current_user, owner.rolname
		FROM pg_database database
		JOIN pg_roles owner ON owner.oid = database.datdba
		WHERE database.datname = current_database()
	`).Scan(&currentDatabase, &sessionUser, &currentUser, &databaseOwner)
	if err != nil {
		return fmt.Errorf("read migration authority: %w", err)
	}
	if currentDatabase != expectedDatabase || sessionUser != migrationRole || currentUser != migrationRole || databaseOwner != migrationRole {
		return fmt.Errorf(
			"database migration requires database %q with session user, current user, and owner %q (got database %q, session user %q, current user %q, owner %q)",
			expectedDatabase,
			migrationRole,
			currentDatabase,
			sessionUser,
			currentUser,
			databaseOwner,
		)
	}
	return nil
}

func migrationAuthorityResetStatements(migrationRole string) []string {
	migrate := quoteMigrationIdentifier(migrationRole)
	return []string{
		"REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC CASCADE",
		"REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM PUBLIC CASCADE",
		"REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM PUBLIC CASCADE",
		"REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM PUBLIC CASCADE",
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + migrate + " IN SCHEMA public REVOKE ALL PRIVILEGES ON TABLES FROM PUBLIC CASCADE",
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + migrate + " IN SCHEMA public REVOKE ALL PRIVILEGES ON SEQUENCES FROM PUBLIC CASCADE",
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + migrate + " IN SCHEMA public REVOKE ALL PRIVILEGES ON FUNCTIONS FROM PUBLIC CASCADE",
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + migrate + " IN SCHEMA public REVOKE ALL PRIVILEGES ON TYPES FROM PUBLIC CASCADE",
		migrationRoleACLReset(migrate),
		"GRANT ALL PRIVILEGES ON SCHEMA public TO " + migrate,
		"GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO " + migrate,
		"GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO " + migrate,
		"GRANT ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public TO " + migrate,
	}
}

func migrationRoleACLReset(migrationRole string) string {
	return `DO $noebs_role_acl_reset$
DECLARE
  grantee RECORD;
  managed_type RECORD;
BEGIN
  FOR managed_type IN
    SELECT format('%I.%I', schema.nspname, type.typname) AS qualified_name
    FROM pg_type type
    JOIN pg_namespace schema ON schema.oid = type.typnamespace
    LEFT JOIN pg_class relation ON relation.oid = type.typrelid
    WHERE schema.nspname = 'public'
      AND (
        type.typtype IN ('d', 'e', 'r', 'm')
        OR (type.typtype = 'c' AND relation.relkind = 'c')
      )
  LOOP
    EXECUTE format('REVOKE ALL PRIVILEGES ON TYPE %s FROM PUBLIC CASCADE', managed_type.qualified_name);
  END LOOP;
  FOR grantee IN SELECT rolname FROM pg_roles ORDER BY (rolname = current_user), rolname LOOP
    EXECUTE format('REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM %I CASCADE', grantee.rolname);
    EXECUTE format('REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM %I CASCADE', grantee.rolname);
    EXECUTE format('REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM %I CASCADE', grantee.rolname);
    FOR managed_type IN
      SELECT format('%I.%I', schema.nspname, type.typname) AS qualified_name
      FROM pg_type type
      JOIN pg_namespace schema ON schema.oid = type.typnamespace
      LEFT JOIN pg_class relation ON relation.oid = type.typrelid
      WHERE schema.nspname = 'public'
        AND (
          type.typtype IN ('d', 'e', 'r', 'm')
          OR (type.typtype = 'c' AND relation.relkind = 'c')
        )
    LOOP
      EXECUTE format('REVOKE ALL PRIVILEGES ON TYPE %s FROM %I CASCADE', managed_type.qualified_name, grantee.rolname);
    END LOOP;
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE ` + migrationRole + ` IN SCHEMA public REVOKE ALL PRIVILEGES ON TABLES FROM %I CASCADE', grantee.rolname);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE ` + migrationRole + ` IN SCHEMA public REVOKE ALL PRIVILEGES ON SEQUENCES FROM %I CASCADE', grantee.rolname);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE ` + migrationRole + ` IN SCHEMA public REVOKE ALL PRIVILEGES ON FUNCTIONS FROM %I CASCADE', grantee.rolname);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE ` + migrationRole + ` IN SCHEMA public REVOKE ALL PRIVILEGES ON TYPES FROM %I CASCADE', grantee.rolname);
    EXECUTE format('REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE', grantee.rolname);
  END LOOP;
END
$noebs_role_acl_reset$`
}

func migrationAuthorityStatements(scope string) []string {
	contract := migrationAuthorityContracts[scope]
	migrationRole, _ := postgresauthority.MigrationRole(contract.database)
	statements := []string{migrationObjectOwnerAssertion(migrationRole.Name)}
	statements = append(statements, migrationAuthorityResetStatements(migrationRole.Name)...)
	if len(contract.schemaRoles) > 0 {
		statements = append(statements, "GRANT USAGE ON SCHEMA public TO "+quotedMigrationIdentifiers(contract.schemaRoles))
	}
	for _, role := range contract.broadDMLRoles {
		statements = append(statements,
			"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "+quoteMigrationIdentifier(role),
			"REVOKE ALL PRIVILEGES ON TABLE public."+quoteMigrationIdentifier(contract.versionTable)+" FROM "+quoteMigrationIdentifier(role),
			"REVOKE INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLE public.tenants FROM "+quoteMigrationIdentifier(role),
		)
	}
	for _, role := range contract.sequenceRoles {
		statements = append(statements, "GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO "+quoteMigrationIdentifier(role))
	}
	statements = append(statements, contract.specialGrants...)
	return statements
}

func migrationObjectOwnerAssertion(migrationRole string) string {
	role := quoteMigrationLiteral(migrationRole)
	return fmt.Sprintf(`DO $noebs_object_owners$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM pg_namespace
    WHERE nspname NOT IN ('public', 'information_schema')
      AND nspname !~ '^pg_'
  ) OR EXISTS (
    SELECT 1
    FROM pg_namespace
    WHERE nspname = 'public'
      AND pg_get_userbyid(nspowner) <> %s
  ) OR EXISTS (
    SELECT 1
    FROM pg_class relation
    JOIN pg_namespace schema ON schema.oid = relation.relnamespace
    WHERE schema.nspname = 'public'
      AND relation.relkind IN ('r', 'p', 'S', 'v', 'm', 'f')
      AND pg_get_userbyid(relation.relowner) <> %s
  ) OR EXISTS (
    SELECT 1
    FROM pg_proc function
    JOIN pg_namespace schema ON schema.oid = function.pronamespace
    WHERE schema.nspname = 'public'
      AND pg_get_userbyid(function.proowner) <> %s
  ) OR EXISTS (
    SELECT 1
    FROM pg_type type
    JOIN pg_namespace schema ON schema.oid = type.typnamespace
    LEFT JOIN pg_class relation ON relation.oid = type.typrelid
    WHERE schema.nspname = 'public'
      AND (
        type.typtype IN ('d', 'e', 'r', 'm')
        OR (type.typtype = 'c' AND relation.relkind = 'c')
      )
      AND pg_get_userbyid(type.typowner) <> %s
  ) THEN
    RAISE EXCEPTION 'database schemas or public objects do not match migration role %% authority', %s;
  END IF;
END
$noebs_object_owners$`, role, role, role, role, role)
}

func convergeMigrationAuthority(ctx context.Context, db *DB, scope string) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for index, statement := range migrationAuthorityStatements(scope) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("authority statement %d: %w", index+1, err)
		}
	}
	return tx.Commit()
}

// migrationAuthoritySQL renders the transaction for static contract tests.
// Runtime convergence uses an explicit sqlx transaction above.
func migrationAuthoritySQL(scope string) string {
	statements := append([]string{"BEGIN"}, migrationAuthorityStatements(scope)...)
	statements = append(statements, "COMMIT")
	return strings.Join(statements, ";\n") + ";"
}

func walletAuthorityGrants() []string {
	return []string{
		`GRANT SELECT ON TABLE public.tenants TO wallet_ledger_runtime, wallet_ledger_worker, wallet_ledger_webhook`,
		`GRANT SELECT, INSERT ON TABLE public.operator_identities, public.wallets, public.fee_configs, public.exchange_rates, public.withdrawal_destinations TO wallet_ledger_runtime`,
		`GRANT SELECT ON TABLE public.ledger_transactions, public.ledger_entries, public.transaction_limits, public.wallet_audit_log, public.funding_sources TO wallet_ledger_runtime`,
		`GRANT UPDATE (is_active, updated_at) ON TABLE public.withdrawal_destinations TO wallet_ledger_runtime`,
		`GRANT USAGE ON SEQUENCE public.operator_identities_id_seq, public.fee_configs_id_seq, public.exchange_rates_id_seq, public.withdrawal_destinations_id_seq TO wallet_ledger_runtime`,
		`GRANT SELECT ON TABLE public.operator_identities, public.fee_configs, public.exchange_rates, public.transaction_limits, public.withdrawal_destinations TO wallet_ledger_worker`,
		`GRANT SELECT, INSERT ON TABLE public.wallets, public.ledger_transactions, public.ledger_entries, public.balance_holds, public.funding_sources, public.ledger_funding_links, public.ledger_withdrawal_destination_links TO wallet_ledger_worker`,
		`GRANT INSERT ON TABLE public.wallet_audit_log TO wallet_ledger_worker`,
		`GRANT UPDATE (balance, available_balance, version, updated_at) ON TABLE public.wallets TO wallet_ledger_worker`,
		`GRANT UPDATE (counter_entry_id) ON TABLE public.ledger_entries TO wallet_ledger_worker`,
		`GRANT UPDATE (amount_remaining, status, captured_at, released_at, committed_at, expired_at) ON TABLE public.balance_holds TO wallet_ledger_worker`,
		`GRANT UPDATE (supports_withdrawal, verification_status, verified_at, verified_by, withdrawal_method, total_funded, last_funded_at, total_withdrawn, last_withdrawn_at, updated_at) ON TABLE public.funding_sources TO wallet_ledger_worker`,
		`GRANT UPDATE (last_used_at, total_withdrawn, updated_at) ON TABLE public.withdrawal_destinations TO wallet_ledger_worker`,
		`GRANT SELECT, INSERT ON TABLE public.transaction_limit_period_usage, public.transaction_limit_reservations TO wallet_ledger_worker`,
		`GRANT UPDATE (reserved_amount, consumed_amount, updated_at) ON TABLE public.transaction_limit_period_usage TO wallet_ledger_worker`,
		`GRANT UPDATE (status, ledger_transaction_id, consumed_at, released_at) ON TABLE public.transaction_limit_reservations TO wallet_ledger_worker`,
		`GRANT USAGE ON SEQUENCE public.ledger_transactions_id_seq, public.ledger_entries_id_seq, public.balance_holds_id_seq, public.wallet_audit_log_id_seq, public.funding_sources_id_seq, public.ledger_funding_links_id_seq, public.ledger_withdrawal_destination_links_id_seq, public.transaction_limit_reservations_id_seq TO wallet_ledger_worker`,
		`GRANT SELECT, INSERT ON TABLE public.workflow_decisions TO wallet_ledger_runtime`,
		`GRANT SELECT ON TABLE public.workflow_decisions TO wallet_ledger_worker`,
		`GRANT SELECT, INSERT ON TABLE public.manual_transfers TO wallet_ledger_runtime`,
		`GRANT USAGE ON SEQUENCE public.manual_transfers_id_seq TO wallet_ledger_runtime`,
		`GRANT SELECT ON TABLE public.manual_transfers TO wallet_ledger_worker`,
		`GRANT UPDATE (status, approved_by_operator_id, proof_of_payment, rejection_reason, approved_at, completed_at) ON TABLE public.manual_transfers TO wallet_ledger_worker`,
		`GRANT SELECT ON TABLE public.manual_transfer_approvals TO wallet_ledger_runtime`,
		`GRANT SELECT, INSERT ON TABLE public.manual_transfer_approvals TO wallet_ledger_worker`,
		`GRANT USAGE ON SEQUENCE public.manual_transfer_approvals_id_seq TO wallet_ledger_worker`,
		`GRANT SELECT, INSERT ON TABLE public.p2p_commands TO wallet_ledger_runtime`,
		`GRANT UPDATE (run_id) ON TABLE public.p2p_commands TO wallet_ledger_runtime`,
		`GRANT SELECT ON TABLE public.p2p_commands TO wallet_ledger_worker`,
		`GRANT SELECT, INSERT ON TABLE public.funding_source_withdrawal_reservations TO wallet_ledger_worker`,
		`GRANT UPDATE (status, ledger_entry_id, consumed_at, released_at) ON TABLE public.funding_source_withdrawal_reservations TO wallet_ledger_worker`,
		`GRANT USAGE ON SEQUENCE public.funding_source_withdrawal_reservations_id_seq TO wallet_ledger_worker`,
		`GRANT SELECT ON TABLE public.psp_configs, public.psp_config_overrides TO wallet_ledger_runtime`,
		`GRANT SELECT, INSERT ON TABLE public.deposit_intents TO wallet_ledger_runtime`,
		`GRANT UPDATE (run_id) ON TABLE public.deposit_intents TO wallet_ledger_runtime`,
		`GRANT USAGE ON SEQUENCE public.deposit_intents_id_seq TO wallet_ledger_runtime`,
		`GRANT SELECT, INSERT ON TABLE public.psp_transactions TO wallet_ledger_runtime`,
		`GRANT USAGE ON SEQUENCE public.psp_transactions_id_seq TO wallet_ledger_runtime`,
		`GRANT SELECT ON TABLE public.psp_configs, public.psp_config_overrides TO wallet_ledger_worker`,
		`GRANT SELECT ON TABLE public.deposit_intents TO wallet_ledger_worker`,
		`GRANT SELECT ON TABLE public.psp_transactions TO wallet_ledger_worker`,
		`GRANT UPDATE (status, psp_transaction_id, response_code, response_message, raw_response, confirmed_at, last_polled_at, next_poll_at, retry_count, last_error_type, last_error_at, lock_token, lock_expires_at, workflow_signal_payload, workflow_signal_delivered_at) ON TABLE public.psp_transactions TO wallet_ledger_worker`,
		`GRANT SELECT, INSERT ON TABLE public.psp_transaction_amounts, public.psp_interactions TO wallet_ledger_worker`,
		`GRANT USAGE ON SEQUENCE public.psp_transaction_amounts_id_seq, public.psp_interactions_id_seq TO wallet_ledger_worker`,
		`GRANT SELECT ON TABLE public.psp_configs, public.psp_config_overrides, public.psp_transactions TO wallet_ledger_webhook`,
		`GRANT UPDATE (status, psp_transaction_id, response_code, response_message, raw_response, confirmed_at, workflow_signal_payload) ON TABLE public.psp_transactions TO wallet_ledger_webhook`,
		`GRANT SELECT, INSERT ON TABLE public.psp_interactions TO wallet_ledger_webhook`,
		`GRANT USAGE ON SEQUENCE public.psp_interactions_id_seq TO wallet_ledger_webhook`,
	}
}

func quotedMigrationIdentifiers(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quoteMigrationIdentifier(value))
	}
	return strings.Join(quoted, ", ")
}

func quoteMigrationIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteMigrationLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
