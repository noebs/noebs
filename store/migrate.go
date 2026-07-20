package store

import (
	"context"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/postgres/*/*.sql
var postgresMigrations embed.FS

const (
	MigrationScopeIdentityAuth     = "identity-auth"
	MigrationScopeCardVault        = "card-vault"
	MigrationScopeEBSAdapter       = "ebs-adapter"
	MigrationScopePSPWebhook       = "psp-webhook"
	MigrationScopeAdminReporting   = "admin-reporting"
	MigrationScopeNotificationChat = "notification-chat"
	MigrationScopeWalletLedger     = "wallet-ledger"
	MigrationScopeWorkloadAuth     = "workload-auth"
	MigrationScopeGatewayAuth      = "gateway-auth"
)

var migrationScopePaths = map[string]string{
	MigrationScopeIdentityAuth:     "migrations/postgres/identity_auth",
	MigrationScopeCardVault:        "migrations/postgres/card_vault",
	MigrationScopeEBSAdapter:       "migrations/postgres/ebs_adapter",
	MigrationScopePSPWebhook:       "migrations/postgres/psp_webhook",
	MigrationScopeAdminReporting:   "migrations/postgres/admin_reporting",
	MigrationScopeNotificationChat: "migrations/postgres/notification_chat",
	MigrationScopeWalletLedger:     "migrations/postgres/wallet_ledger",
	MigrationScopeWorkloadAuth:     "migrations/postgres/workload_auth",
	MigrationScopeGatewayAuth:      "migrations/postgres/gateway_auth",
}

var migrationScopeTableNames = map[string]string{
	MigrationScopeIdentityAuth:     "goose_db_version_identity_auth",
	MigrationScopeCardVault:        "goose_db_version_card_vault",
	MigrationScopeEBSAdapter:       "goose_db_version_ebs_adapter",
	MigrationScopePSPWebhook:       "goose_db_version_psp_webhook",
	MigrationScopeAdminReporting:   "goose_db_version_admin_reporting",
	MigrationScopeNotificationChat: "goose_db_version_notification_chat",
	MigrationScopeWalletLedger:     "goose_db_version_wallet_ledger",
	MigrationScopeWorkloadAuth:     "goose_db_version_workload_auth",
	MigrationScopeGatewayAuth:      "goose_db_version_gateway_auth",
}

// MigrateScope applies embedded SQL migrations for one service-owned database.
func MigrateScope(ctx context.Context, db *DB, scope string) error {
	if db == nil || db.DB == nil {
		return fmt.Errorf("db is nil")
	}
	migrationPath, ok := migrationScopePaths[scope]
	if !ok {
		return fmt.Errorf("unknown migration scope %q", scope)
	}
	if db.Driver != DriverPostgres {
		return fmt.Errorf("unsupported migration driver %q (postgres only)", db.Driver)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	goose.SetTableName(migrationScopeTableNames[scope])
	goose.SetBaseFS(postgresMigrations)
	return goose.UpContext(ctx, db.DB.DB, migrationPath)
}
