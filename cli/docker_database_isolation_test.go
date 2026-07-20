package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresRoleCatalogMatchesReleaseSpecs(t *testing.T) {
	if err := validatePostgresRoleCatalog(); err != nil {
		t.Fatal(err)
	}
	if got := len(allPostgresRoleSpecs()); got != 22 {
		t.Fatalf("Postgres role count = %d, want 22", got)
	}
}

func TestPostgresBootstrapRejectsEveryUnexpectedCustomRole(t *testing.T) {
	paths := []string{
		filepath.Join("..", "deploy", "docker", "postgres", "001-service-databases.sql"),
		filepath.Join("..", "deploy", "docker", "postgres", "postgres-start.sh"),
	}
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		authority := string(payload)
		for _, required := range []string{
			"rolname <> 'postgres'",
			"rolname !~ '^pg_'",
		} {
			if !strings.Contains(authority, required) {
				t.Fatalf("%s does not exclude only built-in role authority with %q", path, required)
			}
		}
		if strings.Contains(authority, "rolcanlogin") {
			t.Fatalf("%s ignores unexpected NOLOGIN roles", path)
		}
	}
}

func TestDockerServiceDatabasesRevokePublicAccessBeforeExplicitGrants(t *testing.T) {
	sql, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "postgres", "001-service-databases.sql"))
	if err != nil {
		t.Fatalf("read Docker service database SQL: %v", err)
	}

	requireAuthDatabaseIsolation(t, string(sql))
}

func TestKubernetesServiceDatabasesRevokePublicAccessBeforeExplicitGrants(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "platform", "postgres-provisioning.sql"))
	if err != nil {
		t.Fatalf("read prepared Kubernetes Postgres provisioning SQL: %v", err)
	}
	requireAuthDatabaseIsolation(t, string(payload))
}

func requireAuthDatabaseIsolation(t *testing.T, sql string) {
	t.Helper()

	for _, database := range []struct {
		name         string
		owner        string
		runtimeRoles []string
	}{
		{name: "identity_auth", owner: "identity_auth_migrate", runtimeRoles: []string{"identity_auth_runtime"}},
		{name: "card_vault", owner: "card_vault_migrate", runtimeRoles: []string{"card_vault_runtime"}},
		{name: "ebs_adapter", owner: "ebs_adapter_migrate", runtimeRoles: []string{"ebs_adapter_runtime", "ebs_adapter_events"}},
		{name: "admin_reporting", owner: "admin_reporting_migrate", runtimeRoles: []string{"admin_reporting_runtime", "admin_reporting_projector"}},
		{name: "notification_chat", owner: "notification_chat_migrate", runtimeRoles: []string{"notification_chat_runtime"}},
		{name: "wallet_ledger", owner: "wallet_ledger_migrate", runtimeRoles: []string{"wallet_ledger_runtime", "wallet_ledger_worker", "wallet_ledger_webhook"}},
		{name: "workload_auth", owner: "workload_auth_migrate", runtimeRoles: []string{"workload_auth_runtime", "workload_auth_cleanup"}},
		{name: "gateway_auth", owner: "gateway_auth_migrate", runtimeRoles: []string{"gateway_auth_runtime", "gateway_auth_cleanup"}},
	} {
		create := "SELECT 'CREATE DATABASE " + database.name + " OWNER " + database.owner + "'"
		owner := "ALTER DATABASE " + database.name + " OWNER TO " + database.owner + ";"
		schemaOwner := "ALTER SCHEMA public OWNER TO " + database.owner + ";"
		for _, required := range []string{create, owner, "\\connect " + database.name, schemaOwner} {
			if !strings.Contains(sql, required) {
				t.Fatalf("%s database/schema authority is missing %q", database.name, required)
			}
		}
		if !strings.Contains(sql, "('"+database.name+"', '"+database.owner+"', TRUE)") {
			t.Fatalf("%s migration role lacks exact CONNECT/TEMPORARY catalog entry", database.name)
		}
		for _, role := range database.runtimeRoles {
			if !strings.Contains(sql, "('"+database.name+"', '"+role+"', FALSE)") {
				t.Fatalf("%s runtime role %s lacks exact CONNECT-only catalog entry", database.name, role)
			}
		}
	}
	for _, required := range []string{
		"REVOKE ALL PRIVILEGES ON DATABASE %I FROM PUBLIC CASCADE",
		"REVOKE ALL PRIVILEGES ON DATABASE %I FROM %I CASCADE",
		"REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC CASCADE",
		"REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE",
		"ALTER ROLE %I IN DATABASE %I RESET ALL",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("database authority does not dynamically converge %q", required)
		}
	}
	for _, retired := range []string{"CREATE DATABASE psp_webhook", "psp_webhook_migrate", "psp_webhook_runtime"} {
		if strings.Contains(sql, retired) {
			t.Fatalf("database authority retains %q", retired)
		}
	}
}
