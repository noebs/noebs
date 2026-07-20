package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/adonese/noebs/store"
	"github.com/jmoiron/sqlx"
)

var testPostgres *testdb.PostgresContainer
var testDBName string
var testConfigPath string
var testTenantCatalogPath string
var testKeycloakCACertificate string

const unavailableCLIDatabaseURL = "postgres://noebs:noebs@127.0.0.1:1/noebs_cli_unavailable?sslmode=disable&connect_timeout=1"

func TestMain(m *testing.M) {
	now := time.Now().UTC()
	transportInputs, transportErr := generateTestInternalTransportInputs(now)
	if transportErr != nil {
		failCLITestSetup("generate test Keycloak transport CA: %v", transportErr)
	}
	transport, transportErr := prepareInternalTransportRelease(transportInputs, rand.Reader, now)
	if transportErr != nil {
		failCLITestSetup("generate test Keycloak transport CA: %v", transportErr)
	}
	testKeycloakCACertificate = transport.caCertificate
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	dbURL, postgresErr := startCLITestPostgres(ctx)
	cancel()
	role := serviceRoleAPIGateway
	if postgresErr != nil {
		if !testdb.IsContainerRuntimeUnavailable(postgresErr) {
			failCLITestSetup("start PostgreSQL test environment: %v", postgresErr)
		}
		dbURL = unavailableCLIDatabaseURL
		fmt.Fprintf(os.Stderr, "PostgreSQL unavailable; running database-independent CLI tests: %v\n", postgresErr)
	}

	testConfigPath = filepath.Join(".", "config.test.yaml")
	if err := writeCLITestConfig(cliTestConfig(role, dbURL)); err != nil {
		failCLITestSetup("write CLI test config: %v", err)
	}
	testTenantCatalogPath = filepath.Join(".", "tenant-catalog.test.yaml")
	if err := os.WriteFile(testTenantCatalogPath, []byte("api_version: noebs.sd/tenants/v1\ntenants:\n  - id: test-tenant\n    name: CLI Test Tenant\n"), 0o644); err != nil {
		failCLITestSetup("write CLI test tenant catalog: %v", err)
	}
	tenantCatalogFilePath = testTenantCatalogPath
	if postgresErr != nil {
		// Tests that initialize database-owning roles still receive a non-nil
		// store, but any actual SQL call fails at that test's database boundary
		// instead of panicking. Pure tests do not initialize the application.
		if err := installUnavailableCLIDatabase(); err != nil {
			failCLITestSetup("configure unavailable test database: %v", err)
		}
	}
	testCatalog, catalogErr := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "test-tenant", Name: "CLI Test Tenant"}})
	if catalogErr != nil {
		failCLITestSetup("configure test tenant catalog: %v", catalogErr)
	}
	runtimeTenantCatalog = testCatalog

	code := m.Run()
	cleanupCLITestEnvironment()
	os.Exit(code)
}

func startCLITestPostgres(ctx context.Context) (string, error) {
	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		return "", err
	}
	testPostgres = container
	testDBName = fmt.Sprintf("noebs_cli_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, testDBName)
	if err != nil {
		return "", fmt.Errorf("create database: %w", err)
	}
	db, err := store.OpenFromConfig(dbURL, store.DriverPostgres)
	if err != nil {
		return "", fmt.Errorf("open migration database: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := provisionCLIGatewayAuthRoles(ctx, db); err != nil {
		return "", fmt.Errorf("provision gateway authentication roles: %w", err)
	}
	if err := migrateAllServiceScopes(ctx, db, "test-tenant"); err != nil {
		return "", fmt.Errorf("run migration jobs: %w", err)
	}
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "test-tenant", Name: "CLI Test Tenant"}})
	if err != nil {
		return "", err
	}
	if err := store.New(db).ProvisionTenantCatalog(ctx, catalog); err != nil {
		return "", fmt.Errorf("provision test tenant: %w", err)
	}
	return dbURL, nil
}

func provisionCLIGatewayAuthRoles(ctx context.Context, db *store.DB) error {
	for _, statement := range []string{
		"CREATE ROLE gateway_auth_runtime LOGIN PASSWORD 'gateway-auth-runtime-test-password'",
		"CREATE ROLE gateway_auth_cleanup LOGIN PASSWORD 'gateway-auth-cleanup-test-password'",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func cliTestConfig(role serviceRole, dbURL string) ebs_fields.NoebsConfig {
	cfg := ebs_fields.NoebsConfig{
		ServiceRole:     string(role),
		DatabaseURL:     dbURL,
		DatabaseDriver:  "postgres",
		DefaultTenantID: "test-tenant",
		DataKey:         "test-data-key",
		ServiceDiscovery: map[string]string{
			string(serviceRoleIdentityAuth):   "http://127.0.0.1:1",
			string(serviceRoleCardVault):      "http://127.0.0.1:1",
			string(serviceRoleEBSAdapter):     "http://127.0.0.1:1",
			string(serviceRolePSPWebhook):     "http://127.0.0.1:1",
			string(serviceRoleAdminReporting): "http://127.0.0.1:1",
			string(serviceRoleNotification):   "http://127.0.0.1:1",
			string(serviceRoleWalletAPI):      "http://127.0.0.1:1",
		},
		GRPCServiceDiscovery:                       map[string]string{string(serviceRoleWalletLedger): "127.0.0.1:1"},
		KafkaBrokers:                               []string{"127.0.0.1:9092"},
		KafkaTransactionTopic:                      "test-ebs-transactions",
		AdminReportingKafkaConsumerGroup:           "test-admin-reporting-projector",
		EBSTransactionEventPublisherBatchSize:      10,
		EBSTransactionEventPublisherPollIntervalMs: 1000,
	}

	privateKey := testWorkloadPrivateKey(string(role))
	cfg.WorkloadAuth.SigningKeyID = testWorkloadKeyID(string(role))
	cfg.WorkloadAuth.SigningPrivateKey = base64.StdEncoding.EncodeToString(privateKey)
	if role == serviceRoleIdentityAuth {
		gatewayKeyID := testWorkloadKeyID(string(serviceRoleAPIGateway))
		cfg.WorkloadAuth.NonceDatabaseURL = dbURL
		cfg.WorkloadAuth.TrustedKeys = map[string]workloadauth.TrustedKeyConfig{
			gatewayKeyID: {
				Caller:    string(serviceRoleAPIGateway),
				PublicKey: base64.StdEncoding.EncodeToString(testWorkloadPrivateKey(string(serviceRoleAPIGateway)).Public().(ed25519.PublicKey)),
			},
		}
	}
	if role == serviceRoleAPIGateway {
		cfg.PSPWebhookRoutes = map[string]ebs_fields.PSPWebhookRoute{
			testPSPWebhookCallbackID: {
				TenantID:     "test-tenant",
				ProviderCode: "test-provider",
			},
		}
		cfg.OIDC.Issuer = "https://identity.example/realms/noebs"
		cfg.OIDC.JWKSURL = "https://127.0.0.1:1/realms/noebs/protocol/openid-connect/certs"
		cfg.OIDC.Audience = "noebs-api"
		cfg.OIDC.AllowedClients = []string{"noebs-mobile", "noebs-backoffice"}
		cfg.OIDC.AccessTokenType = "Bearer"
		cfg.OIDC.MaxFutureIssuedAtSeconds = 5
		cfg.OIDC.JWKSRefreshSeconds = 300
		cfg.OIDC.UnknownKeyRefreshIntervalSeconds = 30
		cfg.KeycloakCACertificate = testKeycloakCACertificate
		cfg.BackofficeClientSecret = "test-backoffice-client-secret"
		cfg.BackofficeRedirectURL = "https://app.example/backoffice/oauth/callback"
		cfg.BackofficePostLogoutURL = "https://app.example/backoffice/oauth/logout/callback"
		cfg.BackofficeEncryptionKeyID = "test-key"
		cfg.BackofficeEncryptionKeys = map[string]string{
			"test-key": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)),
		}
	}
	return cfg
}

func writeCLITestConfig(cfg ebs_fields.NoebsConfig) error {
	payload, err := json.MarshalIndent(struct {
		Noebs ebs_fields.NoebsConfig `json:"noebs"`
	}{Noebs: cfg}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(testConfigPath, payload, 0o644)
}

func installUnavailableCLIDatabase() error {
	db, err := sqlx.Open(store.DriverPostgres, unavailableCLIDatabaseURL)
	if err != nil {
		return err
	}
	database = &store.DB{DB: db, Driver: store.DriverPostgres}
	storeSvc = store.New(database, store.WithDataKey("test-data-key"))
	openServiceDatabase = func(_, _, _ string) (*store.DB, error) { return database, nil }
	return nil
}

func cleanupCLITestEnvironment() {
	if database != nil {
		_ = database.Close()
	}
	if testConfigPath != "" {
		_ = os.Remove(testConfigPath)
	}
	if testTenantCatalogPath != "" {
		_ = os.Remove(testTenantCatalogPath)
	}
	if testPostgres == nil {
		return
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	if testDBName != "" {
		_ = testPostgres.DropDatabase(cleanupCtx, testDBName)
	}
	_ = testPostgres.Terminate(cleanupCtx)
}

func failCLITestSetup(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	cleanupCLITestEnvironment()
	os.Exit(1)
}

func migrateAllServiceScopes(ctx context.Context, db *store.DB, tenantID string) error {
	for _, scope := range []string{
		store.MigrationScopeIdentityAuth,
		store.MigrationScopeCardVault,
		store.MigrationScopeEBSAdapter,
		store.MigrationScopePSPWebhook,
		store.MigrationScopeAdminReporting,
		store.MigrationScopeNotificationChat,
		store.MigrationScopeWalletLedger,
		store.MigrationScopeWorkloadAuth,
		store.MigrationScopeGatewayAuth,
	} {
		if err := store.MigrateScope(ctx, db, scope); err != nil {
			return fmt.Errorf("%s: %w", scope, err)
		}
	}
	return nil
}
