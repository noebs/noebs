package store

import "testing"

func TestMigrationScopesAreServiceOwned(t *testing.T) {
	for scope, path := range migrationScopePaths {
		if path == "migrations/postgres" {
			t.Fatalf("scope %q points at legacy monolith migration path", scope)
		}
		if scope == "" {
			t.Fatalf("migration scope must be explicit")
		}
	}
	if _, ok := migrationScopePaths["legacy"]; ok {
		t.Fatalf("legacy migration scope must not be registered")
	}
}
