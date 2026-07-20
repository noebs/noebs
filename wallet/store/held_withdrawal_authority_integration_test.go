package store

import (
	"context"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
)

func TestWorkerAuthorityExecutesHeldWithdrawalSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	const databaseName = "wallet_ledger"
	migrationURL, err := container.CreateDatabaseForRole(ctx, databaseName, "wallet_ledger_migrate")
	if err != nil {
		t.Fatal(err)
	}
	migrationDB, err := basestore.OpenFromConfig(migrationURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = migrationDB.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, databaseName)
	})
	if err := basestore.MigrateScope(ctx, migrationDB, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatal(err)
	}
	const tenantID = "tenant-held-authority"
	provisionWalletStoreTestTenant(t, ctx, migrationDB, tenantID, "Held Withdrawal Authority Tenant")
	migrationStore := New(migrationDB)
	fixture := newHeldWithdrawalFixture(t, ctx, migrationStore, migrationDB, tenantID, true, true)

	workerURL, err := container.DatabaseURLForRole(databaseName, "wallet_ledger_worker")
	if err != nil {
		t.Fatal(err)
	}
	workerDB, err := basestore.OpenFromConfig(workerURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerDB.Close() })
	workerStore := New(workerDB)

	posted, err := workerStore.PostHeldWithdrawalSettlement(ctx, fixture.Params)
	if err != nil {
		t.Fatalf("worker held withdrawal settlement: %v", err)
	}
	if posted.Existing || len(posted.Transfers) != 2 {
		t.Fatalf("worker settlement = %+v, want new two-leg settlement", posted)
	}
	assertHeldWithdrawalCommitted(t, ctx, fixture, posted.TransactionID, 4)
}
