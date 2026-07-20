package handler

import (
	"context"
	"testing"

	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/store"
)

func provisionHandlerTestTenant(t testing.TB, ctx context.Context, storeSvc *store.Store, id, name string) {
	t.Helper()
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: tenantcatalog.ID(id), Name: name}})
	if err != nil {
		t.Fatal(err)
	}
	if err := storeSvc.ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatalf("provision tenant %s: %v", id, err)
	}
}
