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
	"strings"
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
var testInternalTransport preparedInternalTransportRelease
var testRuntimeDir string

const unavailableCLIDatabaseURL = "postgres://gateway_auth_runtime:test@127.0.0.1:1/gateway_auth?sslmode=disable&connect_timeout=1"

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
	testInternalTransport = transport
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

	testRoot, pathErr := os.Getwd()
	if pathErr != nil {
		failCLITestSetup("resolve CLI test root: %v", pathErr)
	}
	testRuntimeDir, pathErr = os.MkdirTemp("", "noebs-cli-test-")
	if pathErr != nil {
		failCLITestSetup("create CLI test runtime directory: %v", pathErr)
	}
	testConfigPath = filepath.Join(testRuntimeDir, "config.test.yaml")
	if err := os.Setenv(cliTestRootEnv, testRoot); err != nil {
		failCLITestSetup("configure CLI test root: %v", err)
	}
	if err := os.Setenv(cliTestConfigPathEnv, testConfigPath); err != nil {
		failCLITestSetup("configure CLI test config path: %v", err)
	}
	if err := writeCLITestConfig(cliTestConfig(role, testTLSDatabaseURL(dbURL))); err != nil {
		failCLITestSetup("write CLI test config: %v", err)
	}
	testTenantCatalogPath = filepath.Join(testRuntimeDir, "tenant-catalog.test.yaml")
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
	testDBName = "gateway_auth"
	migrationURL, err := container.CreateDatabaseForRole(ctx, testDBName, "gateway_auth_migrate")
	if err != nil {
		return "", fmt.Errorf("create database: %w", err)
	}
	db, err := store.OpenFromConfig(migrationURL, store.DriverPostgres)
	if err != nil {
		return "", fmt.Errorf("open migration database: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := store.MigrateScope(ctx, db, store.MigrationScopeGatewayAuth); err != nil {
		return "", fmt.Errorf("run gateway-auth migration job: %w", err)
	}
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "test-tenant", Name: "CLI Test Tenant"}})
	if err != nil {
		return "", fmt.Errorf("build gateway-auth test tenant catalog: %w", err)
	}
	if err := store.New(db).ProvisionTenantCatalog(ctx, catalog); err != nil {
		return "", fmt.Errorf("provision gateway-auth test tenant catalog: %w", err)
	}
	runtimeURL, err := container.DatabaseURLForRole(testDBName, "gateway_auth_runtime")
	if err != nil {
		return "", fmt.Errorf("resolve gateway-auth runtime database URL: %w", err)
	}
	runtimeDB, err := store.OpenFromConfig(runtimeURL, store.DriverPostgres)
	if err != nil {
		return "", fmt.Errorf("open runtime database: %w", err)
	}
	database = runtimeDB
	storeSvc = store.New(runtimeDB, store.WithDataKey("test-data-key"))
	openServiceDatabase = func(_, _, _ string, _ postgresRoleSpec) (*store.DB, error) { return runtimeDB, nil }
	return runtimeURL, nil
}

func testTLSDatabaseURL(raw string) string {
	return strings.Replace(raw, "sslmode=disable", "sslmode=verify-full", 1)
}

func cliTestConfig(role serviceRole, dbURL string) ebs_fields.NoebsConfig {
	cfg := ebs_fields.NoebsConfig{
		ServiceRole:     string(role),
		DatabaseURL:     dbURL,
		DatabaseDriver:  "postgres",
		DefaultTenantID: "test-tenant",
		DataKey:         "test-data-key",
		ServiceDiscovery: map[string]string{
			string(serviceRoleIdentityAuth):   "https://127.0.0.1:1",
			string(serviceRoleCardVault):      "https://127.0.0.1:1",
			string(serviceRoleEBSAdapter):     "https://127.0.0.1:1",
			string(serviceRolePSPWebhook):     "https://127.0.0.1:1",
			string(serviceRoleAdminReporting): "https://127.0.0.1:1",
			string(serviceRoleNotification):   "https://127.0.0.1:1",
			string(serviceRoleWalletAPI):      "https://127.0.0.1:1",
		},
		GRPCServiceDiscovery:                       map[string]string{string(serviceRoleWalletLedger): "127.0.0.1:1"},
		KafkaBrokers:                               []string{"127.0.0.1:9092"},
		KafkaTransactionTopic:                      "test-ebs-transactions",
		AdminReportingKafkaConsumerGroup:           "test-admin-reporting-projector",
		EBSTransactionEventPublisherBatchSize:      10,
		EBSTransactionEventPublisherPollIntervalMs: 1000,
		WalletEnabled:                              true,
		WalletDefaultCurrency:                      "USD",
		WalletApprovalThreshold:                    100_000,
		WalletHoldExpirySeconds:                    3600,
		WalletApprovalTimeoutSeconds:               3600,
		WalletManualTransferApprovalTimeoutSeconds: 3600,
	}
	if identity, ok := testInternalTransport.services[role]; ok {
		cfg.InternalTransport = identity
	}
	if dbURL != "" {
		cfg.DatabaseCACertificate = testKeycloakCACertificate
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
		cfg.WalletAuthorizerClientSecret = "test-wallet-authorizer-client-secret"
		cfg.WalletAuthorizerRedirectURL = "https://app.example/wallet/authorizations/oauth/callback"
		cfg.GatewayAuthEncryptionKeyID = "test-key"
		cfg.GatewayAuthEncryptionKeys = map[string]string{
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
	openServiceDatabase = func(_, _, _ string, _ postgresRoleSpec) (*store.DB, error) { return database, nil }
	return nil
}

func cleanupCLITestEnvironment() {
	if database != nil {
		_ = database.Close()
	}
	_ = os.Unsetenv(cliTestRootEnv)
	_ = os.Unsetenv(cliTestConfigPathEnv)
	if testRuntimeDir != "" {
		_ = os.RemoveAll(testRuntimeDir)
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
