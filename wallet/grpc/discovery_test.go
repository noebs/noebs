package walletgrpc

import (
	"context"
	"testing"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWalletDiscoveryRequiresIdentityAndExplicitInputs(t *testing.T) {
	server := NewServer(nil)
	for name, call := range map[string]func() error{
		"account": func() error {
			_, err := server.GetWalletAccount(context.Background(), &walletv1.GetWalletAccountRequest{})
			return err
		},
		"providers": func() error {
			_, err := server.ListWalletProviders(context.Background(), &walletv1.ListWalletProvidersRequest{})
			return err
		},
		"funding": func() error {
			_, err := server.ListWalletFundingMethods(context.Background(), &walletv1.ListWalletFundingMethodsRequest{})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if call() == nil {
				t.Fatal("missing service/identity was accepted")
			}
		})
	}
	for name, call := range map[string]func() error{
		"account":   func() error { _, err := server.GetWalletAccount(context.Background(), nil); return err },
		"providers": func() error { _, err := server.ListWalletProviders(context.Background(), nil); return err },
		"funding":   func() error { _, err := server.ListWalletFundingMethods(context.Background(), nil); return err },
	} {
		t.Run("nil_"+name, func(t *testing.T) {
			if status.Code(call()) != codes.InvalidArgument {
				t.Fatal("nil request must fail at API boundary")
			}
		})
	}
	store := &walletstore.Store{}
	if _, err := store.ListUserWallets(context.Background(), "", 42); err != walletstore.ErrMissingTenantID {
		t.Fatalf("missing tenant reached DB: %v", err)
	}
	if _, err := store.ListUserWallets(context.Background(), "tenant", 0); err != walletstore.ErrInvalidUserID {
		t.Fatalf("missing user reached DB: %v", err)
	}
}

func TestWalletAccountAndFundingDiscovery(t *testing.T) {
	server, tenant, wallet42, wallet7 := newWalletServerWithUsers(t)
	ctx := walletGatewayIdentityContext(42, tenant)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	account, err := server.GetWalletAccount(ctx, &walletv1.GetWalletAccountRequest{})
	must(err)
	if account.TenantId != tenant || account.UserId != "42" || len(account.Wallets) != 1 || account.Wallets[0].Id != wallet42.ID.String() {
		t.Fatalf("account leaked or lost ownership: %+v", account)
	}
	empty, err := server.GetWalletAccount(walletGatewayIdentityContext(99, tenant), &walletv1.GetWalletAccountRequest{})
	must(err)
	if len(empty.Wallets) != 0 {
		t.Fatal("account restoration must not create wallets")
	}
	_, err = server.GetWalletAccount(ctx, &walletv1.GetWalletAccountRequest{TenantId: "another-tenant"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("tenant mismatch: %v", err)
	}
	_, err = server.ListWalletFundingMethods(ctx, &walletv1.ListWalletFundingMethodsRequest{WalletId: wallet7.ID.String()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("other customer's funding details: %v", err)
	}
	_, err = server.ListWalletFundingMethods(ctx, &walletv1.ListWalletFundingMethodsRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing wallet: %v", err)
	}
	_, err = server.ListWalletProviders(context.Background(), &walletv1.ListWalletProvidersRequest{TenantId: tenant})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous discovery: %v", err)
	}

	providers, err := server.ListWalletProviders(ctx, &walletv1.ListWalletProvidersRequest{})
	must(err)
	if len(providers.Providers) != 0 {
		t.Fatal("provider must not be invented for an unbound tenant")
	}

	// Add a synthetic PSP configuration to exercise the same directory through
	// the existing deposit adapter. Its private endpoint must not be published.
	_, err = server.Service.Store.DB.ExecContext(ctx, `INSERT INTO psp_configs
		(tenant_id, provider_code, provider_name, api_base_url, enabled_currencies, idempotency_header_name, supports_deposit, supports_withdrawal, deposit_response_mapping)
		VALUES ($1,'example','Example PSP','https://private.invalid',ARRAY['USD'],'Idempotency-Key',true,false,'{}')`, tenant)
	must(err)
	providers, err = server.ListWalletProviders(ctx, &walletv1.ListWalletProvidersRequest{})
	must(err)
	if len(providers.Providers) != 1 || providers.Providers[0].Id != "example" || providers.Providers[0].Capabilities.Send || !providers.Providers[0].Capabilities.Funding || providers.Providers[0].Capabilities.Services {
		t.Fatalf("PSP capabilities misrepresented: %+v", providers)
	}
	funding, err := server.ListWalletFundingMethods(ctx, &walletv1.ListWalletFundingMethodsRequest{WalletId: wallet42.ID.String()})
	must(err)
	if len(funding.Methods) != 1 || funding.Methods[0].Mode != "deposit" || !funding.Methods[0].Available {
		t.Fatalf("PSP funding: %+v", funding)
	}

	// Bind a registered rail to a normal customer wallet. No customer opening
	// funds, native outcomes or simulated ledger credits are needed for discovery.
	w, err := ensureUserWalletForTest(t, ctx, server.Service, tenant, 42, "SDG")
	must(err)
	systems := make([]*walletstore.Wallet, 0, 2)
	for _, owner := range []string{walletstore.SystemMojaloopClearing, walletstore.SystemMojaloopSuspense} {
		system, err := server.Service.Store.EnsureWallet(ctx, walletstore.EnsureWalletParams{TenantID: tenant, OwnerType: walletstore.OwnerTypeSystem, OwnerID: owner, Currency: "SDG", CurrencyUnitID: w.CurrencyUnitID, KYCTier: walletstore.KYCTierUnverified})
		must(err)
		systems = append(systems, system)
	}
	_, err = server.Service.Store.DB.ExecContext(ctx, `INSERT INTO interop_bindings(tenant_id,fsp_id,currency,currency_unit_version_id,clearing_wallet_id,suspense_wallet_id,enabled) VALUES($1,'examplefsp','SDG',$2,$3,$4,true)`, tenant, w.CurrencyUnitID, systems[0].ID, systems[1].ID)
	must(err)
	providers, err = server.ListWalletProviders(ctx, &walletv1.ListWalletProvidersRequest{})
	must(err)
	if len(providers.Providers) != 2 || providers.Providers[1].Id != "mojaloop" || !providers.Providers[1].Available || providers.Providers[1].Capabilities.Send || providers.Providers[1].Capabilities.Receive || !providers.Providers[1].Capabilities.Funding || providers.Providers[1].Capabilities.Services {
		t.Fatalf("rail capabilities: %+v", providers)
	}
	assertInteropEligibility := func(userID int64, eligible bool) {
		t.Helper()
		result, err := server.ListWalletProviders(walletGatewayIdentityContext(userID, tenant), &walletv1.ListWalletProvidersRequest{})
		must(err)
		if len(result.Providers) != 2 {
			t.Fatalf("provider directory: %+v", result)
		}
		p := result.Providers[1]
		if p.Id != "mojaloop" || !p.Available || !p.Capabilities.Funding || p.Capabilities.Send != eligible || p.Capabilities.Receive != eligible {
			t.Fatalf("account-specific rail eligibility = %v: %+v", eligible, p)
		}
	}
	// A tenant binding and another customer's registered recipient must not
	// enable this customer's unregistered source wallet or invent an account.
	foreign, err := ensureUserWalletForTest(t, ctx, server.Service, tenant, 7, "SDG")
	must(err)
	_, err = server.Service.Store.DB.ExecContext(ctx, `INSERT INTO interop_aliases(tenant_id,identifier,wallet_id,display_name) VALUES($1,'249900000077',$2,'Fixture recipient')`, tenant, foreign.ID)
	must(err)
	assertInteropEligibility(7, true)
	assertInteropEligibility(42, false)
	assertInteropEligibility(99, false)
	_, err = server.ListWalletFundingMethods(ctx, &walletv1.ListWalletFundingMethodsRequest{WalletId: foreign.ID.String()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("foreign registered alias disclosed: %v", err)
	}
	getFunding := func() *walletv1.WalletFundingMethod {
		t.Helper()
		result, err := server.ListWalletFundingMethods(ctx, &walletv1.ListWalletFundingMethodsRequest{WalletId: w.ID.String()})
		must(err)
		if len(result.Methods) != 1 {
			t.Fatalf("currency scoped methods: %+v", result)
		}
		return result.Methods[0]
	}
	method := getFunding()
	if method.Available || method.UnavailableReason != "registration_required" || method.AccountIdentifier != "" {
		t.Fatalf("invented receiving alias: %+v", method)
	}
	_, err = server.Service.Store.DB.ExecContext(ctx, `INSERT INTO interop_aliases(tenant_id,identifier,wallet_id,display_name) VALUES($1,'249900000088',$2,'Synthetic customer')`, tenant, w.ID)
	must(err)
	assertInteropEligibility(42, true)
	method = getFunding()
	if !method.Available || method.AccountIdentifier != "249900000088" || method.AccountName != "Synthetic customer" || method.Mode != "external_transfer" {
		t.Fatalf("registered receiver: %+v", method)
	}
	for range 3 {
		_ = getFunding()
	}
	current, err := server.Service.Store.GetWallet(ctx, tenant, w.ID)
	must(err)
	if current.Balance != 0 || current.AvailableBalance != 0 {
		t.Fatal("funding discovery changed money")
	}
	_, err = server.Service.Store.DB.ExecContext(ctx, `UPDATE wallets SET status='frozen' WHERE id=$1`, w.ID)
	must(err)
	assertInteropEligibility(42, false)
	assertInteropEligibility(7, true)
	method = getFunding()
	if method.Available || method.UnavailableReason != "wallet_inactive" || method.AccountIdentifier != "" {
		t.Fatalf("frozen receiver: %+v", method)
	}
	_, err = server.Service.Store.DB.ExecContext(ctx, `UPDATE interop_bindings SET enabled=false WHERE tenant_id=$1`, tenant)
	must(err)
	method = getFunding()
	if method.Available || method.UnavailableReason != "provider_unavailable" {
		t.Fatalf("disabled provider: %+v", method)
	}
	providers, err = server.ListWalletProviders(ctx, &walletv1.ListWalletProvidersRequest{})
	must(err)
	if providers.Providers[1].Available || providers.Providers[1].Capabilities.Funding {
		t.Fatal("disabled rail advertised as available")
	}
}
