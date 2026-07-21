package groosh_test

import (
	"errors"
	"math"
	"testing"

	"github.com/adonese/noebs/groosh"
)

func mustMoney(t *testing.T, minor int64, unit groosh.CurrencyUnit) groosh.Money {
	t.Helper()
	money, err := groosh.NewMoney(minor, unit)
	if err != nil {
		t.Fatalf("NewMoney: %v", err)
	}
	return money
}

func TestMoneyConstructionAndAccessors(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	money, err := groosh.New(0, unit)
	if err != nil {
		t.Fatal(err)
	}
	if money.MinorUnits() != 0 || !money.IsZero() || money.Sign() != 0 {
		t.Fatalf("zero money accessors are inconsistent")
	}
	if money.Unit() != unit || money.CurrencyCode() != "USD" || money.UnitVersionID() != 1 {
		t.Fatalf("money identity accessors are inconsistent")
	}
	if got := mustMoney(t, -1, unit).Sign(); got != -1 {
		t.Fatalf("negative sign = %d", got)
	}
	if got := mustMoney(t, 1, unit).Sign(); got != 1 {
		t.Fatalf("positive sign = %d", got)
	}
}

func TestMoneyExactArithmeticAndComparison(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	a := mustMoney(t, 125, unit)
	b := mustMoney(t, -25, unit)

	sum, err := a.Add(b)
	if err != nil || sum.MinorUnits() != 100 {
		t.Fatalf("Add = %d, %v; want 100", sum.MinorUnits(), err)
	}
	difference, err := a.Sub(b)
	if err != nil || difference.MinorUnits() != 150 {
		t.Fatalf("Sub = %d, %v; want 150", difference.MinorUnits(), err)
	}
	negative, err := a.Neg()
	if err != nil || negative.MinorUnits() != -125 {
		t.Fatalf("Neg = %d, %v; want -125", negative.MinorUnits(), err)
	}
	absolute, err := b.Abs()
	if err != nil || absolute.MinorUnits() != 25 {
		t.Fatalf("Abs = %d, %v; want 25", absolute.MinorUnits(), err)
	}
	if a.MinorUnits() != 125 || b.MinorUnits() != -25 {
		t.Fatal("operations mutated an operand")
	}

	for _, tt := range []struct {
		left, right int64
		want        int
	}{{1, 2, -1}, {2, 2, 0}, {3, 2, 1}} {
		got, err := mustMoney(t, tt.left, unit).Cmp(mustMoney(t, tt.right, unit))
		if err != nil || got != tt.want {
			t.Errorf("Cmp(%d, %d) = %d, %v; want %d", tt.left, tt.right, got, err, tt.want)
		}
	}
}

func TestMoneyArithmeticDetectsEveryInt64Boundary(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	tests := []struct {
		name string
		run  func() error
	}{
		{"max plus one", func() error { _, err := mustMoney(t, math.MaxInt64, unit).Add(mustMoney(t, 1, unit)); return err }},
		{"min plus negative one", func() error { _, err := mustMoney(t, math.MinInt64, unit).Add(mustMoney(t, -1, unit)); return err }},
		{"max minus negative one", func() error { _, err := mustMoney(t, math.MaxInt64, unit).Sub(mustMoney(t, -1, unit)); return err }},
		{"min minus one", func() error { _, err := mustMoney(t, math.MinInt64, unit).Sub(mustMoney(t, 1, unit)); return err }},
		{"neg min", func() error { _, err := mustMoney(t, math.MinInt64, unit).Neg(); return err }},
		{"abs min", func() error { _, err := mustMoney(t, math.MinInt64, unit).Abs(); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, groosh.ErrOverflow) {
				t.Fatalf("error = %v, want ErrOverflow", err)
			}
		})
	}

	if got, err := mustMoney(t, math.MaxInt64, unit).Add(mustMoney(t, 0, unit)); err != nil || got.MinorUnits() != math.MaxInt64 {
		t.Fatalf("max + 0 = %d, %v", got.MinorUnits(), err)
	}
	if got, err := mustMoney(t, math.MinInt64, unit).Sub(mustMoney(t, 0, unit)); err != nil || got.MinorUnits() != math.MinInt64 {
		t.Fatalf("min - 0 = %d, %v", got.MinorUnits(), err)
	}
}

func TestMoneyRejectsCurrencyVersionAndDefinitionMismatches(t *testing.T) {
	usdV1 := testUnit(t, "USD", 1, 2)
	eurV1 := testUnit(t, "EUR", 1, 2)
	usdV2 := testUnit(t, "USD", 2, 2)
	usdV1DifferentDefinition := testUnit(t, "USD", 1, 3)

	tests := []struct {
		name  string
		other groosh.CurrencyUnit
		want  error
	}{
		{"currency", eurV1, groosh.ErrCurrencyMismatch},
		{"version", usdV2, groosh.ErrUnitVersionMismatch},
		{"definition", usdV1DifferentDefinition, groosh.ErrUnitDefinitionMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := mustMoney(t, 1, usdV1)
			right := mustMoney(t, 1, tt.other)
			for name, run := range map[string]func() error{
				"add": func() error { _, err := left.Add(right); return err },
				"sub": func() error { _, err := left.Sub(right); return err },
				"cmp": func() error { _, err := left.Cmp(right); return err },
			} {
				if err := run(); !errors.Is(err, tt.want) {
					t.Errorf("%s error = %v, want %v", name, err, tt.want)
				}
			}
		})
	}
}

func TestCashQuantizationUsesExplicitRounding(t *testing.T) {
	unit, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        1,
		Code:             "XCH",
		ISOMinorExponent: exponent(2),
		DisplayExponent:  exponent(2),
		CashExponent:     exponent(2),
		CashIncrement:    2,
		EffectiveFrom:    testEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		minor int64
		mode  groosh.RoundingMode
		want  int64
	}{
		{1, groosh.RoundHalfEven, 0},
		{3, groosh.RoundHalfEven, 4},
		{1, groosh.RoundHalfAwayFromZero, 2},
		{-1, groosh.RoundHalfAwayFromZero, -2},
		{-1, groosh.RoundTowardZero, 0},
		{-1, groosh.RoundFloor, -2},
		{-1, groosh.RoundCeiling, 0},
		{1, groosh.RoundFloor, 0},
		{1, groosh.RoundCeiling, 2},
	}
	for _, tt := range tests {
		got, err := mustMoney(t, tt.minor, unit).QuantizeCash(tt.mode)
		if err != nil || got.MinorUnits() != tt.want {
			t.Errorf("QuantizeCash(%d, %s) = %d, %v; want %d", tt.minor, tt.mode, got.MinorUnits(), err, tt.want)
		}
	}
	if _, err := mustMoney(t, 1, unit).QuantizeCash(0); !errors.Is(err, groosh.ErrInvalidRoundingMode) {
		t.Fatalf("zero rounding mode error = %v", err)
	}
	if ok, err := mustMoney(t, 4, unit).IsCashQuantized(); err != nil || !ok {
		t.Fatalf("4 should be cash quantized: %t, %v", ok, err)
	}
	if ok, err := mustMoney(t, 3, unit).IsCashQuantized(); err != nil || ok {
		t.Fatalf("3 should not be cash quantized: %t, %v", ok, err)
	}
}

func TestCashQuantizationDetectsOutwardOverflow(t *testing.T) {
	unit, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        1,
		Code:             "XCH",
		ISOMinorExponent: exponent(0),
		DisplayExponent:  exponent(0),
		CashExponent:     exponent(0),
		CashIncrement:    10,
		EffectiveFrom:    testEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	max := mustMoney(t, math.MaxInt64, unit)
	if _, err := max.QuantizeCash(groosh.RoundCeiling); !errors.Is(err, groosh.ErrOverflow) {
		t.Fatalf("ceiling max error = %v", err)
	}
	got, err := max.QuantizeCash(groosh.RoundTowardZero)
	if err != nil || got.MinorUnits() != math.MaxInt64-7 {
		t.Fatalf("toward-zero max = %d, %v", got.MinorUnits(), err)
	}
}
