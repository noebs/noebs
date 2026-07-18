package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
)

var testPostgres *testdb.PostgresContainer
var testDBName string
var testConfigPath string

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			fmt.Fprintf(os.Stderr, "skip cli integration tests: %v\n", err)
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "start postgres testcontainer: %v\n", err)
		os.Exit(1)
	}
	testPostgres = container

	dbName := fmt.Sprintf("noebs_cli_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
	if err != nil {
		panic(fmt.Sprintf("create test db: %v", err))
	}
	testDBName = dbName
	testConfigPath = filepath.Join(".", "config.test.yaml")
	configPayload := fmt.Sprintf(`noebs:
  db_url: %q
  db_driver: %q
  default_tenant_id: %q
  service_role: %q
  jwt_secret: "test-jwt-secret"
  sms_key: "test-sms-key"
  sms_sender: "NOEBS"
  sms_gateway: "https://sms.example/send?"
  sms_message: "code"
  google_client_id: "test-google-client-id"
  google_client_secret: "test-google-client-secret"
  google_redirect_url: "https://app.example/auth/google/callback"
  service_discovery:
    identity-auth: "http://127.0.0.1:1"
    card-vault: "http://127.0.0.1:1"
    ebs-adapter: "http://127.0.0.1:1"
    psp-webhook: "http://127.0.0.1:1"
    admin-reporting: "http://127.0.0.1:1"
    notification-chat: "http://127.0.0.1:1"
    consumer-beneficiary: "http://127.0.0.1:1"
    wallet-api: "http://127.0.0.1:1"
  grpc_service_discovery:
    wallet-ledger: "127.0.0.1:1"
  kafka_brokers:
    - "127.0.0.1:9092"
  kafka_transaction_topic: "test-ebs-transactions"
  admin_reporting_kafka_consumer_group: "test-admin-reporting-projector"
  ebs_transaction_event_publisher_batch_size: 10
  ebs_transaction_event_publisher_poll_interval_ms: 1000
  workload_auth:
    signing_key_id: %q
    signing_private_key: %q
    nonce_db_url: %q
    trusted_keys:
      %q:
        caller: %q
        public_key: %q
      %q:
        caller: %q
        public_key: %q
      %q:
        caller: %q
        public_key: %q
`, dbURL, "postgres", "test-tenant", serviceRoleIdentityAuth,
		testWorkloadKeyID(string(serviceRoleIdentityAuth)),
		base64.StdEncoding.EncodeToString(testWorkloadPrivateKey(string(serviceRoleIdentityAuth))),
		dbURL,
		testWorkloadKeyID(string(serviceRoleAPIGateway)), string(serviceRoleAPIGateway), base64.StdEncoding.EncodeToString(testWorkloadPrivateKey(string(serviceRoleAPIGateway)).Public().(ed25519.PublicKey)),
		testWorkloadKeyID(string(serviceRoleEBSAdapter)), string(serviceRoleEBSAdapter), base64.StdEncoding.EncodeToString(testWorkloadPrivateKey(string(serviceRoleEBSAdapter)).Public().(ed25519.PublicKey)),
		testWorkloadKeyID(string(serviceRoleNotification)), string(serviceRoleNotification), base64.StdEncoding.EncodeToString(testWorkloadPrivateKey(string(serviceRoleNotification)).Public().(ed25519.PublicKey)),
	)
	if err := os.WriteFile(testConfigPath, []byte(configPayload), 0o644); err != nil {
		panic(fmt.Sprintf("write test config: %v", err))
	}
	db, err := store.OpenFromConfig(dbURL, store.DriverPostgres)
	if err != nil {
		panic(fmt.Sprintf("open test db for migration job: %v", err))
	}
	if err := migrateAllServiceScopes(ctx, db, "test-tenant"); err != nil {
		panic(fmt.Sprintf("run test migration jobs: %v", err))
	}
	if err := store.New(db).EnsureTenant(ctx, "test-tenant"); err != nil {
		panic(fmt.Sprintf("ensure test tenant: %v", err))
	}
	if err := db.Close(); err != nil {
		panic(fmt.Sprintf("close migration db: %v", err))
	}

	code := m.Run()
	if testConfigPath != "" {
		_ = os.Remove(testConfigPath)
	}
	if testPostgres != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if testDBName != "" {
			_ = testPostgres.DropDatabase(cleanupCtx, testDBName)
		}
		_ = testPostgres.Terminate(cleanupCtx)
	}
	os.Exit(code)
}

func migrateAllServiceScopes(ctx context.Context, db *store.DB, tenantID string) error {
	for _, scope := range []string{
		store.MigrationScopeIdentityAuth,
		store.MigrationScopeCardVault,
		store.MigrationScopeEBSAdapter,
		store.MigrationScopePSPWebhook,
		store.MigrationScopeAdminReporting,
		store.MigrationScopeNotificationChat,
		store.MigrationScopeConsumerBeneficiary,
		store.MigrationScopeWalletLedger,
		store.MigrationScopeWorkloadAuth,
	} {
		if err := store.MigrateScope(ctx, db, tenantID, scope); err != nil {
			return fmt.Errorf("%s: %w", scope, err)
		}
	}
	return nil
}
