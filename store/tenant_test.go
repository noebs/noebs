package store

import (
	"context"
	"testing"

	"github.com/adonese/noebs/internal/tenantcatalog"
)

func provisionTestTenant(t testing.TB, ctx context.Context, store *Store, id, name string) {
	t.Helper()
	provisionTestTenants(t, ctx, store, tenantcatalog.Tenant{ID: tenantcatalog.ID(id), Name: name})
}

func provisionTestTenants(t testing.TB, ctx context.Context, store *Store, tenants ...tenantcatalog.Tenant) {
	t.Helper()
	catalog, err := tenantcatalog.New(tenants)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatalf("provision test tenants: %v", err)
	}
}
