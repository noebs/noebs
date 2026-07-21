package store

import (
	"errors"
	"testing"
)

func TestPSPAmountPoliciesSelectExactUnitAndRegionAndAreImmutable(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	usdUnitID := testCurrencyUnitID(t, ctx, walletStore, "USD")
	aedUnitID := testCurrencyUnitID(t, ctx, walletStore, "AED")

	insertConfig := walletStore.DB.Rebind(`INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url,
		enabled_currencies, idempotency_header_name, deposit_response_mapping
	) VALUES(?, 'policy-provider', 'Policy Provider', 'https://psp.example',
		ARRAY['USD','AED'], 'Idempotency-Key', '{}'::jsonb)`)
	if _, err := walletStore.DB.ExecContext(ctx, insertConfig, tenantID); err != nil {
		t.Fatalf("insert PSP config: %v", err)
	}

	insertPolicy := walletStore.DB.Rebind(`INSERT INTO psp_amount_policies(
		tenant_id, provider_code, currency, currency_unit_version_id,
		direction, region, min_amount, max_amount
	) VALUES(?, 'policy-provider', 'USD', ?, 'deposit', ?, ?, ?)
	RETURNING id`)
	var globalID, regionalID int64
	if err := walletStore.DB.GetContext(ctx, &globalID, insertPolicy, tenantID, usdUnitID, "", 100, 1000); err != nil {
		t.Fatalf("insert global policy: %v", err)
	}
	if err := walletStore.DB.GetContext(ctx, &regionalID, insertPolicy, tenantID, usdUnitID, "AE", 200, 900); err != nil {
		t.Fatalf("insert regional policy: %v", err)
	}

	regional, err := walletStore.GetActivePSPAmountPolicy(ctx, tenantID, "policy-provider", PSPConfigScope{
		Currency: "USD", CurrencyUnitID: usdUnitID, Direction: "deposit", Region: "AE",
	})
	if err != nil {
		t.Fatalf("get regional policy: %v", err)
	}
	if regional.ID != regionalID || regional.MinAmount.Int64 != 200 || regional.MaxAmount.Int64 != 900 {
		t.Fatalf("regional policy = %+v", regional)
	}

	global, err := walletStore.GetActivePSPAmountPolicy(ctx, tenantID, "policy-provider", PSPConfigScope{
		Currency: "USD", CurrencyUnitID: usdUnitID, Direction: "deposit", Region: "US",
	})
	if err != nil {
		t.Fatalf("get global fallback policy: %v", err)
	}
	if global.ID != globalID || global.MinAmount.Int64 != 100 || global.MaxAmount.Int64 != 1000 {
		t.Fatalf("global policy = %+v", global)
	}

	resolved, _, err := walletStore.ResolvePSPConfig(ctx, tenantID, "policy-provider", PSPConfigScope{
		Currency: "USD", CurrencyUnitID: usdUnitID, Direction: "deposit", Region: "AE",
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if resolved.AmountCurrencyUnitID != usdUnitID || resolved.MinAmount.Int64 != 200 || resolved.MaxAmount.Int64 != 900 {
		t.Fatalf("resolved bounds = %+v", resolved)
	}

	if _, err := walletStore.GetActivePSPAmountPolicy(ctx, tenantID, "policy-provider", PSPConfigScope{
		Currency: "USD", CurrencyUnitID: aedUnitID, Direction: "deposit",
	}); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("mismatched unit error = %v, want %v", err, ErrCurrencyMismatch)
	}
	if _, err := walletStore.GetActivePSPAmountPolicy(ctx, tenantID, "policy-provider", PSPConfigScope{
		Currency: "AED", CurrencyUnitID: aedUnitID, Direction: "deposit",
	}); !errors.Is(err, ErrPSPAmountPolicyNotFound) {
		t.Fatalf("missing exact policy error = %v, want %v", err, ErrPSPAmountPolicyNotFound)
	}
	withoutBounds, _, err := walletStore.ResolvePSPConfig(ctx, tenantID, "policy-provider", PSPConfigScope{
		Currency: "AED", CurrencyUnitID: aedUnitID, Direction: "deposit",
	})
	if err != nil {
		t.Fatalf("resolve config without amount policy: %v", err)
	}
	if withoutBounds.AmountCurrencyUnitID != aedUnitID || withoutBounds.MinAmount.Valid || withoutBounds.MaxAmount.Valid {
		t.Fatalf("resolved unbounded amount identity = %+v, want AED unit %d with null bounds", withoutBounds, aedUnitID)
	}

	if _, err := walletStore.DB.ExecContext(ctx, walletStore.DB.Rebind(
		`UPDATE psp_amount_policies SET min_amount = ? WHERE id = ?`), 1, regionalID); err == nil {
		t.Fatal("mutable PSP policy bounds unexpectedly accepted")
	}
	if _, err := walletStore.DB.ExecContext(ctx, walletStore.DB.Rebind(
		`UPDATE psp_amount_policies SET is_active = FALSE, updated_at = clock_timestamp() WHERE id = ?`), regionalID); err != nil {
		t.Fatalf("deactivate PSP policy: %v", err)
	}
	if _, err := walletStore.DB.ExecContext(ctx, walletStore.DB.Rebind(
		`UPDATE psp_amount_policies SET is_active = TRUE WHERE id = ?`), regionalID); err == nil {
		t.Fatal("PSP policy reactivation unexpectedly accepted")
	}
	var replacementID int64
	if err := walletStore.DB.GetContext(ctx, &replacementID, insertPolicy, tenantID, usdUnitID, "AE", 250, 850); err != nil {
		t.Fatalf("insert replacement policy: %v", err)
	}
	if replacementID == regionalID {
		t.Fatal("replacement policy did not create a new immutable version")
	}
}
