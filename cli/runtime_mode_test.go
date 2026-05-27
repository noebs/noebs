package main

import (
	"errors"
	"testing"
)

func TestParseServiceRoleRequiresExplicitRole(t *testing.T) {
	_, err := parseServiceRole("")
	if !errors.Is(err, errMissingServiceRole) {
		t.Fatalf("error = %v, want %v", err, errMissingServiceRole)
	}
}

func TestParseServiceRoleRejectsUnknownRole(t *testing.T) {
	_, err := parseServiceRole("api")
	if !errors.Is(err, errInvalidServiceRole) {
		t.Fatalf("error = %v, want %v", err, errInvalidServiceRole)
	}
}

func TestCurrentServiceRoleReadsConfig(t *testing.T) {
	setServiceRoleForTest(t, serviceRolePSPWebhook)
	got, err := currentServiceRole()
	if err != nil {
		t.Fatalf("currentServiceRole() error = %v", err)
	}
	if got != serviceRolePSPWebhook {
		t.Fatalf("role = %q, want %q", got, serviceRolePSPWebhook)
	}
}

func TestParseServiceRoleAcceptsKnownRoles(t *testing.T) {
	roles := []serviceRole{
		serviceRoleAPIGateway,
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
		serviceRoleWalletAPI,
		serviceRoleWalletLedger,
		serviceRoleWalletWorker,
		serviceRoleIdentityAuthMigrate,
		serviceRoleCardVaultMigrate,
		serviceRoleEBSAdapterMigrate,
		serviceRolePSPWebhookMigrate,
		serviceRoleAdminReportingMigrate,
		serviceRoleNotificationMigrate,
		serviceRoleBeneficiaryMigrate,
		serviceRoleWalletLedgerMigrate,
	}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			got, err := parseServiceRole(string(role))
			if err != nil {
				t.Fatalf("parseServiceRole() error = %v", err)
			}
			if got != role {
				t.Fatalf("role = %q, want %q", got, role)
			}
		})
	}
}

func TestServiceRoleProcessOwnership(t *testing.T) {
	if !serviceRoleAPIGateway.startsHTTP() || serviceRoleAPIGateway.startsGRPC() || serviceRoleAPIGateway.startsWalletWorker() || serviceRoleAPIGateway.runsMigrations() {
		t.Fatalf("api-gateway role should own only the HTTP process")
	}
	if !serviceRoleIdentityAuth.startsHTTP() || serviceRoleIdentityAuth.startsGRPC() || serviceRoleIdentityAuth.startsWalletWorker() || serviceRoleIdentityAuth.runsMigrations() {
		t.Fatalf("identity-auth role should own only the HTTP process")
	}
	if !serviceRoleCardVault.startsHTTP() || serviceRoleCardVault.startsGRPC() || serviceRoleCardVault.startsWalletWorker() || serviceRoleCardVault.runsMigrations() {
		t.Fatalf("card-vault role should own only the HTTP process")
	}
	if !serviceRoleEBSAdapter.startsHTTP() || serviceRoleEBSAdapter.startsGRPC() || serviceRoleEBSAdapter.startsWalletWorker() || serviceRoleEBSAdapter.runsMigrations() {
		t.Fatalf("ebs-adapter role should own only the HTTP process")
	}
	if !serviceRolePSPWebhook.startsHTTP() || serviceRolePSPWebhook.startsGRPC() || serviceRolePSPWebhook.startsWalletWorker() || serviceRolePSPWebhook.runsMigrations() {
		t.Fatalf("psp-webhook role should own only the HTTP process")
	}
	if !serviceRoleAdminReporting.startsHTTP() || serviceRoleAdminReporting.startsGRPC() || serviceRoleAdminReporting.startsWalletWorker() || serviceRoleAdminReporting.runsMigrations() {
		t.Fatalf("admin-reporting role should own only the HTTP process")
	}
	if !serviceRoleNotification.startsHTTP() || !serviceRoleNotification.startsChat() || serviceRoleNotification.startsGRPC() || serviceRoleNotification.startsWalletWorker() || serviceRoleNotification.runsMigrations() {
		t.Fatalf("notification-chat role should own only the HTTP chat process")
	}
	if !serviceRoleBeneficiary.startsHTTP() || serviceRoleBeneficiary.startsGRPC() || serviceRoleBeneficiary.startsWalletWorker() || serviceRoleBeneficiary.runsMigrations() {
		t.Fatalf("consumer-beneficiary role should own only the HTTP process")
	}
	if !serviceRoleWalletAPI.startsHTTP() || serviceRoleWalletAPI.startsGRPC() || serviceRoleWalletAPI.startsWalletWorker() || serviceRoleWalletAPI.runsMigrations() {
		t.Fatalf("wallet-api role should own only the HTTP process")
	}
	if serviceRoleWalletLedger.startsHTTP() || !serviceRoleWalletLedger.startsGRPC() || serviceRoleWalletLedger.startsWalletWorker() || serviceRoleWalletLedger.runsMigrations() {
		t.Fatalf("wallet-ledger role should own only the gRPC process")
	}
	if serviceRoleWalletWorker.startsHTTP() || serviceRoleWalletWorker.startsGRPC() || !serviceRoleWalletWorker.startsWalletWorker() || serviceRoleWalletWorker.runsMigrations() {
		t.Fatalf("wallet-worker role should own only the Temporal worker process")
	}
	migrationRoles := []serviceRole{
		serviceRoleIdentityAuthMigrate,
		serviceRoleCardVaultMigrate,
		serviceRoleEBSAdapterMigrate,
		serviceRolePSPWebhookMigrate,
		serviceRoleAdminReportingMigrate,
		serviceRoleNotificationMigrate,
		serviceRoleBeneficiaryMigrate,
		serviceRoleWalletLedgerMigrate,
	}
	for _, role := range migrationRoles {
		t.Run(string(role), func(t *testing.T) {
			if role.startsHTTP() || role.startsGRPC() || role.startsWalletWorker() || !role.runsMigrations() {
				t.Fatalf("%s role should only run migrations", role)
			}
			if _, ok := role.migrationScope(); !ok {
				t.Fatalf("%s role should map to a migration scope", role)
			}
		})
	}
}

func TestServiceRoleDatabaseOwnership(t *testing.T) {
	if serviceRoleAPIGateway.opensDatabase() {
		t.Fatalf("api-gateway role must not open a service database")
	}
	if err := validateRoleDatabaseConfig(serviceRoleAPIGateway, "postgres://noebs:noebs@postgres:5432/api_gateway?sslmode=disable", "", "pgx"); !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("api-gateway database error = %v, want %v", err, errDatabaseNotAllowed)
	}
	if err := validateRoleDatabaseConfig(serviceRoleAPIGateway, "", "/data/noebs.db", "sqlite3"); !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("api-gateway database path error = %v, want %v", err, errDatabaseNotAllowed)
	}

	databaseRoles := []serviceRole{
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
		serviceRoleWalletAPI,
		serviceRoleWalletLedger,
		serviceRoleWalletWorker,
		serviceRoleIdentityAuthMigrate,
		serviceRoleCardVaultMigrate,
		serviceRoleEBSAdapterMigrate,
		serviceRolePSPWebhookMigrate,
		serviceRoleAdminReportingMigrate,
		serviceRoleNotificationMigrate,
		serviceRoleBeneficiaryMigrate,
		serviceRoleWalletLedgerMigrate,
	}
	for _, role := range databaseRoles {
		t.Run(string(role), func(t *testing.T) {
			if !role.opensDatabase() {
				t.Fatalf("%s should require an explicit database URL", role)
			}
			if err := validateRoleDatabaseConfig(role, "", "", "pgx"); !errors.Is(err, errMissingDatabaseURL) {
				t.Fatalf("%s database error = %v, want %v", role, err, errMissingDatabaseURL)
			}
			if err := validateRoleDatabaseConfig(role, "", "", "sqlite3"); !errors.Is(err, errMissingDatabasePath) {
				t.Fatalf("%s sqlite database error = %v, want %v", role, err, errMissingDatabasePath)
			}
		})
	}
}
