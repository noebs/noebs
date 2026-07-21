package walletgrpc

import (
	"context"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantcatalog"
	basestore "github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet"
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

func ensureUserWalletForTest(t testing.TB, ctx context.Context, service *wallet.Service, tenantID string, userID int64, currency string) (*walletstore.Wallet, error) {
	t.Helper()
	unit, err := service.Store.GetCurrencyUnit(ctx, currency, time.Now().UTC())
	if err != nil {
		t.Fatalf("resolve %s unit for wallet test: %v", currency, err)
	}
	return service.EnsureUserWallet(ctx, wallet.EnsureUserWalletParams{
		TenantID:       tenantID,
		UserID:         userID,
		Currency:       currency,
		CurrencyUnitID: unit.ID,
	})
}
