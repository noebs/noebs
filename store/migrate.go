package store

import (
	"context"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/postgres/*.sql migrations/postgres/*/*.sql
var postgresMigrations embed.FS

var migrationDriver string
var migrationDefaultTenant string
var migrationScope string

const (
	MigrationScopeLegacy              = "legacy"
	MigrationScopeIdentityAuth        = "identity-auth"
	MigrationScopeCardVault           = "card-vault"
	MigrationScopeEBSAdapter          = "ebs-adapter"
	MigrationScopePSPWebhook          = "psp-webhook"
	MigrationScopeAdminReporting      = "admin-reporting"
	MigrationScopeNotificationChat    = "notification-chat"
	MigrationScopeConsumerBeneficiary = "consumer-beneficiary"
	MigrationScopeWalletLedger        = "wallet-ledger"
)

var migrationScopePaths = map[string]string{
	MigrationScopeLegacy:              "migrations/postgres",
	MigrationScopeIdentityAuth:        "migrations/postgres/identity_auth",
	MigrationScopeCardVault:           "migrations/postgres/card_vault",
	MigrationScopeEBSAdapter:          "migrations/postgres/ebs_adapter",
	MigrationScopePSPWebhook:          "migrations/postgres/psp_webhook",
	MigrationScopeAdminReporting:      "migrations/postgres/admin_reporting",
	MigrationScopeNotificationChat:    "migrations/postgres/notification_chat",
	MigrationScopeConsumerBeneficiary: "migrations/postgres/consumer_beneficiary",
	MigrationScopeWalletLedger:        "migrations/postgres/wallet_ledger",
}

var migrationScopeTableNames = map[string]string{
	MigrationScopeLegacy:              "goose_db_version",
	MigrationScopeIdentityAuth:        "goose_db_version_identity_auth",
	MigrationScopeCardVault:           "goose_db_version_card_vault",
	MigrationScopeEBSAdapter:          "goose_db_version_ebs_adapter",
	MigrationScopePSPWebhook:          "goose_db_version_psp_webhook",
	MigrationScopeAdminReporting:      "goose_db_version_admin_reporting",
	MigrationScopeNotificationChat:    "goose_db_version_notification_chat",
	MigrationScopeConsumerBeneficiary: "goose_db_version_consumer_beneficiary",
	MigrationScopeWalletLedger:        "goose_db_version_wallet_ledger",
}

// Migrate applies embedded SQL/Go migrations using goose.
func Migrate(ctx context.Context, db *DB, defaultTenantID string) error {
	return MigrateScope(ctx, db, defaultTenantID, MigrationScopeLegacy)
}

// MigrateScope applies embedded SQL migrations for one service-owned database.
func MigrateScope(ctx context.Context, db *DB, defaultTenantID, scope string) error {
	if db == nil || db.DB == nil {
		return fmt.Errorf("db is nil")
	}
	migrationPath, ok := migrationScopePaths[scope]
	if !ok {
		return fmt.Errorf("unknown migration scope %q", scope)
	}
	tenantID, err := ValidateTenantID(defaultTenantID)
	if err != nil {
		return err
	}

	migrationDriver = db.Driver
	migrationDefaultTenant = tenantID
	migrationScope = scope

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
