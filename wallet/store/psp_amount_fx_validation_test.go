package store

import (
	"database/sql"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func validPSPFXAmount() PSPTransactionAmount {
	return PSPTransactionAmount{
		TenantID:              "tenant",
		PSPTransactionID:      1,
		AmountKind:            PSPAmountWalletDebit,
		Amount:                100,
		Currency:              "AED",
		CurrencyUnitID:        11,
		FxRate:                decimal.NullDecimal{Decimal: decimal.RequireFromString("3.75000000"), Valid: true},
		FxRateNumerator:       decimal.NullDecimal{Decimal: decimal.NewFromInt(15), Valid: true},
		FxRateDenominator:     decimal.NullDecimal{Decimal: decimal.NewFromInt(4), Valid: true},
		FxBaseCurrency:        sql.NullString{String: "USD", Valid: true},
		FxQuoteCurrency:       sql.NullString{String: "AED", Valid: true},
		FxBaseCurrencyUnitID:  sql.NullInt64{Int64: 12, Valid: true},
		FxQuoteCurrencyUnitID: sql.NullInt64{Int64: 11, Valid: true},
		FxSource:              sql.NullString{String: "provider", Valid: true},
		FxConversionAt:        sql.NullTime{Time: time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC), Valid: true},
	}
}

func TestValidatePSPTransactionAmountRequiresExactFXMetadata(t *testing.T) {
	valid := validPSPFXAmount()
	if _, err := validatePSPTransactionAmount(valid); err != nil {
		t.Fatalf("valid FX amount: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*PSPTransactionAmount)
		wantErr error
	}{
		{"zero rate", func(amount *PSPTransactionAmount) { amount.FxRate.Decimal = decimal.Zero }, ErrInvalidRate},
		{"negative rate", func(amount *PSPTransactionAmount) { amount.FxRate.Decimal = decimal.NewFromInt(-1) }, ErrInvalidRate},
		{"excess scale", func(amount *PSPTransactionAmount) { amount.FxRate.Decimal = decimal.RequireFromString("1.123456789") }, ErrPSPFXRateNotRepresentable},
		{"excess precision", func(amount *PSPTransactionAmount) {
			amount.FxRate.Decimal = decimal.RequireFromString("10000000000.00000000")
		}, ErrPSPFXRateNotRepresentable},
		{"missing numerator", func(amount *PSPTransactionAmount) { amount.FxRateNumerator = decimal.NullDecimal{} }, ErrMissingFXRateFraction},
		{"missing denominator", func(amount *PSPTransactionAmount) { amount.FxRateDenominator = decimal.NullDecimal{} }, ErrMissingFXRateFraction},
		{"zero numerator", func(amount *PSPTransactionAmount) { amount.FxRateNumerator.Decimal = decimal.Zero }, ErrInvalidFXRateFraction},
		{"negative denominator", func(amount *PSPTransactionAmount) { amount.FxRateDenominator.Decimal = decimal.NewFromInt(-4) }, ErrInvalidFXRateFraction},
		{"fractional numerator", func(amount *PSPTransactionAmount) { amount.FxRateNumerator.Decimal = decimal.RequireFromString("15.5") }, ErrPSPFXRateFractionNotRepresentable},
		{"unreduced fraction", func(amount *PSPTransactionAmount) {
			amount.FxRateNumerator.Decimal = decimal.NewFromInt(30)
			amount.FxRateDenominator.Decimal = decimal.NewFromInt(8)
		}, ErrInvalidFXRateFraction},
		{"fraction projection mismatch", func(amount *PSPTransactionAmount) { amount.FxRateNumerator.Decimal = decimal.NewFromInt(16) }, ErrInvalidFXRateFraction},
		{"fraction numerator overflow", func(amount *PSPTransactionAmount) {
			amount.FxRateNumerator.Decimal = decimal.RequireFromString("100000000000000000000000000000000000000")
		}, ErrPSPFXRateFractionNotRepresentable},
		{"fraction projection overflow", func(amount *PSPTransactionAmount) {
			amount.FxRateNumerator.Decimal = decimal.RequireFromString("10000000000")
			amount.FxRateDenominator.Decimal = decimal.NewFromInt(1)
		}, ErrPSPFXRateNotRepresentable},
		{"fraction projection rounds to zero", func(amount *PSPTransactionAmount) {
			amount.FxRateNumerator.Decimal = decimal.NewFromInt(1)
			amount.FxRateDenominator.Decimal = decimal.NewFromInt(250_000_000)
		}, ErrPSPFXRateNotRepresentable},
		{"fraction hostile exponent", func(amount *PSPTransactionAmount) {
			amount.FxRateNumerator.Decimal = decimal.New(1, 1_000_000)
		}, ErrPSPFXRateFractionNotRepresentable},
		{"missing base", func(amount *PSPTransactionAmount) { amount.FxBaseCurrency = sql.NullString{} }, ErrMissingFXCurrency},
		{"missing quote", func(amount *PSPTransactionAmount) { amount.FxQuoteCurrency = sql.NullString{} }, ErrMissingFXCurrency},
		{"blank base", func(amount *PSPTransactionAmount) { amount.FxBaseCurrency = sql.NullString{String: " ", Valid: true} }, ErrMissingFXCurrency},
		{"noncanonical base", func(amount *PSPTransactionAmount) {
			amount.FxBaseCurrency = sql.NullString{String: " USD", Valid: true}
		}, ErrInvalidCurrency},
		{"invalid quote", func(amount *PSPTransactionAmount) {
			amount.FxQuoteCurrency = sql.NullString{String: "aed", Valid: true}
		}, ErrInvalidCurrency},
		{"identical currencies", func(amount *PSPTransactionAmount) {
			amount.FxBaseCurrency = amount.FxQuoteCurrency
			amount.FxBaseCurrencyUnitID = amount.FxQuoteCurrencyUnitID
		}, ErrIdenticalCurrencies},
		{"missing base unit", func(amount *PSPTransactionAmount) { amount.FxBaseCurrencyUnitID = sql.NullInt64{} }, ErrMissingCurrencyUnitID},
		{"missing quote unit", func(amount *PSPTransactionAmount) { amount.FxQuoteCurrencyUnitID = sql.NullInt64{} }, ErrMissingCurrencyUnitID},
		{"missing source", func(amount *PSPTransactionAmount) { amount.FxSource = sql.NullString{} }, ErrMissingFXSource},
		{"blank source", func(amount *PSPTransactionAmount) { amount.FxSource = sql.NullString{String: " \t", Valid: true} }, ErrMissingFXSource},
		{"untrimmed source", func(amount *PSPTransactionAmount) { amount.FxSource = sql.NullString{String: " provider", Valid: true} }, ErrInvalidFXSource},
		{"missing conversion time", func(amount *PSPTransactionAmount) { amount.FxConversionAt = sql.NullTime{} }, ErrMissingFXConversionTime},
		{"sub-microsecond conversion time", func(amount *PSPTransactionAmount) {
			amount.FxConversionAt.Time = amount.FxConversionAt.Time.Add(time.Nanosecond)
		}, ErrInvalidFXConversionTime},
		{"amount currency is not a pair side", func(amount *PSPTransactionAmount) { amount.Currency = "EUR" }, ErrCurrencyMismatch},
		{"amount unit is not the declared side unit", func(amount *PSPTransactionAmount) { amount.CurrencyUnitID++ }, ErrCurrencyMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			amount := valid
			test.mutate(&amount)
			_, err := validatePSPTransactionAmount(amount)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidatePSPTransactionAmountRequiresCompleteReferenceProvenance(t *testing.T) {
	observationID := int64(77)
	quoteID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	withObservation := func(amount *PSPTransactionAmount, inverse bool) {
		amount.FxObservationID = sql.NullInt64{Int64: observationID, Valid: true}
		if inverse {
			amount.FxObservationBaseCurrency = sql.NullString{String: "AED", Valid: true}
			amount.FxObservationQuoteCurrency = sql.NullString{String: "USD", Valid: true}
		} else {
			amount.FxObservationBaseCurrency = sql.NullString{String: "USD", Valid: true}
			amount.FxObservationQuoteCurrency = sql.NullString{String: "AED", Valid: true}
		}
		amount.FxObservationBaseCurrencyUnitID = sql.NullInt64{Int64: 21, Valid: true}
		amount.FxObservationQuoteCurrencyUnitID = sql.NullInt64{Int64: 22, Valid: true}
	}

	for _, inverse := range []bool{false, true} {
		name := "direct"
		if inverse {
			name = "inverse"
		}
		t.Run(name+" observation pair", func(t *testing.T) {
			amount := validPSPFXAmount()
			withObservation(&amount, inverse)
			if _, err := validatePSPTransactionAmount(amount); err != nil {
				t.Fatalf("valid %s observation metadata: %v", name, err)
			}
		})
	}

	tests := []struct {
		name    string
		mutate  func(*PSPTransactionAmount)
		wantErr error
	}{
		{
			name: "partial observation snapshot",
			mutate: func(amount *PSPTransactionAmount) {
				withObservation(amount, false)
				amount.FxObservationQuoteCurrency = sql.NullString{}
			},
			wantErr: ErrMissingFXCurrency,
		},
		{
			name: "quote without observation",
			mutate: func(amount *PSPTransactionAmount) {
				amount.FxQuoteID = uuid.NullUUID{UUID: quoteID, Valid: true}
			},
			wantErr: ErrMissingFXObservation,
		},
		{
			name: "nil quote uuid",
			mutate: func(amount *PSPTransactionAmount) {
				withObservation(amount, false)
				amount.FxQuoteID = uuid.NullUUID{Valid: true}
			},
			wantErr: ErrMissingConversionQuoteID,
		},
		{
			name: "unrelated observation pair",
			mutate: func(amount *PSPTransactionAmount) {
				withObservation(amount, false)
				amount.FxObservationBaseCurrency = sql.NullString{String: "EUR", Valid: true}
			},
			wantErr: ErrPSPFXProvenanceMismatch,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			amount := validPSPFXAmount()
			test.mutate(&amount)
			_, err := validatePSPTransactionAmount(amount)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidatePSPTransactionAmountRejectsMetadataWithoutRate(t *testing.T) {
	base := PSPTransactionAmount{
		TenantID:         "tenant",
		PSPTransactionID: 1,
		AmountKind:       PSPAmountSettlement,
		Amount:           100,
		Currency:         "USD",
		CurrencyUnitID:   11,
	}
	tests := map[string]func(*PSPTransactionAmount){
		"hidden invalid rate": func(amount *PSPTransactionAmount) {
			amount.FxRate.Decimal = decimal.NewFromInt(1)
		},
		"rate numerator": func(amount *PSPTransactionAmount) {
			amount.FxRateNumerator = decimal.NullDecimal{Decimal: decimal.NewFromInt(1), Valid: true}
		},
		"hidden invalid rate numerator": func(amount *PSPTransactionAmount) {
			amount.FxRateNumerator.Decimal = decimal.NewFromInt(1)
		},
		"rate denominator": func(amount *PSPTransactionAmount) {
			amount.FxRateDenominator = decimal.NullDecimal{Decimal: decimal.NewFromInt(1), Valid: true}
		},
		"hidden invalid rate denominator": func(amount *PSPTransactionAmount) {
			amount.FxRateDenominator.Decimal = decimal.NewFromInt(1)
		},
		"base currency": func(amount *PSPTransactionAmount) { amount.FxBaseCurrency = sql.NullString{String: "USD", Valid: true} },
		"quote currency": func(amount *PSPTransactionAmount) {
			amount.FxQuoteCurrency = sql.NullString{String: "AED", Valid: true}
		},
		"base unit": func(amount *PSPTransactionAmount) {
			amount.FxBaseCurrencyUnitID = sql.NullInt64{Int64: 11, Valid: true}
		},
		"quote unit": func(amount *PSPTransactionAmount) {
			amount.FxQuoteCurrencyUnitID = sql.NullInt64{Int64: 12, Valid: true}
		},
		"source": func(amount *PSPTransactionAmount) { amount.FxSource = sql.NullString{String: "provider", Valid: true} },
		"hidden invalid source": func(amount *PSPTransactionAmount) {
			amount.FxSource = sql.NullString{String: "provider", Valid: false}
		},
		"observation id": func(amount *PSPTransactionAmount) { amount.FxObservationID = sql.NullInt64{Int64: 7, Valid: true} },
		"quote id": func(amount *PSPTransactionAmount) {
			amount.FxQuoteID = uuid.NullUUID{UUID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), Valid: true}
		},
		"conversion time": func(amount *PSPTransactionAmount) {
			amount.FxConversionAt = sql.NullTime{Time: time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC), Valid: true}
		},
		"observation base currency": func(amount *PSPTransactionAmount) {
			amount.FxObservationBaseCurrency = sql.NullString{String: "USD", Valid: true}
		},
		"observation quote currency": func(amount *PSPTransactionAmount) {
			amount.FxObservationQuoteCurrency = sql.NullString{String: "AED", Valid: true}
		},
		"observation base unit": func(amount *PSPTransactionAmount) {
			amount.FxObservationBaseCurrencyUnitID = sql.NullInt64{Int64: 11, Valid: true}
		},
		"observation quote unit": func(amount *PSPTransactionAmount) {
			amount.FxObservationQuoteCurrencyUnitID = sql.NullInt64{Int64: 12, Valid: true}
		},
	}
	for name, addOne := range tests {
		t.Run(name, func(t *testing.T) {
			amount := base
			addOne(&amount)
			_, err := validatePSPTransactionAmount(amount)
			if !errors.Is(err, ErrMissingFXRate) {
				t.Fatalf("error = %v, want %v", err, ErrMissingFXRate)
			}
		})
	}
}

func TestBuildPSPTransactionAmountDoesNotEraseInvalidFXUnitMetadata(t *testing.T) {
	amount := buildPSPTransactionAmount("tenant", 1, PSPTransactionAmountInput{
		AmountKind:           PSPAmountSettlement,
		Amount:               100,
		Currency:             "USD",
		CurrencyUnitID:       11,
		FxBaseCurrencyUnitID: -1,
	})
	if _, err := validatePSPTransactionAmount(amount); !errors.Is(err, ErrMissingFXRate) {
		t.Fatalf("negative FX unit without rate error = %v, want %v", err, ErrMissingFXRate)
	}

	amount = validPSPFXAmount()
	amount.FxBaseCurrencyUnitID = nullNonZeroInt64(-1)
	if _, err := validatePSPTransactionAmount(amount); !errors.Is(err, ErrInvalidCurrencyUnitID) {
		t.Fatalf("negative FX unit with rate error = %v, want %v", err, ErrInvalidCurrencyUnitID)
	}
}

func TestValidatePSPTransactionAmountReplayIncludesEveryFXProvenanceField(t *testing.T) {
	existing := validPSPFXAmount()
	existing.FxObservationID = sql.NullInt64{Int64: 77, Valid: true}
	existing.FxQuoteID = uuid.NullUUID{
		UUID:  uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		Valid: true,
	}
	existing.FxObservationBaseCurrency = sql.NullString{String: "USD", Valid: true}
	existing.FxObservationQuoteCurrency = sql.NullString{String: "AED", Valid: true}
	existing.FxObservationBaseCurrencyUnitID = sql.NullInt64{Int64: 31, Valid: true}
	existing.FxObservationQuoteCurrencyUnitID = sql.NullInt64{Int64: 32, Valid: true}

	mutations := map[string]func(*PSPTransactionAmount){
		"rate numerator":   func(amount *PSPTransactionAmount) { amount.FxRateNumerator.Decimal = decimal.NewFromInt(16) },
		"rate denominator": func(amount *PSPTransactionAmount) { amount.FxRateDenominator.Decimal = decimal.NewFromInt(5) },
		"observation id":   func(amount *PSPTransactionAmount) { amount.FxObservationID.Int64++ },
		"quote id":         func(amount *PSPTransactionAmount) { amount.FxQuoteID.UUID = uuid.New() },
		"conversion time": func(amount *PSPTransactionAmount) {
			amount.FxConversionAt.Time = amount.FxConversionAt.Time.Add(time.Microsecond)
		},
		"observation base currency": func(amount *PSPTransactionAmount) {
			amount.FxObservationBaseCurrency.String = "EUR"
		},
		"observation quote currency": func(amount *PSPTransactionAmount) {
			amount.FxObservationQuoteCurrency.String = "GBP"
		},
		"observation base unit": func(amount *PSPTransactionAmount) {
			amount.FxObservationBaseCurrencyUnitID.Int64++
		},
		"observation quote unit": func(amount *PSPTransactionAmount) {
			amount.FxObservationQuoteCurrencyUnitID.Int64++
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			requested := existing
			mutate(&requested)
			if err := ValidatePSPTransactionAmountReplay(&existing, requested); !errors.Is(err, ErrDuplicateAmount) {
				t.Fatalf("replay mismatch error = %v, want %v", err, ErrDuplicateAmount)
			}
		})
	}
}

func TestCanonicalFXRateFractionAndProjection(t *testing.T) {
	numerator, denominator, err := CanonicalFXRateFraction(decimal.NullDecimal{
		Decimal: decimal.RequireFromString("3.75000000"),
		Valid:   true,
	})
	if err != nil {
		t.Fatalf("CanonicalFXRateFraction() error = %v", err)
	}
	if !numerator.Decimal.Equal(decimal.NewFromInt(15)) || !denominator.Decimal.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("fraction = %s/%s, want 15/4", numerator.Decimal, denominator.Decimal)
	}
	projection, inverseNumerator, inverseDenominator, err := CanonicalPSPFXRateSnapshot(
		new(big.Rat).SetFrac(big.NewInt(4), big.NewInt(15)),
	)
	if err != nil {
		t.Fatalf("CanonicalPSPFXRateSnapshot(inverse) error = %v", err)
	}
	if !projection.Decimal.Equal(decimal.RequireFromString("0.26666667")) ||
		!inverseNumerator.Decimal.Equal(decimal.NewFromInt(4)) ||
		!inverseDenominator.Decimal.Equal(decimal.NewFromInt(15)) {
		t.Fatalf("inverse snapshot = %s, %s/%s, want 0.26666667, 4/15",
			projection.Decimal, inverseNumerator.Decimal, inverseDenominator.Decimal)
	}

	inverse := validPSPFXAmount()
	inverse.FxRate = decimal.NullDecimal{Decimal: decimal.RequireFromString("0.26666667"), Valid: true}
	inverse.FxRateNumerator = decimal.NullDecimal{Decimal: decimal.NewFromInt(4), Valid: true}
	inverse.FxRateDenominator = decimal.NullDecimal{Decimal: decimal.NewFromInt(15), Valid: true}
	if _, err := validatePSPTransactionAmount(inverse); err != nil {
		t.Fatalf("exact inverse fraction with rounded projection: %v", err)
	}

	tie := projectPSPFXRate(big.NewInt(1), big.NewInt(200_000_000))
	if !tie.Equal(decimal.RequireFromString("0.00000001")) {
		t.Fatalf("half-away tie projection = %s, want 0.00000001", tie)
	}

	adversarialNumerator, _ := new(big.Int).SetString("12345678499999999999999999999999999999", 10)
	adversarialDenominator, _ := new(big.Int).SetString("99999999999999999999999999999999999999", 10)
	if got := projectPSPFXRate(adversarialNumerator, adversarialDenominator); !got.Equal(decimal.RequireFromString("0.12345678")) {
		t.Fatalf("near-tie exact projection = %s, want 0.12345678", got)
	}
}
