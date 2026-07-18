package workloadauth_test

import (
	"context"
	"fmt"
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
	databaseName := fmt.Sprintf("workload_nonce_%d", time.Now().UnixNano())
	databaseURL, err := postgres.CreateDatabase(ctx, databaseName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.DropDatabase(context.Background(), databaseName) })
	db, err := store.OpenFromConfig(databaseURL, store.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.MigrateScope(ctx, db, "test-tenant", store.MigrationScopeWorkloadAuth); err != nil {
		t.Fatal(err)
	}
	first, err := workloadauth.NewPostgresNonceStore(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	second, err := workloadauth.NewPostgresNonceStore(db.DB)
	if err != nil {
		t.Fatal(err)
	}

	const attempts = 64
	start := make(chan struct{})
	var successes atomic.Int32
	var failures atomic.Int32
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
		t.Fatalf("failures=%d successes=%d, want 0/1", failures.Load(), successes.Load())
	}
	used, err := first.Use(ctx, "gateway-2026-07-a", "identity-auth", "shared-nonce", time.Now().Add(90*time.Second))
	if err != nil || !used {
		t.Fatalf("same nonce in another audience = %t, %v", used, err)
	}
	var count int
	if err := db.GetContext(ctx, &count, `SELECT count(*) FROM workload_request_nonces WHERE key_id = $1 AND nonce = $2`, "gateway-2026-07-a", "shared-nonce"); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("audited nonce rows = %d, want 2", count)
	}
}
