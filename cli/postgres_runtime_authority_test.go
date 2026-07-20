package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/store"
)

func TestValidatePostgresDatabaseIdentity(t *testing.T) {
	spec := postgresRoleSpecForTest(t, serviceRoleAPIGateway)
	for _, databaseURL := range []string{
		"postgres://gateway_auth_runtime:secret@postgres:5432/gateway_auth?sslmode=disable",
		"postgresql://gateway_auth_runtime:secret@postgres:5432/gateway_auth?connect_timeout=5&sslmode=verify-full",
	} {
		if err := validatePostgresDatabaseIdentity(databaseURL, spec); err != nil {
			t.Fatalf("validatePostgresDatabaseIdentity(%q) error = %v", databaseURL, err)
		}
	}

	invalid := []string{
		"mysql://gateway_auth_runtime:secret@postgres:5432/gateway_auth",
		"postgres:///gateway_auth",
		"postgres://identity_auth_runtime:secret@postgres:5432/gateway_auth",
		"postgres://gateway_auth_runtime:secret@postgres:5432/identity_auth",
		"postgres://gateway_auth_runtime:secret@postgres:5432/gateway_auth#fragment",
		"postgres://gateway_auth_runtime:secret@postgres:5432/gateway_auth?user=identity_auth_runtime",
		"postgres://gateway_auth_runtime:secret@postgres:5432/gateway_auth?database=identity_auth",
		"postgres://gateway_auth_runtime:secret@postgres:5432/gateway_auth?options=-c%20role%3Didentity_auth_runtime",
		"postgres://gateway_auth_runtime:secret@postgres:5432/gateway_auth?host=elsewhere",
		"postgres://gateway_auth_runtime:secret@postgres:5432/gateway_auth?sslmode=verify-full&sslmode=disable",
	}
	for _, databaseURL := range invalid {
		t.Run(databaseURL, func(t *testing.T) {
			if err := validatePostgresDatabaseIdentity(databaseURL, spec); !errors.Is(err, errPostgresDatabaseIdentity) {
				t.Fatalf("error = %v, want %v", err, errPostgresDatabaseIdentity)
			}
		})
	}
}

func TestEveryDatabaseServiceHasAnExactURLIdentity(t *testing.T) {
	for _, spec := range allPostgresRoleSpecs() {
		if spec.service == "" {
			continue
		}
		databaseURL := "postgres://" + spec.username + ":secret@postgres/" + spec.database + "?sslmode=disable"
		if err := validateRoleDatabaseConfig(spec.service, databaseURL, store.DriverPostgres); err != nil {
			t.Fatalf("%s database identity error = %v", spec.service, err)
		}
	}
}

func TestRequirePostgresSessionAuthority(t *testing.T) {
	if testPostgres == nil {
		t.Skip("PostgreSQL test environment unavailable")
	}
	spec := postgresRoleSpecForTest(t, serviceRoleAPIGateway)
	databaseURL, err := testPostgres.DatabaseURLForRole(testDBName, spec.username)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenFromConfig(databaseURL, store.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := requirePostgresSessionAuthority(db, spec); err != nil {
		t.Fatal(err)
	}
	wrong := spec
	wrong.database = "identity_auth"
	if err := requirePostgresSessionAuthority(db, wrong); !errors.Is(err, errPostgresSessionAuthority) {
		t.Fatalf("wrong database error = %v, want %v", err, errPostgresSessionAuthority)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var sessionUser, currentUser string
	if err := db.QueryRowContext(ctx, `SELECT session_user, current_user`).Scan(&sessionUser, &currentUser); err != nil {
		t.Fatal(err)
	}
	if sessionUser != spec.username || currentUser != spec.username {
		t.Fatalf("session_user=%q current_user=%q, want %q", sessionUser, currentUser, spec.username)
	}
}

func TestWorkloadNonceDatabaseRequiresRuntimeIdentity(t *testing.T) {
	cfg := validWorkloadRuntimeConfig(serviceRoleIdentityAuth)
	cfg.WorkloadAuth.NonceDatabaseURL = "postgres://identity_auth_runtime:secret@postgres/workload_auth?sslmode=disable"
	if err := validateWorkloadAuthRuntimeConfig(serviceRoleIdentityAuth, cfg); !errors.Is(err, errPostgresDatabaseIdentity) {
		t.Fatalf("error = %v, want %v", err, errPostgresDatabaseIdentity)
	}
}

func postgresRoleSpecForTest(t *testing.T, role serviceRole) postgresRoleSpec {
	t.Helper()
	spec, present := postgresRoleSpecForService(role)
	if !present {
		t.Fatalf("Postgres role spec missing for %s", role)
	}
	return spec
}
