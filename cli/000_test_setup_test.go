package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
)

var testPostgres *testdb.PostgresContainer
var testDBName string

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
	if err := os.Setenv("NOEBS_TEST_DB_URL", dbURL); err != nil {
		panic(fmt.Sprintf("set NOEBS_TEST_DB_URL: %v", err))
	}
	_ = os.Setenv("NOEBS_TEST_DB_DRIVER", "postgres")
	_ = os.Setenv("NOEBS_TEST_TENANT", "test-tenant")

	code := m.Run()
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
