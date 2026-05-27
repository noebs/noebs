package main

import (
	"context"
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skipping cli tests: postgres testcontainer unavailable: %v\n", err)
		os.Exit(0)
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
  service_discovery:
    identity-auth: "http://127.0.0.1:1"
    card-vault: "http://127.0.0.1:1"
    ebs-adapter: "http://127.0.0.1:1"
    psp-webhook: "http://127.0.0.1:1"
    admin-reporting: "http://127.0.0.1:1"
    notification-chat: "http://127.0.0.1:1"
    consumer-beneficiary: "http://127.0.0.1:1"
    wallet-api: "http://127.0.0.1:1"
`, dbURL, "postgres", "test-tenant", serviceRoleIdentityAuth)
	if err := os.WriteFile(testConfigPath, []byte(configPayload), 0o644); err != nil {
		panic(fmt.Sprintf("write test config: %v", err))
	}
	db, err := store.OpenFromConfig(dbURL, "", store.DriverPostgres)
	if err != nil {
		panic(fmt.Sprintf("open test db for migration job: %v", err))
	}
	if err := store.Migrate(ctx, db, "test-tenant"); err != nil {
		panic(fmt.Sprintf("run test migration job: %v", err))
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
