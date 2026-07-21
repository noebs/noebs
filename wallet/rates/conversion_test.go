package rates

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/adonese/noebs/groosh"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

func rateTestUnit(code string, exponent int16) *walletstore.CurrencyUnitVersion {
	return &walletstore.CurrencyUnitVersion{
		ID:                    int64(code[0])<<16 | int64(code[1])<<8 | int64(code[2]),
		CurrencyCode:          code,
		ISOMinorExponent:      sql.NullInt16{Int16: exponent, Valid: true},
		DisplayExponent:       exponent,
		CashExponent:          exponent,
		CashRoundingIncrement: 1,
		ValidFrom:             time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestConvertMinorUnitsAppliesCurrencyScales(t *testing.T) {
	tests := []struct {
		name       string
		amount     int64
		rate       string
		baseScale  int16
		quoteScale int16
		want       int64
	}{
		{name: "same scale", amount: 100, rate: "3.67", baseScale: 2, quoteScale: 2, want: 367},
		{name: "two to three", amount: 100, rate: "1", baseScale: 2, quoteScale: 3, want: 1000},
		{name: "three to two", amount: 1000, rate: "1", baseScale: 3, quoteScale: 2, want: 100},
		{name: "zero to two", amount: 100, rate: "0.01", baseScale: 0, quoteScale: 2, want: 100},
		{name: "four to zero", amount: 10000, rate: "2", baseScale: 4, quoteScale: 0, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertMinorUnits(
				tt.amount,
				decimal.RequireFromString(tt.rate),
				rateTestUnit("USD", tt.baseScale),
				rateTestUnit("KWD", tt.quoteScale),
				groosh.RoundHalfAwayFromZero,
			)
			if err != nil {
				t.Fatalf("ConvertMinorUnits() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ConvertMinorUnits() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestConvertMinorUnitsUsesExplicitRounding(t *testing.T) {
	base := rateTestUnit("USD", 2)
	quote := rateTestUnit("JPY", 0)
	rate := decimal.NewFromInt(250) // USD 0.01 is exactly JPY 2.5.

	halfEven, err := ConvertMinorUnits(1, rate, base, quote, groosh.RoundHalfEven)
	if err != nil {
		t.Fatalf("half-even conversion: %v", err)
	}
	halfAway, err := ConvertMinorUnits(1, rate, base, quote, groosh.RoundHalfAwayFromZero)
	if err != nil {
		t.Fatalf("half-away conversion: %v", err)
	}
	if halfEven != 2 || halfAway != 3 {
		t.Fatalf("rounded values = (%d, %d), want (2, 3)", halfEven, halfAway)
	}
}

func TestConvertMinorUnitsRejectsInvalidScaleAndOverflow(t *testing.T) {
	missingScale := rateTestUnit("USD", 2)
	missingScale.ISOMinorExponent.Valid = false

	tests := []struct {
		name      string
		amount    int64
		rate      decimal.Decimal
		base      *walletstore.CurrencyUnitVersion
		quote     *walletstore.CurrencyUnitVersion
		wantError error
	}{
		{name: "missing base", amount: 1, rate: decimal.NewFromInt(1), quote: rateTestUnit("KWD", 3), wantError: walletstore.ErrCurrencyNotFound},
		{name: "missing ISO scale", amount: 1, rate: decimal.NewFromInt(1), base: missingScale, quote: rateTestUnit("KWD", 3), wantError: walletstore.ErrCurrencyScaleUnavailable},
		{name: "zero rate", amount: 1, rate: decimal.Zero, base: rateTestUnit("USD", 2), quote: rateTestUnit("KWD", 3), wantError: walletstore.ErrInvalidRate},
		{name: "overflow", amount: math.MaxInt64, rate: decimal.NewFromInt(1), base: rateTestUnit("USD", 2), quote: rateTestUnit("KWD", 3), wantError: walletstore.ErrAmountOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ConvertMinorUnits(tt.amount, tt.rate, tt.base, tt.quote, groosh.RoundHalfAwayFromZero)
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("ConvertMinorUnits() error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

func TestServiceConvertLoadsBothCurrencyUnitVersions(t *testing.T) {
	usdUnit := rateTestUnit("USD", 2)
	usdUnit.ID = 101
	kwdUnit := rateTestUnit("KWD", 3)
	kwdUnit.ID = 202
	lookedUp := make([]int64, 0, 2)
	service := &Service{
		Store: &walletstore.Store{},
		RateLookup: func(_ context.Context, tenantID, baseCurrency string, baseCurrencyUnitID int64, quoteCurrency string, quoteCurrencyUnitID int64, asOf time.Time) (*walletstore.ExchangeRate, error) {
			if tenantID != "tenant" || baseCurrency != "USD" || quoteCurrency != "KWD" {
				t.Fatalf("unexpected rate lookup: %s %s/%s", tenantID, baseCurrency, quoteCurrency)
			}
			if baseCurrencyUnitID != usdUnit.ID || quoteCurrencyUnitID != kwdUnit.ID {
				t.Fatalf("unexpected rate lookup units: %d/%d", baseCurrencyUnitID, quoteCurrencyUnitID)
			}
			if asOf.IsZero() {
				t.Fatal("rate lookup received zero as-of time")
			}
			return &walletstore.ExchangeRate{
				BaseCurrency:        "USD",
				BaseCurrencyUnitID:  usdUnit.ID,
				QuoteCurrency:       "KWD",
				QuoteCurrencyUnitID: kwdUnit.ID,
				SellRate:            decimal.NewFromInt(1),
			}, nil
		},
		CurrencyUnitLookup: func(_ context.Context, unitID int64) (*walletstore.CurrencyUnitVersion, error) {
			lookedUp = append(lookedUp, unitID)
			if unitID == usdUnit.ID {
				return usdUnit, nil
			}
			if unitID == kwdUnit.ID {
				return kwdUnit, nil
			}
			return nil, walletstore.ErrCurrencyNotFound
		},
	}

	got, err := service.Convert(
		t.Context(),
		"tenant",
		100,
		"USD",
		usdUnit.ID,
		"KWD",
		kwdUnit.ID,
	)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if got != 1000 {
		t.Fatalf("Convert() = %d, want 1000", got)
	}
	if len(lookedUp) != 2 || lookedUp[0] != usdUnit.ID || lookedUp[1] != kwdUnit.ID {
		t.Fatalf("currency unit lookups = %v, want [%d %d]", lookedUp, usdUnit.ID, kwdUnit.ID)
	}
}

func TestServiceConvertRejectsMismatchedRateIdentity(t *testing.T) {
	service := &Service{
		Store: &walletstore.Store{},
		RateLookup: func(context.Context, string, string, int64, string, int64, time.Time) (*walletstore.ExchangeRate, error) {
			return &walletstore.ExchangeRate{
				BaseCurrency:        "EUR",
				BaseCurrencyUnitID:  rateTestUnit("EUR", 2).ID,
				QuoteCurrency:       "KWD",
				QuoteCurrencyUnitID: rateTestUnit("KWD", 3).ID,
				SellRate:            decimal.NewFromInt(1),
			}, nil
		},
		CurrencyUnitLookup: func(context.Context, int64) (*walletstore.CurrencyUnitVersion, error) {
			t.Fatal("unit lookup should not run for a mismatched rate")
			return nil, nil
		},
	}

	_, err := service.Convert(t.Context(), "tenant", 100, "USD", rateTestUnit("USD", 2).ID, "KWD", rateTestUnit("KWD", 3).ID)
	if !errors.Is(err, walletstore.ErrCurrencyMismatch) {
		t.Fatalf("Convert() error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
	}
}

func TestServiceConvertRejectsMissingPinnedUnitIDs(t *testing.T) {
	service := &Service{
		Store: &walletstore.Store{},
		RateLookup: func(context.Context, string, string, int64, string, int64, time.Time) (*walletstore.ExchangeRate, error) {
			return &walletstore.ExchangeRate{
				BaseCurrency:  "USD",
				QuoteCurrency: "KWD",
				SellRate:      decimal.NewFromInt(1),
			}, nil
		},
		CurrencyUnitLookup: func(context.Context, int64) (*walletstore.CurrencyUnitVersion, error) {
			t.Fatal("unit lookup should not run without pinned unit ids")
			return nil, nil
		},
	}

	_, err := service.Convert(t.Context(), "tenant", 100, "USD", rateTestUnit("USD", 2).ID, "KWD", rateTestUnit("KWD", 3).ID)
	if !errors.Is(err, walletstore.ErrMissingCurrencyUnitID) {
		t.Fatalf("Convert() error = %v, want %v", err, walletstore.ErrMissingCurrencyUnitID)
	}
}

func TestServiceConvertRejectsRequestedUnitMismatch(t *testing.T) {
	base := rateTestUnit("USD", 2)
	quote := rateTestUnit("KWD", 3)
	service := &Service{
		Store: &walletstore.Store{},
		RateLookup: func(context.Context, string, string, int64, string, int64, time.Time) (*walletstore.ExchangeRate, error) {
			return &walletstore.ExchangeRate{
				BaseCurrency:        base.CurrencyCode,
				BaseCurrencyUnitID:  base.ID,
				QuoteCurrency:       quote.CurrencyCode,
				QuoteCurrencyUnitID: quote.ID,
				SellRate:            decimal.NewFromInt(1),
			}, nil
		},
		CurrencyUnitLookup: func(context.Context, int64) (*walletstore.CurrencyUnitVersion, error) {
			t.Fatal("unit lookup should not run after request/rate identity mismatch")
			return nil, nil
		},
	}

	_, err := service.Convert(t.Context(), "tenant", 100, "USD", base.ID+1, "KWD", quote.ID)
	if !errors.Is(err, walletstore.ErrCurrencyMismatch) {
		t.Fatalf("Convert() error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
	}
}

func TestServiceConvertRejectsSameCurrencyDifferentUnits(t *testing.T) {
	service := &Service{Store: &walletstore.Store{}}
	_, err := service.Convert(t.Context(), "tenant", 100, "USD", 101, "USD", 202)
	if !errors.Is(err, walletstore.ErrCurrencyMismatch) {
		t.Fatalf("Convert() error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
	}
}

func TestServiceConvertSameCurrencyValidatesPinnedUnitIdentity(t *testing.T) {
	for _, tt := range []struct {
		name string
		unit *walletstore.CurrencyUnitVersion
	}{
		{name: "wrong code", unit: &walletstore.CurrencyUnitVersion{ID: 101, CurrencyCode: "EUR"}},
		{name: "wrong id", unit: &walletstore.CurrencyUnitVersion{ID: 202, CurrencyCode: "USD"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lookupCalls := 0
			service := &Service{
				Store: &walletstore.Store{},
				CurrencyUnitLookup: func(_ context.Context, unitID int64) (*walletstore.CurrencyUnitVersion, error) {
					lookupCalls++
					return tt.unit, nil
				},
				RateLookup: func(context.Context, string, string, int64, string, int64, time.Time) (*walletstore.ExchangeRate, error) {
					t.Fatal("same-currency conversion must not look up an exchange rate")
					return nil, nil
				},
			}

			_, err := service.Convert(t.Context(), "tenant", 100, "USD", 101, "USD", 101)
			if !errors.Is(err, walletstore.ErrCurrencyMismatch) {
				t.Fatalf("Convert() error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
			}
			if lookupCalls != 1 {
				t.Fatalf("currency unit lookup calls = %d, want 1", lookupCalls)
			}
		})
	}
}

func TestServiceConvertSameCurrencyReturnsIdentityAfterUnitValidation(t *testing.T) {
	unit := rateTestUnit("USD", 2)
	unit.ID = 101
	service := &Service{
		Store: &walletstore.Store{},
		CurrencyUnitLookup: func(_ context.Context, unitID int64) (*walletstore.CurrencyUnitVersion, error) {
			if unitID != unit.ID {
				t.Fatalf("unit lookup = %d, want %d", unitID, unit.ID)
			}
			return unit, nil
		},
	}

	got, err := service.Convert(t.Context(), "tenant", 100, "USD", unit.ID, "USD", unit.ID)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if got != 100 {
		t.Fatalf("Convert() = %d, want 100", got)
	}
}

func TestServiceConvertSameCurrencyRejectsMissingOperationalScale(t *testing.T) {
	unit := rateTestUnit("USD", 2)
	unit.ID = 101
	unit.ISOMinorExponent = sql.NullInt16{}
	service := &Service{
		Store: &walletstore.Store{},
		CurrencyUnitLookup: func(_ context.Context, unitID int64) (*walletstore.CurrencyUnitVersion, error) {
			if unitID != unit.ID {
				t.Fatalf("unit lookup = %d, want %d", unitID, unit.ID)
			}
			return unit, nil
		},
	}

	_, err := service.Convert(t.Context(), "tenant", 100, "USD", unit.ID, "USD", unit.ID)
	if !errors.Is(err, walletstore.ErrCurrencyScaleUnavailable) {
		t.Fatalf("Convert() error = %v, want %v", err, walletstore.ErrCurrencyScaleUnavailable)
	}
}
