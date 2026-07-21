package validation

import (
	"context"
	"database/sql"
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

func testCurrencyUnit(code string, scale int16) *walletstore.CurrencyUnitVersion {
	return &walletstore.CurrencyUnitVersion{
		ID:                    int64(code[0])<<16 | int64(code[1])<<8 | int64(code[2]),
		CurrencyCode:          code,
		ISOMinorExponent:      sql.NullInt16{Int16: scale, Valid: true},
		DisplayExponent:       scale,
		CashExponent:          scale,
		CashRoundingIncrement: 1,
		ValidFrom:             time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
}

func testCurrencyUnitLookup(scales map[string]int16) func(context.Context, int64) (*walletstore.CurrencyUnitVersion, error) {
	return func(_ context.Context, currencyUnitID int64) (*walletstore.CurrencyUnitVersion, error) {
		for code, scale := range scales {
			unit := testCurrencyUnit(code, scale)
			if unit.ID == currencyUnitID {
				return unit, nil
			}
		}
		return nil, walletstore.ErrCurrencyNotFound
	}
}

func testExchangeRate(baseCurrency, quoteCurrency string, value decimal.Decimal) *walletstore.ExchangeRate {
	return &walletstore.ExchangeRate{
		BaseCurrency:        baseCurrency,
		BaseCurrencyUnitID:  testCurrencyUnit(baseCurrency, 0).ID,
		QuoteCurrency:       quoteCurrency,
		QuoteCurrencyUnitID: testCurrencyUnit(quoteCurrency, 0).ID,
		BuyRate:             value,
		SellRate:            value,
	}
}

func TestConvertWithdrawalAmountRespectsDifferentCurrencyScales(t *testing.T) {
	service := &Service{
		RateLookup: func(context.Context, string, string, int64, string, int64, time.Time) (*walletstore.ExchangeRate, error) {
			return testExchangeRate("USD", "KWD", decimal.NewFromInt(1)), nil
		},
		CurrencyUnitLookup: testCurrencyUnitLookup(map[string]int16{"USD": 2, "KWD": 3}),
	}

	amount, _, _, err := service.convertWithdrawalAmount(
		t.Context(),
		"tenant",
		100,
		"USD",
		testCurrencyUnit("USD", 2).ID,
		"KWD",
		testCurrencyUnit("KWD", 3).ID,
	)
	if err != nil {
		t.Fatalf("convert withdrawal amount: %v", err)
	}
	if amount != 1000 {
		t.Fatalf("converted minor units = %d, want 1000", amount)
	}
}
