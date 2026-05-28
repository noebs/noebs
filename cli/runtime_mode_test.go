package main

import (
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
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
	if err := validateRoleDatabaseConfig(serviceRoleAPIGateway, "postgres://noebs:noebs@postgres:5432/api_gateway?sslmode=disable", "pgx"); !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("api-gateway database error = %v, want %v", err, errDatabaseNotAllowed)
	}
	if err := rejectLegacyDatabasePath(map[string]interface{}{"db_path": "/data/noebs.db"}); !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("database path error = %v, want %v", err, errDatabaseNotAllowed)
	}
	if err := validateRoleDatabaseConfig(serviceRoleWalletAPI, "postgres://noebs:noebs@postgres:5432/wallet_ledger?sslmode=disable", "pgx"); !errors.Is(err, errDatabaseNotAllowed) {
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
			if err := validateRoleDatabaseConfig(role, "", "pgx"); !errors.Is(err, errMissingDatabaseURL) {
				t.Fatalf("%s database error = %v, want %v", role, err, errMissingDatabaseURL)
			}
			if err := validateRoleDatabaseConfig(role, "", "sqlite3"); !errors.Is(err, store.ErrUnsupportedDatabaseDriver) {
				t.Fatalf("%s sqlite driver error = %v, want %v", role, err, store.ErrUnsupportedDatabaseDriver)
			}
			if err := validateRoleDatabaseConfig(role, "postgres://noebs:noebs@postgres:5432/noebs?sslmode=disable", ""); !errors.Is(err, store.ErrMissingDatabaseDriver) {
				t.Fatalf("%s missing driver error = %v, want %v", role, err, store.ErrMissingDatabaseDriver)
			}
			if err := validateRoleDatabaseConfig(role, "postgres://noebs:noebs@postgres:5432/noebs?sslmode=disable", "mysql"); !errors.Is(err, store.ErrUnsupportedDatabaseDriver) {
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

func validWalletRuntimeConfig() ebs_fields.NoebsConfig {
	return ebs_fields.NoebsConfig{
		WalletEnabled:                    true,
		TemporalEnabled:                  true,
		TemporalHost:                     "temporal-frontend",
		TemporalPort:                     "7233",
		TemporalNamespace:                "default",
		GRPCEnabled:                      true,
		GRPCPort:                         ":9090",
		WalletDefaultCurrency:            "SDG",
		WalletHoldExpirySeconds:          3600,
		WalletApprovalTimeoutSeconds:     3600,
		WalletVerificationTimeoutSeconds: 86400,
		WalletManualTransferApprovalTimeoutSeconds: 86400,
		WalletPSPPollerCron:                        "*/5 * * * *",
		WalletPSPPollerBatchSize:                   100,
		WalletPSPPollerIntervalSeconds:             300,
		WalletReconciliationCron:                   "0 3 * * *",
		WalletReconciliationBatchSize:              500,
		WalletReconciliationLookbackHours:          24,
	}
}

func TestServiceRoleRuntimeConfigRequiresExplicitOTelConfigWhenEnabled(t *testing.T) {
	cfg := ebs_fields.NoebsConfig{OtelEnabled: true}
	if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, cfg); !errors.Is(err, errMissingOtelEndpoint) {
		t.Fatalf("otel endpoint error = %v, want %v", err, errMissingOtelEndpoint)
	}

	cfg = ebs_fields.NoebsConfig{
		OtelEnabled:  true,
		OtelEndpoint: "otel-collector:4317",
	}
	if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, cfg); !errors.Is(err, errMissingOtelServiceName) {
		t.Fatalf("otel service name error = %v, want %v", err, errMissingOtelServiceName)
	}

	cfg = ebs_fields.NoebsConfig{
		OtelEnabled:     true,
		OtelEndpoint:    "otel-collector:4317",
		OtelServiceName: string(serviceRoleAPIGateway),
		OtelSampleRate:  0.1,
	}
	if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, cfg); !errors.Is(err, errInvalidOtelServiceName) {
		t.Fatalf("otel service name mismatch error = %v, want %v", err, errInvalidOtelServiceName)
	}

	cfg = ebs_fields.NoebsConfig{
		OtelEnabled:     true,
		OtelEndpoint:    "otel-collector:4317",
		OtelServiceName: string(serviceRoleIdentityAuth),
	}
	if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, cfg); !errors.Is(err, errInvalidOtelSampleRate) {
		t.Fatalf("otel sample rate error = %v, want %v", err, errInvalidOtelSampleRate)
	}

	cfg.OtelSampleRate = 0.25
	cfg.ServiceDiscovery = map[string]string{
		string(serviceRoleCardVault): "http://card-vault:8080",
	}
	if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, cfg); err != nil {
		t.Fatalf("explicit otel runtime config error = %v", err)
	}
}

func TestServiceRoleRuntimeConfigRequiresExplicitEBSAdapterConfig(t *testing.T) {
	legacy := explicitEBSRuntimeConfig()
	legacy.ConsumerQAIP = "https://consumer.qa.example"
	if err := validateRoleRuntimeConfig(serviceRoleEBSAdapter, legacy); !errors.Is(err, errLegacyEBSConfig) {
		t.Fatalf("legacy ebs-adapter runtime config error = %v, want %v", err, errLegacyEBSConfig)
	}

	legacy = explicitEBSRuntimeConfig()
	legacy.IsConsumerProd = true
	if err := validateRoleRuntimeConfig(serviceRoleEBSAdapter, legacy); !errors.Is(err, errLegacyEBSConfig) {
		t.Fatalf("legacy ebs-adapter mode error = %v, want %v", err, errLegacyEBSConfig)
	}

	required := []struct {
		name   string
		mutate func(*ebs_fields.NoebsConfig)
	}{
		{name: "consumer_endpoint", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.ConsumerIP = "" }},
		{name: "merchant_endpoint", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.MerchantIP = "" }},
		{name: "ipin_endpoint", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.IPINIp = "" }},
		{name: "consumer_app_id", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.ConsumerID = "" }},
		{name: "merchant_app_id", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.MerchantID = "" }},
		{name: "p2p_fees", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.EBSDynamicFees.CardTransferfees = 0 }},
		{name: "special_payment_fees", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.EBSDynamicFees.SpecialPaymentFees = 0 }},
		{name: "custom_fees", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.EBSDynamicFees.CustomFees = 0 }},
	}

	for _, tt := range required {
		t.Run(tt.name, func(t *testing.T) {
			cfg := explicitEBSRuntimeConfig()
			tt.mutate(&cfg)
			if err := validateRoleRuntimeConfig(serviceRoleEBSAdapter, cfg); !errors.Is(err, errMissingEBSConfig) {
				t.Fatalf("ebs-adapter runtime config error = %v, want %v", err, errMissingEBSConfig)
			}
		})
	}

	if err := validateRoleRuntimeConfig(serviceRoleEBSAdapter, explicitEBSRuntimeConfig()); err != nil {
		t.Fatalf("explicit ebs-adapter runtime config error = %v", err)
	}
	missingCardVault := explicitEBSRuntimeConfig()
	missingCardVault.ServiceDiscovery = map[string]string{}
	if err := validateRoleRuntimeConfig(serviceRoleEBSAdapter, missingCardVault); err == nil {
		t.Fatalf("ebs-adapter should require card-vault service discovery")
	}
	missingIdentityAuth := explicitEBSRuntimeConfig()
	delete(missingIdentityAuth.ServiceDiscovery, string(serviceRoleIdentityAuth))
	if err := validateRoleRuntimeConfig(serviceRoleEBSAdapter, missingIdentityAuth); err == nil {
		t.Fatalf("ebs-adapter should require identity-auth service discovery")
	}
	missingNotification := explicitEBSRuntimeConfig()
	delete(missingNotification.ServiceDiscovery, string(serviceRoleNotification))
	if err := validateRoleRuntimeConfig(serviceRoleEBSAdapter, missingNotification); err == nil {
		t.Fatalf("ebs-adapter should require notification-chat service discovery")
	}
	withoutAdminReporting := explicitEBSRuntimeConfig()
	delete(withoutAdminReporting.ServiceDiscovery, string(serviceRoleAdminReporting))
	if err := validateRoleRuntimeConfig(serviceRoleEBSAdapter, withoutAdminReporting); err != nil {
		t.Fatalf("ebs-adapter should not require admin-reporting service discovery: %v", err)
	}
	if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, identityAuthRuntimeConfig()); err != nil {
		t.Fatalf("identity-auth should not require EBS endpoint config: %v", err)
	}
}

func TestServiceRoleRuntimeConfigDoesNotRequireCardVaultServiceDiscovery(t *testing.T) {
	if err := validateRoleRuntimeConfig(serviceRoleCardVault, ebs_fields.NoebsConfig{DataKey: "card-vault-data-key"}); err != nil {
		t.Fatalf("card-vault runtime config error = %v", err)
	}
	if err := validateRoleRuntimeConfig(serviceRoleCardVault, ebs_fields.NoebsConfig{}); !errors.Is(err, store.ErrMissingDataKey) {
		t.Fatalf("card-vault missing data key error = %v, want %v", err, store.ErrMissingDataKey)
	}
}

func TestServiceRoleRuntimeConfigRequiresIdentityAuthCardVaultDiscovery(t *testing.T) {
	if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, identityAuthRuntimeConfig()); err != nil {
		t.Fatalf("identity-auth runtime config error = %v", err)
	}
	if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, ebs_fields.NoebsConfig{}); err == nil {
		t.Fatalf("identity-auth should require card-vault service discovery")
	}
}

func explicitEBSRuntimeConfig() ebs_fields.NoebsConfig {
	return ebs_fields.NoebsConfig{
		ConsumerIP: "https://consumer.ebs.example",
		MerchantIP: "https://merchant.ebs.example",
		IPINIp:     "https://ipin.ebs.example",
		ConsumerID: "consumer-app",
		MerchantID: "merchant-app",
		EBSDynamicFees: ebs_fields.DynamicFeesFields{
			CardTransferfees:   30,
			SpecialPaymentFees: 2,
			CustomFees:         85,
		},
		ServiceDiscovery: map[string]string{
			string(serviceRoleIdentityAuth): "http://identity-auth:8080",
			string(serviceRoleCardVault):    "http://card-vault:8080",
			string(serviceRoleNotification): "http://notification-chat:8080",
		},
	}
}

func identityAuthRuntimeConfig() ebs_fields.NoebsConfig {
	return ebs_fields.NoebsConfig{
		ServiceDiscovery: map[string]string{
			string(serviceRoleCardVault): "http://card-vault:8080",
		},
	}
}

func TestServiceRoleRuntimeConfigRequiresExplicitWalletConfig(t *testing.T) {
	if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, identityAuthRuntimeConfig()); err != nil {
		t.Fatalf("identity-auth runtime config error = %v", err)
	}
	if err := validateRoleRuntimeConfig(serviceRoleWalletAPI, ebs_fields.NoebsConfig{}); !errors.Is(err, errWalletNotEnabled) {
		t.Fatalf("wallet-api runtime config error = %v, want %v", err, errWalletNotEnabled)
	}

	tests := []struct {
		name   string
		mutate func(*ebs_fields.NoebsConfig)
	}{
		{name: "currency", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.WalletDefaultCurrency = "" }},
		{name: "hold_expiry", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.WalletHoldExpirySeconds = 0 }},
		{name: "approval_timeout", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.WalletApprovalTimeoutSeconds = 0 }},
		{name: "verification_timeout", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.WalletVerificationTimeoutSeconds = 0 }},
		{name: "manual_transfer_approval_timeout", mutate: func(cfg *ebs_fields.NoebsConfig) {
			cfg.WalletManualTransferApprovalTimeoutSeconds = 0
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validWalletRuntimeConfig()
			tt.mutate(&cfg)
			if err := validateRoleRuntimeConfig(serviceRoleWalletAPI, cfg); !errors.Is(err, errInvalidWalletConfig) {
				t.Fatalf("wallet-api runtime config error = %v, want %v", err, errInvalidWalletConfig)
			}
		})
	}
}

func TestServiceRoleRuntimeConfigRequiresExplicitTemporalConfig(t *testing.T) {
	cfg := validWalletRuntimeConfig()
	cfg.TemporalEnabled = false
	if err := validateRoleRuntimeConfig(serviceRoleWalletLedger, cfg); !errors.Is(err, errTemporalNotEnabled) {
		t.Fatalf("wallet-ledger temporal enabled error = %v, want %v", err, errTemporalNotEnabled)
	}

	cfg = validWalletRuntimeConfig()
	cfg.TemporalHost = ""
	if err := validateRoleRuntimeConfig(serviceRoleWalletLedger, cfg); !errors.Is(err, walletworker.ErrMissingTemporalHost) {
		t.Fatalf("wallet-ledger temporal host error = %v, want %v", err, walletworker.ErrMissingTemporalHost)
	}

	cfg = validWalletRuntimeConfig()
	cfg.TemporalPort = ""
	if err := validateRoleRuntimeConfig(serviceRoleWalletLedger, cfg); !errors.Is(err, walletworker.ErrMissingTemporalPort) {
		t.Fatalf("wallet-ledger temporal port error = %v, want %v", err, walletworker.ErrMissingTemporalPort)
	}

	cfg = validWalletRuntimeConfig()
	cfg.TemporalNamespace = ""
	if err := validateRoleRuntimeConfig(serviceRoleWalletLedger, cfg); !errors.Is(err, walletworker.ErrMissingTemporalNamespace) {
		t.Fatalf("wallet-ledger temporal namespace error = %v, want %v", err, walletworker.ErrMissingTemporalNamespace)
	}
}

func TestServiceRoleRuntimeConfigRequiresExplicitGRPCConfig(t *testing.T) {
	cfg := validWalletRuntimeConfig()
	cfg.GRPCEnabled = false
	if err := validateRoleRuntimeConfig(serviceRoleWalletLedger, cfg); !errors.Is(err, errGRPCNotEnabled) {
		t.Fatalf("wallet-ledger grpc enabled error = %v, want %v", err, errGRPCNotEnabled)
	}

	cfg = validWalletRuntimeConfig()
	cfg.GRPCPort = ""
	if err := validateRoleRuntimeConfig(serviceRoleWalletLedger, cfg); !errors.Is(err, errMissingGRPCPort) {
		t.Fatalf("wallet-ledger grpc port error = %v, want %v", err, errMissingGRPCPort)
	}

	cfg = validWalletRuntimeConfig()
	cfg.GRPCGatewayEnabled = true
	cfg.GRPCGatewayPort = ""
	if err := validateRoleRuntimeConfig(serviceRoleWalletLedger, cfg); !errors.Is(err, errMissingGRPCGateway) {
		t.Fatalf("wallet-ledger grpc gateway port error = %v, want %v", err, errMissingGRPCGateway)
	}
}

func TestServiceRoleRuntimeConfigRequiresExplicitWalletWorkerSchedules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ebs_fields.NoebsConfig)
		want   error
	}{
		{name: "poller_cron", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.WalletPSPPollerCron = "" }, want: errMissingWalletWorkflowCron},
		{name: "poller_batch", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.WalletPSPPollerBatchSize = 0 }, want: errInvalidWalletConfig},
		{name: "poller_interval", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.WalletPSPPollerIntervalSeconds = 0 }, want: errInvalidWalletConfig},
		{name: "reconciliation_cron", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.WalletReconciliationCron = "" }, want: errMissingWalletWorkflowCron},
		{name: "reconciliation_batch", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.WalletReconciliationBatchSize = 0 }, want: errInvalidWalletConfig},
		{name: "reconciliation_lookback", mutate: func(cfg *ebs_fields.NoebsConfig) { cfg.WalletReconciliationLookbackHours = 0 }, want: errInvalidWalletConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validWalletRuntimeConfig()
			tt.mutate(&cfg)
			if err := validateRoleRuntimeConfig(serviceRoleWalletWorker, cfg); !errors.Is(err, tt.want) {
				t.Fatalf("wallet-worker runtime config error = %v, want %v", err, tt.want)
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
		adminReports  bool
		dashboard     bool
		merchant      bool
		wallet        bool
		pspStore      bool
		walletPSPDeps bool
	}{
		{role: serviceRoleAPIGateway},
		{role: serviceRoleIdentityAuth, consumer: true},
		{role: serviceRoleCardVault, consumer: true},
		{role: serviceRoleEBSAdapter, consumer: true, merchant: true},
		{role: serviceRolePSPWebhook, pspStore: true, walletPSPDeps: true},
		{role: serviceRoleAdminReporting, adminReports: true, dashboard: true},
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
			if got := adminReportingService.Store != nil; got != tt.adminReports {
				t.Fatalf("adminReportingService initialized = %t, want %t", got, tt.adminReports)
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
			if got := pspWebhookStore != nil; got != tt.pspStore {
				t.Fatalf("pspWebhookStore initialized = %t, want %t", got, tt.pspStore)
			}
			pspDeps := walletPSPRegistry != nil && walletPSPLoader != nil
			if pspDeps != tt.walletPSPDeps {
				t.Fatalf("wallet PSP deps initialized = %t, want %t", pspDeps, tt.walletPSPDeps)
			}
		})
	}
}
