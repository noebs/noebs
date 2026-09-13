package accountenrollment_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
)

func TestEnrollmentPostgresSerializesReplicasAndPersistsPartialProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	postgres, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("PostgreSQL fixture unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.Terminate(context.Background()) })
	databaseURL, err := postgres.CreateDatabaseForRole(ctx, "identity_auth", "identity_auth_migrate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.DropDatabase(context.Background(), "identity_auth") })
	migrationDB, err := store.OpenFromConfig(databaseURL, store.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateScope(ctx, migrationDB, store.MigrationScopeIdentityAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := migrationDB.ExecContext(ctx, `INSERT INTO tenants(id,name,created_at) VALUES ('tenant-a','Tenant A',clock_timestamp()) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	_ = migrationDB.Close()
	runtimeURL, err := postgres.DatabaseURLForRole("identity_auth", "identity_auth_runtime")
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenFromConfig(runtimeURL, store.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	first, err := accountenrollment.NewPostgresStore(db.DB.DB)
	if err != nil {
		t.Fatal(err)
	}
	second, err := accountenrollment.NewPostgresStore(db.DB.DB)
	if err != nil {
		t.Fatal(err)
	}
	id := accountenrollment.Identity{Issuer: "https://example.test/auth/realms/noebs", Subject: "synthetic-subject", TenantID: "tenant-a"}
	wantFailure := errors.New("upstream failed after durable progress")
	err = first.WithIdentity(ctx, id, func(access accountenrollment.ProgressAccess) error {
		if err := access.Save(ctx, accountenrollment.Progress{Complete: false}); err != nil {
			return err
		}
		return wantFailure
	})
	if !errors.Is(err, wantFailure) {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			selected := first
			if index%2 == 1 {
				selected = second
			}
			if err := selected.WithIdentity(ctx, id, func(access accountenrollment.ProgressAccess) error {
				progress, err := access.Load(ctx)
				if err != nil {
					return err
				}
				if progress == nil {
					return errors.New("partial progress lost")
				}
				progress.Complete = true
				return access.Save(ctx, *progress)
			}); err != nil {
				t.Error(err)
			}
		}(index)
	}
	group.Wait()
	if err := second.WithIdentity(ctx, id, func(access accountenrollment.ProgressAccess) error {
		p, err := access.Load(ctx)
		if err == nil && (p == nil || !p.Complete) {
			t.Error("completion receipt not durable")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
