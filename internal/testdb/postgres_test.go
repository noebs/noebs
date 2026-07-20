package testdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/store"
)

func TestIsContainerRuntimeUnavailable(t *testing.T) {
	for _, message := range []string{
		"get provider: checked path: $XDG_RUNTIME_DIR",
		"checked path: $XDG_RUNTIME_DIR, failed to create Docker provider",
	} {
		err := WrapContainerRuntimeError(errors.New(message))
		if !IsContainerRuntimeUnavailable(err) {
			t.Fatalf("IsContainerRuntimeUnavailable(%v) = false, want true", err)
		}
	}
}

func TestWrapContainerRuntimeErrorLeavesDatabaseErrorsAlone(t *testing.T) {
	err := errors.New("postgres startup timeout")
	if wrapped := WrapContainerRuntimeError(err); wrapped != err {
		t.Fatalf("WrapContainerRuntimeError() = %v, want original error", wrapped)
	}
}

func TestCanonicalServiceDatabaseUsesAuthenticatedMigrationRole(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	postgres, err := StartPostgresContainer(ctx)
	if err != nil {
		if IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.Terminate(context.Background()) })

	const databaseName = "wallet_ledger"
	databaseURL, err := postgres.CreateDatabaseForRole(ctx, databaseName, "wallet_ledger_migrate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.DropDatabase(context.Background(), databaseName) })

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var database, currentUser, sessionUser, databaseOwner, schemaOwner string
	if err := db.QueryRowContext(ctx, `
		SELECT
			current_database(),
			current_user,
			session_user,
			pg_get_userbyid(database.datdba),
			pg_get_userbyid(schema.nspowner)
		FROM pg_database database
		JOIN pg_namespace schema ON schema.nspname = 'public'
		WHERE database.datname = current_database()
	`).Scan(&database, &currentUser, &sessionUser, &databaseOwner, &schemaOwner); err != nil {
		t.Fatal(err)
	}
	for label, got := range map[string]string{
		"current database": database,
		"current user":     currentUser,
		"session user":     sessionUser,
		"database owner":   databaseOwner,
		"schema owner":     schemaOwner,
	} {
		want := "wallet_ledger_migrate"
		if label == "current database" {
			want = databaseName
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", label, got, want)
		}
	}

	runtimeURL, err := postgres.DatabaseURLForRole(databaseName, "wallet_ledger_runtime")
	if err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := sql.Open("pgx", runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeDB.Close() })
	if err := runtimeDB.QueryRowContext(ctx, `SELECT current_user, session_user`).Scan(&currentUser, &sessionUser); err != nil {
		t.Fatal(err)
	}
	if currentUser != "wallet_ledger_runtime" || sessionUser != "wallet_ledger_runtime" {
		t.Fatalf("runtime identity = current:%q session:%q", currentUser, sessionUser)
	}
}

func TestRoleConvergenceRemovesRoleAndSettingDrift(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	postgres, err := StartPostgresContainer(ctx)
	if err != nil {
		if IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.Terminate(context.Background()) })

	admin, err := sql.Open("pgx", postgres.adminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	for _, statement := range []string{
		`DROP ROLE IF EXISTS noebs_test_role_member`,
		`DROP ROLE IF EXISTS noebs_test_role_granted`,
		`CREATE ROLE noebs_test_role_member NOLOGIN`,
		`CREATE ROLE noebs_test_role_granted NOLOGIN`,
		`GRANT wallet_ledger_runtime TO noebs_test_role_member`,
		`GRANT noebs_test_role_granted TO wallet_ledger_runtime`,
		`ALTER ROLE wallet_ledger_runtime NOLOGIN CONNECTION LIMIT 2 VALID UNTIL '2000-01-01'`,
		`ALTER ROLE wallet_ledger_runtime SET search_path = pg_catalog`,
	} {
		if _, err := admin.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP ROLE IF EXISTS noebs_test_role_member`)
		_, _ = admin.ExecContext(context.Background(), `DROP ROLE IF EXISTS noebs_test_role_granted`)
	})

	if err := postgres.ensureApplicationRoles(ctx); err != nil {
		t.Fatal(err)
	}
	var canLogin, restricted bool
	var connectionLimit, memberships, settings int
	if err := admin.QueryRowContext(ctx, `
		SELECT
			role.rolcanlogin,
			NOT role.rolsuper
				AND NOT role.rolcreatedb
				AND NOT role.rolcreaterole
				AND NOT role.rolinherit
				AND NOT role.rolreplication
				AND NOT role.rolbypassrls
				AND role.rolvaliduntil = 'infinity'::timestamptz,
			role.rolconnlimit,
			(
				SELECT count(*)
				FROM pg_auth_members membership
				WHERE membership.roleid = role.oid OR membership.member = role.oid
			),
			(
				SELECT count(*)
				FROM pg_db_role_setting setting
				WHERE setting.setrole = role.oid
			)
		FROM pg_roles role
		WHERE role.rolname = 'wallet_ledger_runtime'
	`).Scan(&canLogin, &restricted, &connectionLimit, &memberships, &settings); err != nil {
		t.Fatal(err)
	}
	if !canLogin || !restricted || connectionLimit != -1 || memberships != 0 || settings != 0 {
		t.Fatalf(
			"converged role = login:%v restricted:%v connection_limit:%d memberships:%d settings:%d",
			canLogin,
			restricted,
			connectionLimit,
			memberships,
			settings,
		)
	}
}

func TestDistinctServiceScopesMigrateConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	postgres, err := StartPostgresContainer(ctx)
	if err != nil {
		if IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.Terminate(context.Background()) })

	tests := []struct {
		database string
		role     string
		scope    string
	}{
		{database: "identity_auth", role: "identity_auth_migrate", scope: store.MigrationScopeIdentityAuth},
		{database: "wallet_ledger", role: "wallet_ledger_migrate", scope: store.MigrationScopeWalletLedger},
	}
	for _, test := range tests {
		database := test.database
		t.Cleanup(func() { _ = postgres.DropDatabase(context.Background(), database) })
	}

	start := make(chan struct{})
	results := make(chan error, len(tests))
	var migrations sync.WaitGroup
	for _, test := range tests {
		test := test
		migrations.Add(1)
		go func() {
			defer migrations.Done()
			<-start
			databaseURL, err := postgres.CreateDatabaseForRole(ctx, test.database, test.role)
			if err != nil {
				results <- fmt.Errorf("create %s: %w", test.database, err)
				return
			}
			db, err := store.OpenFromConfig(databaseURL, store.DriverPostgres)
			if err != nil {
				results <- fmt.Errorf("open %s: %w", test.database, err)
				return
			}
			defer func() { _ = db.Close() }()
			if err := store.MigrateScope(ctx, db, test.scope); err != nil {
				results <- fmt.Errorf("migrate %s: %w", test.scope, err)
				return
			}
			results <- nil
		}()
	}
	close(start)
	migrations.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
}
