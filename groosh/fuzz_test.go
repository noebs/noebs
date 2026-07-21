package groosh_test

import (
	"errors"
	"math/big"
	"testing"

	"github.com/adonese/noebs/groosh"
)

func FuzzParseMajorRoundTrip(f *testing.F) {
	for _, seed := range []struct {
		input    string
		exponent uint8
	}{
		{"0", 0},
		{"1.23", 2},
		{"-92233720368547758.08", 2},
		{"1.23000", 2},
		{" 1.00", 2},
		{"1e2", 2},
	} {
		f.Add(seed.input, seed.exponent)
	}

	f.Fuzz(func(t *testing.T, input string, rawExponent uint8) {
		exponent := rawExponent
		unit := testUnit(t, "XTS", 1, exponent)
		money, err := groosh.ParseMajor(input, unit)
		if err != nil {
			return
		}
		canonical, err := money.CanonicalString()
		if err != nil {
			t.Fatalf("successful parse produced unformattable money: %v", err)
		}
		roundTrip, err := groosh.ParseCanonical(canonical, unit)
		if err != nil {
			t.Fatalf("ParseCanonical(%q): %v", canonical, err)
		}
		if roundTrip != money {
			t.Fatalf("round trip = %v, want %v", roundTrip, money)
		}
	})
}

func FuzzAddSubRoundTrip(f *testing.F) {
	f.Add(int64(0), int64(0))
	f.Add(int64(100), int64(-30))
	f.Add(int64(^uint64(0)>>1), int64(1))
	f.Add(-int64(^uint64(0)>>1)-1, int64(-1))

	f.Fuzz(func(t *testing.T, left, right int64) {
		unit := testUnit(t, "XTS", 1, 2)
		a := mustMoney(t, left, unit)
		b := mustMoney(t, right, unit)
		sum, err := a.Add(b)
		exact := new(big.Int).Add(big.NewInt(left), big.NewInt(right))
		if err != nil {
			if exact.IsInt64() || !errors.Is(err, groosh.ErrOverflow) {
				t.Fatalf("Add(%d, %d) returned false overflow: %v", left, right, err)
			}
			return
		}
		if !exact.IsInt64() || sum.MinorUnits() != exact.Int64() {
			t.Fatalf("Add(%d, %d) = %d, exact %s", left, right, sum.MinorUnits(), exact)
		}
		recovered, err := sum.Sub(b)
		if err != nil {
			t.Fatalf("non-overflowing add could not be reversed: %v", err)
		}
		if recovered != a {
			t.Fatalf("(%d + %d) - %d = %d", left, right, right, recovered.MinorUnits())
		}
	})
}

func FuzzIdentityConversion(f *testing.F) {
	f.Add(int64(0), uint8(0), uint8(groosh.RoundHalfEven))
	f.Add(int64(-12345), uint8(2), uint8(groosh.RoundFloor))
	f.Add(int64(^uint64(0)>>1), uint8(18), uint8(groosh.RoundCeiling))

	f.Fuzz(func(t *testing.T, minor int64, rawExponent, rawMode uint8) {
		exponent := rawExponent
		mode := groosh.RoundingMode(rawMode%5 + 1)
		unit := testUnit(t, "XTS", 1, exponent)
		amount := mustMoney(t, minor, unit)
		converted, err := groosh.Convert(amount, unit, big.NewRat(1, 1), mode)
		if err != nil {
			t.Fatalf("identity conversion: %v", err)
		}
		if converted != amount {
			t.Fatalf("identity conversion = %v, want %v", converted, amount)
		}
	})
}

func FuzzAllocationPreservesTotal(f *testing.F) {
	f.Add(int64(10), uint64(1), uint64(2), uint64(3))
	f.Add(int64(-10), uint64(1), uint64(1), uint64(1))
	f.Add(int64(0), uint64(0), uint64(0), uint64(0))

	f.Fuzz(func(t *testing.T, total int64, a, b, c uint64) {
		if a == 0 && b == 0 && c == 0 {
			return
		}
		unit := testUnit(t, "XTS", 1, 2)
		parts, err := mustMoney(t, total, unit).Allocate([]uint64{a, b, c})
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		sum := new(big.Int)
		for i := range parts {
			sum.Add(sum, big.NewInt(parts[i].MinorUnits()))
			if parts[i].Unit() != unit {
				t.Fatalf("part %d has a different unit", i)
			}
		}
		if sum.Cmp(big.NewInt(total)) != 0 {
			t.Fatalf("allocated sum = %s, want %d", sum, total)
		}
	})
}
