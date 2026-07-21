package groosh_test

import (
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/adonese/noebs/groosh"
)

func TestConvertUsesMajorUnitRateAndAdjustsTargetScale(t *testing.T) {
	usd := testUnit(t, "USD", 1, 2)
	bhd := testUnit(t, "BHD", 1, 3)
	amount := mustMoney(t, 100, usd) // USD 1.00
	rate := big.NewRat(3, 2)         // BHD 1.5 per USD 1

	got, err := groosh.Convert(amount, bhd, rate, groosh.RoundHalfEven)
	if err != nil || got.MinorUnits() != 1500 {
		t.Fatalf("Convert = %d, %v; want 1500", got.MinorUnits(), err)
	}
	if got.Unit() != bhd {
		t.Fatal("conversion did not attach the target snapshot")
	}
	if rate.Cmp(big.NewRat(3, 2)) != 0 {
		t.Fatalf("Convert mutated rate: %s", rate)
	}

	methodGot, err := amount.ConvertTo(bhd, rate, groosh.RoundHalfEven)
	if err != nil || methodGot != got {
		t.Fatalf("ConvertTo = %v, %v; want %v", methodGot, err, got)
	}
}

func TestConvertUsesISOScalesRatherThanDisplayScales(t *testing.T) {
	base, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        1,
		Code:             "USD",
		ISOMinorExponent: exponent(2),
		DisplayExponent:  exponent(0),
		CashExponent:     exponent(2),
		CashIncrement:    1,
		EffectiveFrom:    testEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	quote, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        2,
		Code:             "JPY",
		ISOMinorExponent: exponent(0),
		DisplayExponent:  exponent(4),
		CashExponent:     exponent(0),
		CashIncrement:    1,
		EffectiveFrom:    testEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}

	// USD 1.00 at 150 JPY/USD is JPY 150. Display exponents 0 and 4 are
	// deliberately misleading and must not enter ledger conversion math.
	got, err := groosh.Convert(mustMoney(t, 100, base), quote, big.NewRat(150, 1), groosh.RoundHalfEven)
	if err != nil || got.MinorUnits() != 150 {
		t.Fatalf("cross-scale conversion = %d, %v; want 150", got.MinorUnits(), err)
	}
}

func TestConvertRoundingModesForPositiveAndNegativeTies(t *testing.T) {
	base := testUnit(t, "AAA", 1, 0)
	quote := testUnit(t, "BBB", 1, 0)
	rate := big.NewRat(1, 2)

	tests := []struct {
		amount int64
		mode   groosh.RoundingMode
		want   int64
	}{
		{1, groosh.RoundHalfEven, 0},
		{3, groosh.RoundHalfEven, 2},
		{-1, groosh.RoundHalfEven, 0},
		{-3, groosh.RoundHalfEven, -2},
		{1, groosh.RoundHalfAwayFromZero, 1},
		{-1, groosh.RoundHalfAwayFromZero, -1},
		{1, groosh.RoundTowardZero, 0},
		{-1, groosh.RoundTowardZero, 0},
		{1, groosh.RoundFloor, 0},
		{-1, groosh.RoundFloor, -1},
		{1, groosh.RoundCeiling, 1},
		{-1, groosh.RoundCeiling, 0},
	}
	for _, tt := range tests {
		got, err := groosh.Convert(mustMoney(t, tt.amount, base), quote, rate, tt.mode)
		if err != nil || got.MinorUnits() != tt.want {
			t.Errorf("Convert(%d, %s) = %d, %v; want %d", tt.amount, tt.mode, got.MinorUnits(), err, tt.want)
		}
	}
}

func TestConvertRoundsNonTiesInBothDirections(t *testing.T) {
	base := testUnit(t, "AAA", 1, 0)
	quote := testUnit(t, "BBB", 1, 0)
	tests := []struct {
		rate *big.Rat
		mode groosh.RoundingMode
		want int64
	}{
		{big.NewRat(49, 100), groosh.RoundHalfEven, 0},
		{big.NewRat(51, 100), groosh.RoundHalfEven, 1},
		{big.NewRat(49, 100), groosh.RoundHalfAwayFromZero, 0},
		{big.NewRat(51, 100), groosh.RoundHalfAwayFromZero, 1},
	}
	for _, tt := range tests {
		got, err := groosh.Convert(mustMoney(t, 1, base), quote, tt.rate, tt.mode)
		if err != nil || got.MinorUnits() != tt.want {
			t.Errorf("rate %s mode %s = %d, %v; want %d", tt.rate, tt.mode, got.MinorUnits(), err, tt.want)
		}
	}
}

func TestConvertRejectsInvalidInputsAndOverflow(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	amount := mustMoney(t, 100, unit)
	for name, rate := range map[string]*big.Rat{
		"nil":      nil,
		"zero":     new(big.Rat),
		"negative": big.NewRat(-1, 2),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := groosh.Convert(amount, unit, rate, groosh.RoundHalfEven); !errors.Is(err, groosh.ErrInvalidRate) {
				t.Fatalf("error = %v, want ErrInvalidRate", err)
			}
		})
	}
	if _, err := groosh.Convert(amount, unit, big.NewRat(1, 1), 0); !errors.Is(err, groosh.ErrInvalidRoundingMode) {
		t.Fatalf("invalid mode error = %v", err)
	}
	if _, err := groosh.Convert(mustMoney(t, math.MaxInt64, unit), unit, big.NewRat(2, 1), groosh.RoundHalfEven); !errors.Is(err, groosh.ErrOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
	var invalid groosh.CurrencyUnit
	if _, err := groosh.Convert(amount, invalid, big.NewRat(1, 1), groosh.RoundHalfEven); !errors.Is(err, groosh.ErrInvalidMoney) {
		t.Fatalf("invalid target error = %v", err)
	}
	missingISO, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:       99,
		Code:            "XTS",
		DisplayExponent: exponent(2),
		CashExponent:    exponent(2),
		CashIncrement:   1,
		EffectiveFrom:   testEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := groosh.Convert(amount, missingISO, big.NewRat(1, 1), groosh.RoundHalfEven); !errors.Is(err, groosh.ErrInvalidMoney) || !errors.Is(err, groosh.ErrMissingISOMinorExponent) {
		t.Fatalf("missing target ISO exponent error = %v", err)
	}
}

func TestParseRateIsStrictPositiveAndExact(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  *big.Rat
	}{
		{"1", big.NewRat(1, 1)},
		{"1.5", big.NewRat(3, 2)},
		{"0.00100", big.NewRat(1, 1000)},
		{"0002.50", big.NewRat(5, 2)},
	} {
		got, err := groosh.ParseRate(tt.input)
		if err != nil || got.Cmp(tt.want) != 0 {
			t.Errorf("ParseRate(%q) = %v, %v; want %v", tt.input, got, err, tt.want)
		}
	}
	for _, input := range []string{"", "0", "0.0", "-1", "+1", ".1", "1.", "1/2", "1e2", " 1", "1 ", "1,2", "NaN"} {
		if _, err := groosh.ParseRate(input); !errors.Is(err, groosh.ErrInvalidRate) {
			t.Errorf("ParseRate(%q) error = %v", input, err)
		}
	}
}

func TestRoundMinorUnitsRequiresExplicitPolicyAndPreservesInput(t *testing.T) {
	value := big.NewRat(5, 2)
	for _, test := range []struct {
		name string
		mode groosh.RoundingMode
		want int64
	}{
		{name: "half even", mode: groosh.RoundHalfEven, want: 2},
		{name: "half away", mode: groosh.RoundHalfAwayFromZero, want: 3},
		{name: "toward zero", mode: groosh.RoundTowardZero, want: 2},
		{name: "floor", mode: groosh.RoundFloor, want: 2},
		{name: "ceiling", mode: groosh.RoundCeiling, want: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := groosh.RoundMinorUnits(value, test.mode)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("RoundMinorUnits() = %d, want %d", got, test.want)
			}
		})
	}
	if value.Cmp(big.NewRat(5, 2)) != 0 {
		t.Fatalf("RoundMinorUnits mutated input: %s", value)
	}
	if _, err := groosh.RoundMinorUnits(value, 0); !errors.Is(err, groosh.ErrInvalidRoundingMode) {
		t.Fatalf("missing rounding policy error = %v", err)
	}
	if _, err := groosh.RoundMinorUnits(nil, groosh.RoundHalfEven); !errors.Is(err, groosh.ErrInvalidAmount) {
		t.Fatalf("nil value error = %v", err)
	}
	overflow := new(big.Rat).SetInt(new(big.Int).Add(big.NewInt(math.MaxInt64), big.NewInt(1)))
	if _, err := groosh.RoundMinorUnits(overflow, groosh.RoundHalfEven); !errors.Is(err, groosh.ErrOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestRoundingModeNamesAndZeroInvalidity(t *testing.T) {
	for mode, want := range map[groosh.RoundingMode]string{
		groosh.RoundHalfEven:         "half_even",
		groosh.RoundHalfAwayFromZero: "half_away_from_zero",
		groosh.RoundTowardZero:       "toward_zero",
		groosh.RoundFloor:            "floor",
		groosh.RoundCeiling:          "ceiling",
		groosh.RoundingMode(0):       "invalid",
		groosh.RoundingMode(255):     "invalid",
	} {
		if got := mode.String(); got != want {
			t.Errorf("RoundingMode(%d).String() = %q, want %q", mode, got, want)
		}
	}
	for _, mode := range []groosh.RoundingMode{
		groosh.RoundHalfEven,
		groosh.RoundHalfAwayFromZero,
		groosh.RoundTowardZero,
		groosh.RoundFloor,
		groosh.RoundCeiling,
	} {
		parsed, err := groosh.ParseRoundingMode(mode.String())
		if err != nil || parsed != mode {
			t.Errorf("ParseRoundingMode(%q) = %v, %v", mode.String(), parsed, err)
		}
	}
	for _, value := range []string{"", "HALF_EVEN", "half-even", "unknown"} {
		if _, err := groosh.ParseRoundingMode(value); !errors.Is(err, groosh.ErrInvalidRoundingMode) {
			t.Errorf("ParseRoundingMode(%q) error = %v", value, err)
		}
	}
}
