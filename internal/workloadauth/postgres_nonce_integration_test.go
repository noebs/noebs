package workloadauth_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/adonese/noebs/store"
)

func TestPostgresNonceStoreIsAtomicAcrossInstancesAndAudiences(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	postgres, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.Terminate(context.Background()) })
	const databaseName = "workload_auth"
	databaseURL, err := postgres.CreateDatabaseForRole(ctx, databaseName, "workload_auth_migrate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.DropDatabase(context.Background(), databaseName) })
	migrationDB, err := store.OpenFromConfig(databaseURL, store.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateScope(ctx, migrationDB, store.MigrationScopeWorkloadAuth); err != nil {
		_ = migrationDB.Close()
		t.Fatal(err)
	}
	if err := migrationDB.Close(); err != nil {
		t.Fatal(err)
	}
	runtimeURL, err := postgres.DatabaseURLForRole(databaseName, "workload_auth_runtime")
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenFromConfig(runtimeURL, store.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	first, err := workloadauth.NewPostgresNonceStore(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	second, err := workloadauth.NewPostgresNonceStore(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	var currentUser, sessionUser string
	var canUseSchema, canInsert, canSelect, canReadConflictKey, canReadExpiry bool
	if err := db.QueryRowContext(ctx, `
		SELECT
			current_user,
			session_user,
			has_schema_privilege(current_user, 'public', 'USAGE'),
			has_table_privilege(current_user, 'public.workload_request_nonces', 'INSERT'),
			has_table_privilege(current_user, 'public.workload_request_nonces', 'SELECT'),
			has_column_privilege(current_user, 'public.workload_request_nonces', 'key_id', 'SELECT')
				AND has_column_privilege(current_user, 'public.workload_request_nonces', 'audience', 'SELECT')
				AND has_column_privilege(current_user, 'public.workload_request_nonces', 'nonce', 'SELECT'),
			has_column_privilege(current_user, 'public.workload_request_nonces', 'expires_at', 'SELECT')
	`).Scan(&currentUser, &sessionUser, &canUseSchema, &canInsert, &canSelect, &canReadConflictKey, &canReadExpiry); err != nil {
		t.Fatal(err)
	}
	if currentUser != "workload_auth_runtime" || sessionUser != currentUser || !canUseSchema || !canInsert || canSelect || canReadConflictKey || canReadExpiry {
		t.Fatalf(
			"runtime authority = current:%q session:%q schema_usage:%v insert:%v table_select:%v conflict_key_select:%v expiry_select:%v",
			currentUser,
			sessionUser,
			canUseSchema,
			canInsert,
			canSelect,
			canReadConflictKey,
			canReadExpiry,
		)
	}

	const attempts = 64
	start := make(chan struct{})
	var successes atomic.Int32
	var failures atomic.Int32
	var firstFailure error
	var failureOnce sync.Once
	var wg sync.WaitGroup
	for attempt := 0; attempt < attempts; attempt++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			storeInstance := first
			if index%2 == 1 {
				storeInstance = second
			}
			used, err := storeInstance.Use(ctx, "gateway-2026-07-a", "card-vault", "shared-nonce", time.Now().Add(90*time.Second))
			if err != nil {
				failures.Add(1)
				failureOnce.Do(func() { firstFailure = err })
				return
			}
			if used {
				successes.Add(1)
			}
		}(attempt)
	}
	close(start)
	wg.Wait()
	if failures.Load() != 0 || successes.Load() != 1 {
		t.Fatalf("failures=%d successes=%d first_error=%v, want 0/1", failures.Load(), successes.Load(), firstFailure)
	}
	used, err := first.Use(ctx, "gateway-2026-07-a", "identity-auth", "shared-nonce", time.Now().Add(90*time.Second))
	if err != nil || !used {
		t.Fatalf("same nonce in another audience = %t, %v", used, err)
	}
	auditDB, err := store.OpenFromConfig(databaseURL, store.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = auditDB.Close() })
	var count int
	if err := auditDB.GetContext(ctx, &count, `SELECT count(*) FROM workload_request_nonces WHERE key_id = $1 AND nonce = $2`, "gateway-2026-07-a", "shared-nonce"); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("audited nonce rows = %d, want 2", count)
	}
}
