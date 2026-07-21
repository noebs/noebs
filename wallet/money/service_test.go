package money

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/adonese/noebs/groosh"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var testMoneyDate = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type fakeRepository struct {
	units       map[string]walletstore.CurrencyUnitVersion
	unitsByID   map[int64]walletstore.CurrencyUnitVersion
	codeLookups int
	observation walletstore.FXRateObservation
	source      walletstore.FXSource
	created     walletstore.MoneyConversionQuoteInput
	createCalls int
	quote       *walletstore.MoneyConversionQuote
	nilUnit     bool
}

func (f *fakeRepository) GetCurrencyUnit(_ context.Context, code string, _ time.Time) (*walletstore.CurrencyUnitVersion, error) {
	f.codeLookups++
	if f.nilUnit {
		return nil, nil
	}
	unit, ok := f.units[code]
	if !ok {
		return nil, walletstore.ErrCurrencyNotFound
	}
	return &unit, nil
}

func (f *fakeRepository) GetCurrencyUnitByID(_ context.Context, id int64) (*walletstore.CurrencyUnitVersion, error) {
	if f.nilUnit {
		return nil, nil
	}
	if unit, ok := f.unitsByID[id]; ok {
		copy := unit
		return &copy, nil
	}
	for _, unit := range f.units {
		if unit.ID == id {
			copy := unit
			return &copy, nil
		}
	}
	return nil, walletstore.ErrCurrencyNotFound
}

func (f *fakeRepository) ListCurrencyUnits(context.Context, time.Time, bool) ([]walletstore.CurrencyUnitVersion, error) {
	result := make([]walletstore.CurrencyUnitVersion, 0, len(f.units))
	for _, unit := range f.units {
		result = append(result, unit)
	}
	return result, nil
}

func (f *fakeRepository) GetLatestFXObservation(context.Context, string, string, string, string, time.Time) (*walletstore.FXRateObservation, error) {
	copy := f.observation
	return &copy, nil
}

func (f *fakeRepository) GetFXObservationByID(context.Context, int64) (*walletstore.FXObservation, error) {
	copy := *f.observation.Observation
	return &copy, nil
}

func (f *fakeRepository) GetFXSourceByID(context.Context, int64) (*walletstore.FXSource, error) {
	copy := f.source
	return &copy, nil
}

func (f *fakeRepository) CreateMoneyConversionQuote(_ context.Context, input walletstore.MoneyConversionQuoteInput) (*walletstore.MoneyConversionQuote, error) {
	f.createCalls++
	f.created = input
	return &walletstore.MoneyConversionQuote{
		ID:                             uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		TenantID:                       input.TenantID,
		RequestedByUserID:              input.RequestedByUserID,
		IdempotencyKey:                 input.IdempotencyKey,
		ObservationID:                  input.ObservationID,
		ObservationBaseCurrencyUnitID:  input.ObservationBaseCurrencyUnitID,
		ObservationQuoteCurrencyUnitID: input.ObservationQuoteCurrencyUnitID,
		ObservationBaseCurrencyCode:    input.ObservationBaseCurrencyCode,
		ObservationQuoteCurrencyCode:   input.ObservationQuoteCurrencyCode,
		ObservationExpiresAt:           input.ObservationExpiresAt,
		InputCurrencyUnitID:            input.InputCurrencyUnitID,
		OutputCurrencyUnitID:           input.OutputCurrencyUnitID,
		InputCurrencyCode:              input.InputCurrencyCode,
		OutputCurrencyCode:             input.OutputCurrencyCode,
		InputMinorUnits:                input.InputMinorUnits,
		OutputMinorUnits:               input.OutputMinorUnits,
		Inverse:                        input.Inverse,
		RoundingMode:                   input.RoundingMode,
		ConversionAt:                   input.ConversionAt,
		CreatedAt:                      input.ConversionAt,
		ExpiresAt:                      input.ExpiresAt,
	}, nil
}

func (f *fakeRepository) GetMoneyConversionQuoteByIdempotency(
	_ context.Context,
	tenantID string,
	requestedBy int64,
	idempotencyKey string,
) (*walletstore.MoneyConversionQuote, error) {
	if f.quote == nil || f.quote.TenantID != tenantID ||
		f.quote.RequestedByUserID != requestedBy || f.quote.IdempotencyKey != idempotencyKey {
		return nil, walletstore.ErrConversionQuoteNotFound
	}
	copy := *f.quote
	return &copy, nil
}

func (f *fakeRepository) GetMoneyConversionQuote(context.Context, string, int64, uuid.UUID) (*walletstore.MoneyConversionQuote, error) {
	if f.quote == nil {
		return nil, walletstore.ErrConversionQuoteNotFound
	}
	copy := *f.quote
	return &copy, nil
}

func currencyDefinition(id int64, code string, exponent int16) walletstore.CurrencyUnitVersion {
	return walletstore.CurrencyUnitVersion{
		ID:                    id,
		CurrencyCode:          code,
		Name:                  code,
		Kind:                  "tender",
		IsActive:              true,
		ISOMinorExponent:      sql.NullInt16{Int16: exponent, Valid: true},
		DisplayExponent:       exponent,
		CashExponent:          exponent,
		CashRoundingIncrement: 1,
		ValidFrom:             testMoneyDate,
		Source:                "test",
		SourceRevision:        "test-1",
		SourcePublishedOn:     testMoneyDate,
	}
}

func TestQuoteConversionBindsRateDirectionAndCurrencyVersions(t *testing.T) {
	usd := currencyDefinition(11, "USD", 2)
	kwd := currencyDefinition(22, "KWD", 3)
	rate := decimal.RequireFromString("0.3075")
	expires := testMoneyDate.Add(24 * time.Hour)
	repository := &fakeRepository{
		units: map[string]walletstore.CurrencyUnitVersion{"USD": usd, "KWD": kwd},
		observation: walletstore.FXRateObservation{
			Observation: &walletstore.FXObservation{
				ID:                  77,
				SourceID:            88,
				BaseCurrencyCode:    "USD",
				QuoteCurrencyCode:   "KWD",
				BaseCurrencyUnitID:  usd.ID,
				QuoteCurrencyUnitID: kwd.ID,
				Rate:                rate,
				Side:                walletstore.FXSideMid,
				Purpose:             walletstore.FXPurposeReference,
				ObservationAt:       testMoneyDate,
				RetrievedAt:         testMoneyDate,
				CreatedAt:           testMoneyDate,
				ExpiresAt:           expires,
			},
			BaseUnit:  &usd,
			QuoteUnit: &kwd,
		},
		source: walletstore.FXSource{ID: 88, Code: "test-source", Purpose: walletstore.FXPurposeReference, IsEnabled: true},
	}

	result, err := NewService(repository).QuoteConversion(context.Background(), QuoteParams{
		TenantID:                "tenant-a",
		RequestedBy:             42,
		IdempotencyKey:          "quote-request-1",
		MaxQuotesPerObservation: 100,
		SourceCode:              "test-source",
		BaseCurrency:            "USD",
		BaseCurrencyUnitID:      usd.ID,
		QuoteCurrency:           "KWD",
		QuoteCurrencyUnitID:     kwd.ID,
		InputMinor:              123,
		Side:                    walletstore.FXSideMid,
		RoundingMode:            groosh.RoundHalfAwayFromZero,
		ConversionTime:          testMoneyDate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output.MinorUnits() != 378 {
		t.Fatalf("output minor units = %d, want 378", result.Output.MinorUnits())
	}
	if result.Input.UnitVersionID() != usd.ID || result.Output.UnitVersionID() != kwd.ID {
		t.Fatalf("unit versions = %d -> %d, want %d -> %d", result.Input.UnitVersionID(), result.Output.UnitVersionID(), usd.ID, kwd.ID)
	}
	if repository.created.ObservationID != 77 || repository.created.Inverse || repository.created.RoundingMode != "half_away_from_zero" {
		t.Fatalf("persisted quote = %+v", repository.created)
	}
	if result.AppliedRate.Cmp(big.NewRat(123, 400)) != 0 {
		t.Fatalf("applied rate = %s, want 123/400", result.AppliedRate.RatString())
	}
}

func TestQuoteConversionPreservesPositiveInputThatRoundsToZero(t *testing.T) {
	repository, params := validQuoteTestRepository()
	repository.observation.Observation.Rate = decimal.RequireFromString("0.00001")
	params.InputMinor = 1

	created, err := NewService(repository).QuoteConversion(t.Context(), params)
	if err != nil {
		t.Fatalf("QuoteConversion() error = %v", err)
	}
	if created.Input.MinorUnits() != 1 || created.Output.MinorUnits() != 0 {
		t.Fatalf("quote amounts = %d -> %d, want 1 -> 0", created.Input.MinorUnits(), created.Output.MinorUnits())
	}
	if repository.created.OutputMinorUnits != 0 || created.Quote.OutputMinorUnits != 0 {
		t.Fatalf("persisted zero output = input %+v, quote %+v", repository.created, created.Quote)
	}

	repository.quote = &created.Quote
	loaded, err := NewService(repository).GetConversionQuote(
		t.Context(), created.Quote.TenantID, created.Quote.RequestedByUserID, created.Quote.ID,
	)
	if err != nil {
		t.Fatalf("GetConversionQuote() zero output error = %v", err)
	}
	if loaded.Output.MinorUnits() != 0 {
		t.Fatalf("loaded output = %d, want 0", loaded.Output.MinorUnits())
	}
}

func TestQuoteConversionInvertsOnlyWhenRepositoryMarksObservationInverse(t *testing.T) {
	usd := currencyDefinition(11, "USD", 2)
	kwd := currencyDefinition(22, "KWD", 3)
	repository := &fakeRepository{
		units: map[string]walletstore.CurrencyUnitVersion{"USD": usd, "KWD": kwd},
		observation: walletstore.FXRateObservation{
			Observation: &walletstore.FXObservation{
				ID: 77, SourceID: 88, BaseCurrencyCode: "USD", QuoteCurrencyCode: "KWD",
				BaseCurrencyUnitID: usd.ID, QuoteCurrencyUnitID: kwd.ID,
				Rate: decimal.RequireFromString("0.3075"), Side: walletstore.FXSideMid,
				Purpose:       walletstore.FXPurposeReference,
				ObservationAt: testMoneyDate, RetrievedAt: testMoneyDate, CreatedAt: testMoneyDate,
				ExpiresAt: testMoneyDate.Add(time.Hour),
			},
			BaseUnit: &usd, QuoteUnit: &kwd, Inverse: true,
		},
		source: walletstore.FXSource{ID: 88, Code: "test-source", Purpose: walletstore.FXPurposeReference, IsEnabled: true},
	}
	result, err := NewService(repository).QuoteConversion(context.Background(), QuoteParams{
		TenantID: "tenant-a", RequestedBy: 42, SourceCode: "test-source",
		IdempotencyKey: "quote-request-1", MaxQuotesPerObservation: 100,
		BaseCurrency: "KWD", BaseCurrencyUnitID: kwd.ID,
		QuoteCurrency: "USD", QuoteCurrencyUnitID: usd.ID, InputMinor: 1000,
		Side: walletstore.FXSideMid, RoundingMode: groosh.RoundHalfAwayFromZero, ConversionTime: testMoneyDate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output.MinorUnits() != 325 || !repository.created.Inverse {
		t.Fatalf("inverse output = %d, persisted inverse = %t; want 325, true", result.Output.MinorUnits(), repository.created.Inverse)
	}
}

func TestQuoteConversionRejectsRepositoryPairSideTimeAndSourceViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeRepository)
	}{
		{
			name: "inverse flag",
			mutate: func(repository *fakeRepository) {
				repository.observation.Inverse = true
			},
		},
		{
			name: "rate side",
			mutate: func(repository *fakeRepository) {
				repository.observation.Observation.Side = walletstore.FXSideAsk
			},
		},
		{
			name: "future retrieval",
			mutate: func(repository *fakeRepository) {
				repository.observation.Observation.RetrievedAt = testMoneyDate.Add(time.Second)
			},
		},
		{
			name: "persisted after conversion",
			mutate: func(repository *fakeRepository) {
				repository.observation.Observation.CreatedAt = testMoneyDate.Add(time.Second)
			},
		},
		{
			name: "source code",
			mutate: func(repository *fakeRepository) {
				repository.source.Code = "other-source"
			},
		},
		{
			name: "observation pair",
			mutate: func(repository *fakeRepository) {
				repository.observation.Observation.BaseCurrencyCode = "KWD"
				repository.observation.Observation.QuoteCurrencyCode = "USD"
				repository.observation.BaseUnit, repository.observation.QuoteUnit =
					repository.observation.QuoteUnit, repository.observation.BaseUnit
				repository.observation.Observation.BaseCurrencyUnitID = repository.observation.BaseUnit.ID
				repository.observation.Observation.QuoteCurrencyUnitID = repository.observation.QuoteUnit.ID
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, params := validQuoteTestRepository()
			test.mutate(repository)
			_, err := NewService(repository).QuoteConversion(t.Context(), params)
			if !errors.Is(err, ErrObservationPairMismatch) {
				t.Fatalf("QuoteConversion() error = %v, want %v", err, ErrObservationPairMismatch)
			}
		})
	}
}

func TestGetConversionQuoteRecomputesPersistedOutput(t *testing.T) {
	repository, params := validQuoteTestRepository()
	created, err := NewService(repository).QuoteConversion(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	repository.quote = &created.Quote

	loaded, err := NewService(repository).GetConversionQuote(
		t.Context(), created.Quote.TenantID, created.Quote.RequestedByUserID, created.Quote.ID,
	)
	if err != nil {
		t.Fatalf("GetConversionQuote() error = %v", err)
	}
	if loaded.Output.MinorUnits() != created.Output.MinorUnits() {
		t.Fatalf("loaded output = %d, want %d", loaded.Output.MinorUnits(), created.Output.MinorUnits())
	}

	repository.quote.OutputMinorUnits++
	_, err = NewService(repository).GetConversionQuote(
		t.Context(), created.Quote.TenantID, created.Quote.RequestedByUserID, created.Quote.ID,
	)
	if !errors.Is(err, ErrQuoteIntegrity) {
		t.Fatalf("corrupt GetConversionQuote() error = %v, want %v", err, ErrQuoteIntegrity)
	}
}

func TestQuoteConversionIdempotencyReturnsOriginalAndRejectsKeyReuse(t *testing.T) {
	repository, params := validQuoteTestRepository()
	service := NewService(repository)
	created, err := service.QuoteConversion(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	repository.quote = &created.Quote

	retry := params
	retry.ConversionTime = created.Quote.ExpiresAt.Add(time.Hour)
	replayed, err := service.QuoteConversion(t.Context(), retry)
	if err != nil {
		t.Fatalf("idempotent retry error = %v", err)
	}
	if replayed.Quote.ID != created.Quote.ID || repository.createCalls != 1 {
		t.Fatalf("retry quote/calls = %s/%d, want %s/1", replayed.Quote.ID, repository.createCalls, created.Quote.ID)
	}

	conflict := params
	conflict.InputMinor++
	if _, err := service.QuoteConversion(t.Context(), conflict); !errors.Is(err, ErrQuoteIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v, want %v", err, ErrQuoteIdempotencyConflict)
	}
}

func TestQuoteConversionRequiresExplicitIdempotencyAndQuota(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*QuoteParams)
		want   error
	}{
		{name: "missing key", mutate: func(params *QuoteParams) { params.IdempotencyKey = "" }, want: walletstore.ErrMissingIdempotencyKey},
		{name: "invalid key", mutate: func(params *QuoteParams) { params.IdempotencyKey = " key " }, want: walletstore.ErrInvalidIdempotencyKey},
		{name: "missing quota", mutate: func(params *QuoteParams) { params.MaxQuotesPerObservation = 0 }, want: walletstore.ErrInvalidLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, params := validQuoteTestRepository()
			test.mutate(&params)
			if _, err := NewService(repository).QuoteConversion(t.Context(), params); !errors.Is(err, test.want) {
				t.Fatalf("QuoteConversion() error = %v, want %v", err, test.want)
			}
			if repository.createCalls != 0 {
				t.Fatalf("invalid request performed %d quote writes", repository.createCalls)
			}
		})
	}
}

func TestParseAndFormatRequireExactCurrencyUnitIdentity(t *testing.T) {
	validTo := testMoneyDate.Add(24 * time.Hour)
	historical := currencyDefinition(71, "USD", 2)
	historical.IsActive = false
	historical.ValidTo = sql.NullTime{Time: validTo, Valid: true}
	repository := &fakeRepository{
		units:     map[string]walletstore.CurrencyUnitVersion{"USD": historical},
		unitsByID: map[int64]walletstore.CurrencyUnitVersion{historical.ID: historical},
	}
	service := NewService(repository)

	amount, display, err := service.FormatMinor(t.Context(), "USD", historical.ID, 1234, groosh.RoundHalfEven)
	if err != nil {
		t.Fatalf("FormatMinor() historical error = %v", err)
	}
	if amount.UnitVersionID() != historical.ID || display != "USD 12.34" {
		t.Fatalf("historical format = %d/%q, want 71/USD 12.34", amount.UnitVersionID(), display)
	}

	if _, err := service.ParseMajor(t.Context(), "USD", historical.ID, "12.34", testMoneyDate); !errors.Is(err, ErrInactiveCurrency) {
		t.Fatalf("ParseMajor() inactive error = %v, want %v", err, ErrInactiveCurrency)
	}
	if _, _, err := service.FormatMinor(t.Context(), "KWD", historical.ID, 1234, groosh.RoundHalfEven); !errors.Is(err, walletstore.ErrCurrencyMismatch) {
		t.Fatalf("FormatMinor() mismatched code/id error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
	}
	if _, err := service.ParseMajor(t.Context(), "USD", 0, "12.34", testMoneyDate); !errors.Is(err, walletstore.ErrMissingCurrencyUnitID) {
		t.Fatalf("ParseMajor() missing unit error = %v, want %v", err, walletstore.ErrMissingCurrencyUnitID)
	}
	if _, err := service.ParseMajor(t.Context(), "USD", -1, "12.34", testMoneyDate); !errors.Is(err, walletstore.ErrInvalidCurrencyUnitID) {
		t.Fatalf("ParseMajor() invalid unit error = %v, want %v", err, walletstore.ErrInvalidCurrencyUnitID)
	}
	if repository.codeLookups != 0 {
		t.Fatalf("exact parse/format performed %d code-only currency lookups", repository.codeLookups)
	}
}

func TestCurrencyLookupsRejectNilRepositoryRows(t *testing.T) {
	service := NewService(&fakeRepository{nilUnit: true})
	if _, err := service.GetCurrency(t.Context(), "USD", testMoneyDate, true); !errors.Is(err, ErrInvalidCurrencyUnitData) {
		t.Fatalf("GetCurrency() nil row error = %v, want %v", err, ErrInvalidCurrencyUnitData)
	}
	if _, err := service.GetCurrencyByUnitID(t.Context(), 1); !errors.Is(err, ErrInvalidCurrencyUnitData) {
		t.Fatalf("GetCurrencyByUnitID() nil row error = %v, want %v", err, ErrInvalidCurrencyUnitData)
	}
}

func TestParseRejectsUnitOutsideEffectiveInterval(t *testing.T) {
	ended := currencyDefinition(81, "USD", 2)
	ended.ValidTo = sql.NullTime{Time: testMoneyDate.Add(time.Hour), Valid: true}
	repository := &fakeRepository{unitsByID: map[int64]walletstore.CurrencyUnitVersion{ended.ID: ended}}

	_, err := NewService(repository).ParseMajor(
		t.Context(), "USD", ended.ID, "1.00", testMoneyDate.Add(2*time.Hour),
	)
	if !errors.Is(err, walletstore.ErrInvalidUsageTime) {
		t.Fatalf("ParseMajor() ended unit error = %v, want %v", err, walletstore.ErrInvalidUsageTime)
	}
}

func TestQuoteConversionUsesCallerPinnedTransitionUnits(t *testing.T) {
	repository, params := validQuoteTestRepository()
	transition := testMoneyDate.Add(time.Hour)
	oldUSD := *repository.observation.BaseUnit
	oldUSD.ValidTo = sql.NullTime{Time: transition, Valid: true}
	repository.observation.BaseUnit = &oldUSD
	newUSD := currencyDefinition(33, "USD", 3)
	newUSD.ValidFrom = transition
	repository.unitsByID = map[int64]walletstore.CurrencyUnitVersion{
		oldUSD.ID:                           oldUSD,
		newUSD.ID:                           newUSD,
		repository.observation.QuoteUnit.ID: *repository.observation.QuoteUnit,
	}
	params.BaseCurrencyUnitID = newUSD.ID
	params.ConversionTime = transition.Add(time.Hour)

	quote, err := NewService(repository).QuoteConversion(t.Context(), params)
	if err != nil {
		t.Fatalf("QuoteConversion() transition error = %v", err)
	}
	if quote.Input.UnitVersionID() != newUSD.ID || quote.Input.MinorUnits() != 123 {
		t.Fatalf("input = version %d amount %d, want version 33 amount 123", quote.Input.UnitVersionID(), quote.Input.MinorUnits())
	}
	if quote.Output.MinorUnits() != 38 {
		t.Fatalf("transition output = %d, want 38 from three-decimal USD input", quote.Output.MinorUnits())
	}
	if repository.created.InputCurrencyUnitID != newUSD.ID || !repository.created.ConversionAt.Equal(params.ConversionTime) {
		t.Fatalf("persisted transition identity/time = %+v", repository.created)
	}
	if repository.codeLookups != 0 {
		t.Fatalf("quote performed %d code-only currency lookups", repository.codeLookups)
	}
}

func TestQuoteConversionRejectsMismatchedPinnedCodeAndNormalizesConversionTime(t *testing.T) {
	repository, params := validQuoteTestRepository()
	params.BaseCurrencyUnitID = params.QuoteCurrencyUnitID
	_, err := NewService(repository).QuoteConversion(t.Context(), params)
	if !errors.Is(err, walletstore.ErrCurrencyMismatch) {
		t.Fatalf("QuoteConversion() mismatched code/id error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
	}

	repository, params = validQuoteTestRepository()
	params.ConversionTime = testMoneyDate.Add(123456789 * time.Nanosecond)
	quote, err := NewService(repository).QuoteConversion(t.Context(), params)
	if err != nil {
		t.Fatalf("QuoteConversion() precise time error = %v", err)
	}
	want := params.ConversionTime.UTC().Truncate(time.Microsecond)
	if !quote.Quote.ConversionAt.Equal(want) || !repository.created.ConversionAt.Equal(want) {
		t.Fatalf("conversion_at = %s/%s, want %s", quote.Quote.ConversionAt, repository.created.ConversionAt, want)
	}
}

func TestGetConversionQuoteRejectsConversionTimeTampering(t *testing.T) {
	repository, params := validQuoteTestRepository()
	created, err := NewService(repository).QuoteConversion(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	repository.quote = &created.Quote
	repository.quote.ConversionAt = repository.quote.ExpiresAt
	repository.quote.CreatedAt = repository.quote.ConversionAt

	_, err = NewService(repository).GetConversionQuote(
		t.Context(), created.Quote.TenantID, created.Quote.RequestedByUserID, created.Quote.ID,
	)
	if !errors.Is(err, ErrQuoteIntegrity) {
		t.Fatalf("tampered conversion_at error = %v, want %v", err, ErrQuoteIntegrity)
	}
}

func TestGetConversionQuoteRejectsObservationPersistedAfterConversion(t *testing.T) {
	repository, params := validQuoteTestRepository()
	created, err := NewService(repository).QuoteConversion(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	repository.quote = &created.Quote
	repository.observation.Observation.CreatedAt = created.Quote.ConversionAt.Add(time.Microsecond)

	_, err = NewService(repository).GetConversionQuote(
		t.Context(), created.Quote.TenantID, created.Quote.RequestedByUserID, created.Quote.ID,
	)
	if !errors.Is(err, ErrQuoteIntegrity) {
		t.Fatalf("late observation hydration error = %v, want %v", err, ErrQuoteIntegrity)
	}
}

func TestQuotePersistenceMatchIncludesExactConversionTime(t *testing.T) {
	repository, params := validQuoteTestRepository()
	created, err := NewService(repository).QuoteConversion(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if !moneyConversionQuoteMatchesInput(&created.Quote, repository.created) {
		t.Fatal("exact persisted quote did not match its creation input")
	}
	tampered := repository.created
	tampered.ConversionAt = tampered.ConversionAt.Add(time.Microsecond)
	if moneyConversionQuoteMatchesInput(&created.Quote, tampered) {
		t.Fatal("conversion_at mismatch was omitted from quote persistence integrity check")
	}
}

func validQuoteTestRepository() (*fakeRepository, QuoteParams) {
	usd := currencyDefinition(11, "USD", 2)
	kwd := currencyDefinition(22, "KWD", 3)
	expires := testMoneyDate.Add(24 * time.Hour)
	repository := &fakeRepository{
		units: map[string]walletstore.CurrencyUnitVersion{"USD": usd, "KWD": kwd},
		observation: walletstore.FXRateObservation{
			Observation: &walletstore.FXObservation{
				ID: 77, SourceID: 88, BaseCurrencyCode: "USD", QuoteCurrencyCode: "KWD",
				BaseCurrencyUnitID: usd.ID, QuoteCurrencyUnitID: kwd.ID,
				Rate: decimal.RequireFromString("0.3075"), Side: walletstore.FXSideMid,
				Purpose: walletstore.FXPurposeReference, ObservationAt: testMoneyDate,
				RetrievedAt: testMoneyDate, CreatedAt: testMoneyDate, ExpiresAt: expires,
			},
			BaseUnit: &usd, QuoteUnit: &kwd,
		},
		source: walletstore.FXSource{
			ID: 88, Code: "test-source", Purpose: walletstore.FXPurposeReference, IsEnabled: true,
		},
	}
	return repository, QuoteParams{
		TenantID: "tenant-a", RequestedBy: 42, SourceCode: "test-source",
		IdempotencyKey: "quote-request-1", MaxQuotesPerObservation: 100,
		BaseCurrency: "USD", BaseCurrencyUnitID: usd.ID,
		QuoteCurrency: "KWD", QuoteCurrencyUnitID: kwd.ID, InputMinor: 123,
		Side: walletstore.FXSideMid, RoundingMode: groosh.RoundHalfEven,
		ConversionTime: testMoneyDate,
	}
}

func TestCurrencyFromDefinitionPreservesMissingISOScale(t *testing.T) {
	definition := currencyDefinition(1, "XXX", 0)
	definition.ISOMinorExponent = sql.NullInt16{}
	currency, err := currencyFromDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := currency.Unit.ISOMinorExponent(); present {
		t.Fatal("missing ISO exponent became present")
	}
	_, err = groosh.NewMoney(0, currency.Unit)
	if !errors.Is(err, groosh.ErrMissingISOMinorExponent) {
		t.Fatalf("NewMoney() error = %v, want missing ISO exponent", err)
	}
}

func TestGetCurrencyByUnitIDRendersHistoricalInactiveVersion(t *testing.T) {
	definition := currencyDefinition(71, "USD", 2)
	definition.IsActive = false
	service := NewService(&fakeRepository{units: map[string]walletstore.CurrencyUnitVersion{"USD": definition}})

	currency, err := service.GetCurrencyByUnitID(context.Background(), 71)
	if err != nil {
		t.Fatalf("GetCurrencyByUnitID() error = %v", err)
	}
	if currency.Definition.ID != 71 || currency.Definition.IsActive {
		t.Fatalf("currency = %+v, want exact inactive version 71", currency.Definition)
	}
}

func TestServiceDoesNotDefaultMissingRepository(t *testing.T) {
	_, err := (*Service)(nil).ListCurrencies(context.Background(), testMoneyDate, true)
	if !errors.Is(err, ErrMissingRepository) {
		t.Fatalf("ListCurrencies() error = %v, want %v", err, ErrMissingRepository)
	}
}
