package walletgrpc

import (
	"context"
	"testing"

	"github.com/adonese/noebs/internal/tenantcatalog"
	basestore "github.com/adonese/noebs/store"
	walletstore "github.com/adonese/noebs/wallet/store"
)

func provisionWalletGRPCTestTenant(t testing.TB, ctx context.Context, db *basestore.DB, id, name string) {
	t.Helper()
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: tenantcatalog.ID(id), Name: name}})
	if err != nil {
		t.Fatal(err)
	}
	if err := basestore.New(db).ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatalf("provision tenant %s: %v", id, err)
	}
}

func resolveWalletGRPCTestOperator(t testing.TB, ctx context.Context, db *basestore.DB, subject string) int64 {
	t.Helper()
	operator, err := walletstore.New(db).ResolveOperatorIdentity(ctx, "https://identity.example/realms/noebs", subject)
	if err != nil {
		t.Fatalf("resolve wallet gRPC test operator %s: %v", subject, err)
	}
	return operator.ID
}
