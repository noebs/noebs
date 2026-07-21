package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestActiveExchangeRatesExcludeFutureAndExpiredWindows(t *testing.T) {
	ctx, store, tenantID := newWalletStoreIntegration(t)
	operatorID := insertWalletOperator(t, ctx, store, "rate-window-operator")
	now := time.Now().UTC()
	usdUnitID := testCurrencyUnitID(t, ctx, store, "USD")
	eurUnitID := testCurrencyUnitID(t, ctx, store, "EUR")

	rates := []ExchangeRate{
		{
			TenantID:            tenantID,
			BaseCurrency:        "USD",
			BaseCurrencyUnitID:  usdUnitID,
			QuoteCurrency:       "EUR",
			QuoteCurrencyUnitID: eurUnitID,
			BuyRate:             decimal.RequireFromString("0.7"),
			SellRate:            decimal.RequireFromString("0.8"),
			SetByOperatorID:     operatorID,
			EffectiveFrom:       now.Add(-2 * time.Hour),
			EffectiveTo:         sql.NullTime{Time: now.Add(-time.Hour), Valid: true},
		},
		{
			TenantID:            tenantID,
			BaseCurrency:        "USD",
			BaseCurrencyUnitID:  usdUnitID,
			QuoteCurrency:       "EUR",
			QuoteCurrencyUnitID: eurUnitID,
			BuyRate:             decimal.RequireFromString("0.9"),
			SellRate:            decimal.RequireFromString("1.0"),
			SetByOperatorID:     operatorID,
			EffectiveFrom:       now.Add(-time.Hour),
			EffectiveTo:         sql.NullTime{Time: now.Add(time.Hour), Valid: true},
		},
		{
			TenantID:            tenantID,
			BaseCurrency:        "USD",
			BaseCurrencyUnitID:  usdUnitID,
			QuoteCurrency:       "EUR",
			QuoteCurrencyUnitID: eurUnitID,
			BuyRate:             decimal.RequireFromString("1.1"),
			SellRate:            decimal.RequireFromString("1.2"),
			SetByOperatorID:     operatorID,
			EffectiveFrom:       now.Add(time.Hour),
		},
	}
	for _, rate := range rates {
		if _, err := store.CreateExchangeRate(ctx, rate); err != nil {
			t.Fatalf("create exchange rate: %v", err)
		}
	}

	pinned, err := store.GetActiveRateForUnitsAt(ctx, tenantID, "USD", usdUnitID, "EUR", eurUnitID, now)
	if err != nil {
		t.Fatalf("get unit-pinned active exchange rate: %v", err)
	}
	if pinned.BaseCurrencyUnitID != usdUnitID || pinned.QuoteCurrencyUnitID != eurUnitID ||
		!pinned.SellRate.Equal(decimal.RequireFromString("1.0")) {
		t.Fatalf("unit-pinned active rate = %+v, want USD unit %d / EUR unit %d at 1.0", pinned, usdUnitID, eurUnitID)
	}

	listed, err := store.ListExchangeRates(ctx, ExchangeRateFilter{
		TenantID:      tenantID,
		BaseCurrency:  "USD",
		QuoteCurrency: "EUR",
		ActiveOnly:    true,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("list active exchange rates: %v", err)
	}
	if len(listed) != 1 || !listed[0].SellRate.Equal(decimal.RequireFromString("1.0")) {
		t.Fatalf("active exchange rates = %+v, want only current rate", listed)
	}
}
