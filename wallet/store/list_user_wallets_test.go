package store

import "testing"

func TestListUserWalletsUsesRuntimeAuthorityAndCanonicalOwnership(t *testing.T) {
	f := newInteropFixture(t)
	n := f.tenant(t, "account-discovery", false)
	// The fixture's noncanonical owner "customer" with user_id=1 must not
	// appear in the authenticated user's account just because one field matches.
	wallets, err := f.runtime.ListUserWallets(f.ctx, n.id, 1)
	interopMust(t, err)
	if len(wallets) != 0 {
		t.Fatal("noncanonical owner was treated as authenticated customer")
	}
	w, err := f.runtime.EnsureWallet(f.ctx, EnsureWalletParams{TenantID: n.id, OwnerType: OwnerTypeUser, OwnerID: "42", UserID: 42, Currency: "SDG", CurrencyUnitID: n.unit, KYCTier: KYCTierUnverified})
	interopMust(t, err)
	wallets, err = f.runtime.ListUserWallets(f.ctx, n.id, 42)
	interopMust(t, err)
	if len(wallets) != 1 || wallets[0].ID != w.ID {
		t.Fatalf("runtime account projection: %+v", wallets)
	}
	wallets, err = f.runtime.ListUserWallets(f.ctx, "another-tenant", 42)
	interopMust(t, err)
	if len(wallets) != 0 {
		t.Fatal("account projection crossed tenant boundary")
	}
}
