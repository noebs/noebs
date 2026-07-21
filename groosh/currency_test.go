package groosh_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/adonese/noebs/groosh"
)

var testEpoch = time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)

func exponent(value uint8) *uint8 { return &value }

func testUnit(t *testing.T, code string, version int64, operationalExponent uint8) groosh.CurrencyUnit {
	t.Helper()
	iso := operationalExponent
	unit, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        version,
		Code:             code,
		ISOMinorExponent: &iso,
		DisplayExponent:  exponent(operationalExponent),
		CashExponent:     exponent(operationalExponent),
		CashIncrement:    1,
		EffectiveFrom:    testEpoch,
	})
	if err != nil {
		t.Fatalf("NewCurrencyUnit: %v", err)
	}
	return unit
}

func TestCurrencyUnitCopiesAndExposesSnapshot(t *testing.T) {
	iso := uint8(0)
	until := testEpoch.Add(24 * time.Hour)
	untilInput := until.In(time.FixedZone("offset", 4*60*60))
	unit, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        7,
		Code:             "SDG",
		ISOMinorExponent: &iso,
		DisplayExponent:  exponent(2),
		CashExponent:     exponent(0),
		CashIncrement:    5,
		EffectiveFrom:    testEpoch.In(time.FixedZone("offset", 4*60*60)),
		EffectiveUntil:   &untilInput,
	})
	if err != nil {
		t.Fatalf("NewCurrencyUnit: %v", err)
	}

	iso = 3
	untilInput = untilInput.Add(30 * 24 * time.Hour)
	gotISO, present := unit.ISOMinorExponent()
	if !present || gotISO != 0 {
		t.Fatalf("ISO exponent = (%d, %t), want (0, true)", gotISO, present)
	}
	if unit.VersionID() != 7 || unit.Code() != "SDG" {
		t.Fatalf("identity = %s@%d", unit.Code(), unit.VersionID())
	}
	minorExponent, hasMinorExponent := unit.MinorExponent()
	if unit.DisplayExponent() != 2 || !hasMinorExponent || minorExponent != 0 ||
		unit.CashExponent() != 0 || unit.CashIncrement() != 5 {
		t.Fatalf("scales were not preserved")
	}
	if !unit.EffectiveFrom().Equal(testEpoch) || unit.EffectiveFrom().Location() != time.UTC {
		t.Fatalf("effective_from = %v, want canonical UTC", unit.EffectiveFrom())
	}
	gotUntil, ok := unit.EffectiveUntil()
	if !ok || !gotUntil.Equal(until) || gotUntil.Location() != time.UTC {
		t.Fatalf("effective_until = (%v, %t), want (%v, true)", gotUntil, ok, until)
	}
	quantum, err := unit.CashQuantumMinorUnits()
	if err != nil || quantum != 5 {
		t.Fatalf("cash quantum = %d, %v; want 5", quantum, err)
	}
}

func TestCurrencyUnitAbsentISOExponentIsDistinctFromZero(t *testing.T) {
	withoutISO, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:       1,
		Code:            "XXX",
		DisplayExponent: exponent(0),
		CashExponent:    exponent(0),
		CashIncrement:   1,
		EffectiveFrom:   testEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if value, present := withoutISO.ISOMinorExponent(); present || value != 0 {
		t.Fatalf("absent ISO exponent = (%d, %t)", value, present)
	}
	if err := withoutISO.Validate(); err != nil {
		t.Fatalf("catalog snapshot without ISO metadata should remain valid: %v", err)
	}
	if _, err := withoutISO.CashQuantumMinorUnits(); !errors.Is(err, groosh.ErrMissingISOMinorExponent) {
		t.Fatalf("cash quantum without ISO exponent error = %v", err)
	}
	if _, err := groosh.NewMoney(0, withoutISO); !errors.Is(err, groosh.ErrInvalidMoney) ||
		!errors.Is(err, groosh.ErrMissingISOMinorExponent) {
		t.Fatalf("Money without ISO exponent error = %v", err)
	}
	if _, err := groosh.ParseMajor("0", withoutISO); !errors.Is(err, groosh.ErrInvalidMoney) ||
		!errors.Is(err, groosh.ErrMissingISOMinorExponent) {
		t.Fatalf("ParseMajor without ISO exponent error = %v", err)
	}

	zero := uint8(0)
	withZero, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        2,
		Code:             "XXX",
		ISOMinorExponent: &zero,
		DisplayExponent:  exponent(0),
		CashExponent:     exponent(0),
		CashIncrement:    1,
		EffectiveFrom:    testEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if value, present := withZero.ISOMinorExponent(); !present || value != 0 {
		t.Fatalf("present zero ISO exponent = (%d, %t)", value, present)
	}
}

func TestCurrencyUnitCashQuantumScaleAdjustment(t *testing.T) {
	unit, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        1,
		Code:             "CHF",
		ISOMinorExponent: exponent(4),
		DisplayExponent:  exponent(2),
		CashExponent:     exponent(2),
		CashIncrement:    5,
		EffectiveFrom:    testEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	quantum, err := unit.CashQuantumMinorUnits()
	if err != nil || quantum != 500 {
		t.Fatalf("quantum = %d, %v; want 500", quantum, err)
	}
}

func TestCurrencyUnitRejectsInvalidSnapshots(t *testing.T) {
	valid := groosh.CurrencyUnitSpec{
		VersionID:        1,
		Code:             "USD",
		ISOMinorExponent: exponent(2),
		DisplayExponent:  exponent(2),
		CashExponent:     exponent(2),
		CashIncrement:    1,
		EffectiveFrom:    testEpoch,
	}
	zeroTime := time.Time{}
	equalTime := testEpoch
	beforeTime := testEpoch.Add(-time.Nanosecond)

	tests := []struct {
		name string
		edit func(*groosh.CurrencyUnitSpec)
		want error
	}{
		{"zero version", func(s *groosh.CurrencyUnitSpec) { s.VersionID = 0 }, groosh.ErrMissingVersionID},
		{"negative version", func(s *groosh.CurrencyUnitSpec) { s.VersionID = -1 }, groosh.ErrMissingVersionID},
		{"missing display exponent", func(s *groosh.CurrencyUnitSpec) { s.DisplayExponent = nil }, groosh.ErrMissingDisplayExponent},
		{"missing cash exponent", func(s *groosh.CurrencyUnitSpec) { s.CashExponent = nil }, groosh.ErrMissingCashExponent},
		{"short code", func(s *groosh.CurrencyUnitSpec) { s.Code = "US" }, groosh.ErrInvalidCurrencyCode},
		{"long code", func(s *groosh.CurrencyUnitSpec) { s.Code = "USDX" }, groosh.ErrInvalidCurrencyCode},
		{"lowercase code", func(s *groosh.CurrencyUnitSpec) { s.Code = "usd" }, groosh.ErrInvalidCurrencyCode},
		{"non ASCII code", func(s *groosh.CurrencyUnitSpec) { s.Code = "UŠD" }, groosh.ErrInvalidCurrencyCode},
		{"zero cash increment", func(s *groosh.CurrencyUnitSpec) { s.CashIncrement = 0 }, groosh.ErrInvalidCashRule},
		{"negative cash increment", func(s *groosh.CurrencyUnitSpec) { s.CashIncrement = -1 }, groosh.ErrInvalidCashRule},
		{"subminor cash quantum", func(s *groosh.CurrencyUnitSpec) { s.CashExponent = exponent(3) }, groosh.ErrInvalidCashRule},
		{"cash quantum overflow", func(s *groosh.CurrencyUnitSpec) {
			s.ISOMinorExponent = exponent(19)
			s.CashExponent = exponent(0)
		}, groosh.ErrInvalidCashRule},
		{"zero effective from", func(s *groosh.CurrencyUnitSpec) { s.EffectiveFrom = time.Time{} }, groosh.ErrInvalidEffectiveInterval},
		{"present zero effective until", func(s *groosh.CurrencyUnitSpec) { s.EffectiveUntil = &zeroTime }, groosh.ErrInvalidEffectiveInterval},
		{"equal effective until", func(s *groosh.CurrencyUnitSpec) { s.EffectiveUntil = &equalTime }, groosh.ErrInvalidEffectiveInterval},
		{"earlier effective until", func(s *groosh.CurrencyUnitSpec) { s.EffectiveUntil = &beforeTime }, groosh.ErrInvalidEffectiveInterval},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := valid
			tt.edit(&spec)
			_, err := groosh.NewCurrencyUnit(spec)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if !errors.Is(err, groosh.ErrInvalidCurrencyUnit) {
				t.Fatalf("error = %v, want ErrInvalidCurrencyUnit category", err)
			}
			var typed *groosh.OpError
			if !errors.As(err, &typed) || typed.Field == "" || typed.Op == "" {
				t.Fatalf("error lacks typed context: %#v", err)
			}
		})
	}
}

func TestCurrencyUnitEffectiveIntervalIsHalfOpen(t *testing.T) {
	until := testEpoch.Add(time.Hour)
	unit, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        1,
		Code:             "USD",
		ISOMinorExponent: exponent(2),
		DisplayExponent:  exponent(2),
		CashExponent:     exponent(2),
		CashIncrement:    1,
		EffectiveFrom:    testEpoch,
		EffectiveUntil:   &until,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		at   time.Time
		want bool
	}{
		{testEpoch.Add(-time.Nanosecond), false},
		{testEpoch, true},
		{testEpoch.Add(30 * time.Minute), true},
		{until, false},
		{until.Add(time.Nanosecond), false},
	}
	for _, tt := range tests {
		got, err := unit.IsEffectiveAt(tt.at)
		if err != nil || got != tt.want {
			t.Errorf("IsEffectiveAt(%v) = %t, %v; want %t", tt.at, got, err, tt.want)
		}
	}
	if _, err := unit.IsEffectiveAt(time.Time{}); !errors.Is(err, groosh.ErrInvalidEffectiveInterval) {
		t.Fatalf("zero time error = %v", err)
	}
}

func TestZeroCurrencyUnitAndMoneyAreInvalid(t *testing.T) {
	var unit groosh.CurrencyUnit
	if !errors.Is(unit.Validate(), groosh.ErrInvalidCurrencyUnit) {
		t.Fatalf("zero unit should be invalid: %v", unit.Validate())
	}
	if _, err := unit.CashQuantumMinorUnits(); !errors.Is(err, groosh.ErrInvalidCurrencyUnit) {
		t.Fatalf("zero unit cash quantum error = %v", err)
	}

	var money groosh.Money
	if !errors.Is(money.Validate(), groosh.ErrInvalidMoney) {
		t.Fatalf("zero money should be invalid: %v", money.Validate())
	}
	if money.String() != "<invalid money>" {
		t.Fatalf("zero money String() = %q", money.String())
	}
	if _, err := groosh.NewMoney(math.MaxInt64, unit); !errors.Is(err, groosh.ErrInvalidMoney) {
		t.Fatalf("NewMoney with zero unit error = %v", err)
	} else if !errors.Is(err, groosh.ErrInvalidCurrencyUnit) {
		t.Fatalf("NewMoney discarded typed unit cause: %v", err)
	}
}
