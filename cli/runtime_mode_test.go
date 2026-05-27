package main

import (
	"errors"
	"testing"

	"github.com/adonese/noebs/store"
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
	if serviceRoleWalletAPI.opensDatabase() {
		t.Fatalf("wallet-api role must not open the wallet-ledger database")
	}
	if err := validateRoleDatabaseConfig(serviceRoleAPIGateway, "postgres://noebs:noebs@postgres:5432/api_gateway?sslmode=disable", "", "pgx"); !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("api-gateway database error = %v, want %v", err, errDatabaseNotAllowed)
	}
	if err := validateRoleDatabaseConfig(serviceRoleAPIGateway, "", "/data/noebs.db", "sqlite3"); !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("api-gateway database path error = %v, want %v", err, errDatabaseNotAllowed)
	}
	if err := validateRoleDatabaseConfig(serviceRoleWalletAPI, "postgres://noebs:noebs@postgres:5432/wallet_ledger?sslmode=disable", "", "pgx"); !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("wallet-api database error = %v, want %v", err, errDatabaseNotAllowed)
	}

	databaseRoles := []serviceRole{
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
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
			if err := validateRoleDatabaseConfig(role, "postgres://noebs:noebs@postgres:5432/noebs?sslmode=disable", "", ""); !errors.Is(err, store.ErrMissingDatabaseDriver) {
				t.Fatalf("%s missing driver error = %v, want %v", role, err, store.ErrMissingDatabaseDriver)
			}
			if err := validateRoleDatabaseConfig(role, "postgres://noebs:noebs@postgres:5432/noebs?sslmode=disable", "", "mysql"); !errors.Is(err, store.ErrUnsupportedDatabaseDriver) {
				t.Fatalf("%s unsupported driver error = %v, want %v", role, err, store.ErrUnsupportedDatabaseDriver)
			}
		})
	}
}

func TestServiceRoleDatabaseOwnerKeys(t *testing.T) {
	tests := []struct {
		role serviceRole
		want serviceRole
	}{
		{role: serviceRoleIdentityAuth, want: serviceRoleIdentityAuth},
		{role: serviceRoleIdentityAuthMigrate, want: serviceRoleIdentityAuth},
		{role: serviceRoleCardVault, want: serviceRoleCardVault},
		{role: serviceRoleCardVaultMigrate, want: serviceRoleCardVault},
		{role: serviceRoleEBSAdapter, want: serviceRoleEBSAdapter},
		{role: serviceRoleEBSAdapterMigrate, want: serviceRoleEBSAdapter},
		{role: serviceRolePSPWebhook, want: serviceRolePSPWebhook},
		{role: serviceRolePSPWebhookMigrate, want: serviceRolePSPWebhook},
		{role: serviceRoleAdminReporting, want: serviceRoleAdminReporting},
		{role: serviceRoleAdminReportingMigrate, want: serviceRoleAdminReporting},
		{role: serviceRoleNotification, want: serviceRoleNotification},
		{role: serviceRoleNotificationMigrate, want: serviceRoleNotification},
		{role: serviceRoleBeneficiary, want: serviceRoleBeneficiary},
		{role: serviceRoleBeneficiaryMigrate, want: serviceRoleBeneficiary},
		{role: serviceRoleWalletLedger, want: serviceRoleWalletLedger},
		{role: serviceRoleWalletLedgerMigrate, want: serviceRoleWalletLedger},
		{role: serviceRoleWalletWorker, want: serviceRoleWalletLedger},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			got, ok := tt.role.databaseOwnerRole()
			if !ok {
				t.Fatalf("%s should have a database owner role", tt.role)
			}
			if got != tt.want {
				t.Fatalf("%s database owner = %s, want %s", tt.role, got, tt.want)
			}
		})
	}

	noDatabaseRoles := []serviceRole{serviceRoleAPIGateway, serviceRoleWalletAPI}
	for _, role := range noDatabaseRoles {
		t.Run(string(role), func(t *testing.T) {
			if owner, ok := role.databaseOwnerRole(); ok {
				t.Fatalf("%s database owner = %s, want none", role, owner)
			}
		})
	}
}

func TestServiceRoleTemporalOwnership(t *testing.T) {
	temporalRoles := []serviceRole{
		serviceRolePSPWebhook,
		serviceRoleWalletLedger,
		serviceRoleWalletWorker,
	}
	for _, role := range temporalRoles {
		t.Run(string(role), func(t *testing.T) {
			if !role.requiresTemporal() {
				t.Fatalf("%s should require Temporal", role)
			}
		})
	}

	noTemporalRoles := []serviceRole{
		serviceRoleAPIGateway,
		serviceRoleWalletAPI,
		serviceRoleWalletLedgerMigrate,
	}
	for _, role := range noTemporalRoles {
		t.Run(string(role), func(t *testing.T) {
			if role.requiresTemporal() {
				t.Fatalf("%s should not require Temporal", role)
			}
		})
	}
}

func TestInitRoleServicesInitializesOnlyOwnedDependencies(t *testing.T) {
	ensureInit()
	previousServices := captureRoleServices()
	t.Cleanup(previousServices.restore)

	tests := []struct {
		role          serviceRole
		consumer      bool
		dashboard     bool
		merchant      bool
		wallet        bool
		walletPSPDeps bool
	}{
		{role: serviceRoleAPIGateway},
		{role: serviceRoleIdentityAuth, consumer: true},
		{role: serviceRoleCardVault, consumer: true},
		{role: serviceRoleEBSAdapter, consumer: true, merchant: true},
		{role: serviceRolePSPWebhook, wallet: true, walletPSPDeps: true},
		{role: serviceRoleAdminReporting, dashboard: true},
		{role: serviceRoleNotification, consumer: true},
		{role: serviceRoleBeneficiary, consumer: true},
		{role: serviceRoleWalletAPI},
		{role: serviceRoleWalletLedger, wallet: true},
		{role: serviceRoleWalletWorker, wallet: true, walletPSPDeps: true},
		{role: serviceRoleIdentityAuthMigrate},
		{role: serviceRoleWalletLedgerMigrate},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if err := initRoleServices(tt.role); err != nil {
				t.Fatalf("initRoleServices(%s): %v", tt.role, err)
			}
			if got := consumerService.Store != nil; got != tt.consumer {
				t.Fatalf("consumerService initialized = %t, want %t", got, tt.consumer)
			}
			if got := dashService.Store != nil; got != tt.dashboard {
				t.Fatalf("dashService initialized = %t, want %t", got, tt.dashboard)
			}
			if got := merchantServices.Store != nil; got != tt.merchant {
				t.Fatalf("merchantServices initialized = %t, want %t", got, tt.merchant)
			}
			if got := walletService != nil; got != tt.wallet {
				t.Fatalf("walletService initialized = %t, want %t", got, tt.wallet)
			}
			pspDeps := walletPSPRegistry != nil && walletPSPLoader != nil
			if pspDeps != tt.walletPSPDeps {
				t.Fatalf("wallet PSP deps initialized = %t, want %t", pspDeps, tt.walletPSPDeps)
			}
		})
	}
}
