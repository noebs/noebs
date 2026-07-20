package store

import (
	"context"
	"testing"

	"github.com/adonese/noebs/internal/tenantcatalog"
	basestore "github.com/adonese/noebs/store"
)

func provisionWalletStoreTestTenant(t testing.TB, ctx context.Context, db *basestore.DB, id, name string) {
	t.Helper()
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: tenantcatalog.ID(id), Name: name}})
	if err != nil {
		t.Fatal(err)
	}
	if err := basestore.New(db).ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatalf("provision tenant %s: %v", id, err)
	}
}
