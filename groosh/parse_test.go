package groosh_test

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/adonese/noebs/groosh"
)

func TestParseMajorAcceptsOnlyExactRepresentableDecimals(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	tests := []struct {
		input string
		want  int64
	}{
		{"0", 0},
		{"-0", 0},
		{"+0", 0},
		{"1", 100},
		{"+1.2", 120},
		{"-1.2", -120},
		{"001.02", 102},
		{"0.01", 1},
		{"1.23", 123},
		{"1.230", 123},
		{"1.230000000000000000000000000000", 123},
		{"92233720368547758.07", math.MaxInt64},
		{"-92233720368547758.08", math.MinInt64},
		{strings.Repeat("0", 1_000) + "1.00", 100},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := groosh.ParseMajor(tt.input, unit)
			if err != nil || got.MinorUnits() != tt.want {
				t.Fatalf("ParseMajor(%q) = %d, %v; want %d", tt.input, got.MinorUnits(), err, tt.want)
			}
		})
	}
}

func TestParseMajorRejectsNonCanonicalSyntax(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	inputs := []string{
		"", "+", "-", ".", ".1", "1.", "1..0", "1.2.3",
		" 1", "1 ", "1 2", "1\t", "1\n", "1,000", "1_000",
		"USD 1.00", "$1.00", "1e2", "1E2", "NaN", "Inf", "١.٢", "１.２",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			_, err := groosh.ParseMajor(input, unit)
			if !errors.Is(err, groosh.ErrInvalidAmountSyntax) || !errors.Is(err, groosh.ErrInvalidAmount) {
				t.Fatalf("ParseMajor(%q) error = %v", input, err)
			}
		})
	}
}

func TestParseMajorDistinguishesInexactAndOverflow(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	for _, input := range []string{"1.001", "1.2301", "-0.0000001"} {
		if _, err := groosh.ParseMajor(input, unit); !errors.Is(err, groosh.ErrInexactAmount) ||
			!errors.Is(err, groosh.ErrInvalidAmount) {
			t.Errorf("ParseMajor(%q) error = %v, want inexact", input, err)
		}
	}
	for _, input := range []string{
		"92233720368547758.08",
		"-92233720368547758.09",
		"99999999999999999999999999999999999999",
	} {
		if _, err := groosh.ParseMajor(input, unit); !errors.Is(err, groosh.ErrOverflow) {
			t.Errorf("ParseMajor(%q) error = %v, want overflow", input, err)
		}
	}
}

func TestParseMajorWithZeroAndLargeExponents(t *testing.T) {
	jpy := testUnit(t, "JPY", 1, 0)
	for _, tt := range []struct {
		input string
		want  int64
	}{{"12", 12}, {"12.0", 12}, {"12.00000", 12}} {
		got, err := groosh.ParseMajor(tt.input, jpy)
		if err != nil || got.MinorUnits() != tt.want {
			t.Errorf("ParseMajor(%q, exponent 0) = %d, %v", tt.input, got.MinorUnits(), err)
		}
	}
	if _, err := groosh.ParseMajor("12.1", jpy); !errors.Is(err, groosh.ErrInexactAmount) {
		t.Fatalf("exponent-zero fraction error = %v", err)
	}

	large := testUnit(t, "XTS", 1, 255)
	input := "0." + strings.Repeat("0", 254) + "1"
	got, err := groosh.ParseMajor(input, large)
	if err != nil || got.MinorUnits() != 1 {
		t.Fatalf("large exponent parse = %d, %v", got.MinorUnits(), err)
	}
	formatted, err := got.MajorString()
	if err != nil || formatted != input {
		t.Fatalf("large exponent format length=%d, error=%v", len(formatted), err)
	}
}

func TestCanonicalAndDisplayAlwaysIncludeCurrencyCode(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	tests := []struct {
		minor int64
		want  string // display form
	}{
		{0, "USD 0.00"},
		{1, "USD 0.01"},
		{-1, "USD -0.01"},
		{100, "USD 1.00"},
		{math.MaxInt64, "USD 92233720368547758.07"},
		{math.MinInt64, "USD -92233720368547758.08"},
	}
	for _, tt := range tests {
		money := mustMoney(t, tt.minor, unit)
		wantCanonical := "USD@1" + strings.TrimPrefix(tt.want, "USD")
		canonical, err := money.CanonicalString()
		if err != nil || canonical != wantCanonical {
			t.Errorf("CanonicalString(%d) = %q, %v; want %q", tt.minor, canonical, err, wantCanonical)
		}
		display, err := money.Display()
		if err != nil || display != tt.want || money.String() != wantCanonical {
			t.Errorf("display/String mismatch: %q, %q, %v", display, money.String(), err)
		}
		text, err := money.MarshalText()
		if err != nil || string(text) != wantCanonical {
			t.Errorf("MarshalText = %q, %v", text, err)
		}
		minorText, err := money.MinorString()
		if err != nil || minorText != strconv.FormatInt(tt.minor, 10) {
			t.Errorf("MinorString = %q, %v", minorText, err)
		}
		roundTrip, err := groosh.ParseCanonical(canonical, unit)
		if err != nil || roundTrip != money {
			t.Errorf("ParseCanonical(%q) = %v, %v", canonical, roundTrip, err)
		}
	}
}

func TestParseCanonicalRejectsMalformedOrWrongCurrency(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	for _, input := range []string{
		"", "USD", "USD@1", "USD@1  ", "USD@1  1.00", "usd@1 1.00", "US@1 1.00", "USDX@1 1.00",
		"USD@ 1.00", "USD@0 1.00", "USD@01 1.00", "USD@+1 1.00",
		"USD@1 +1.00", "USD@1 01.00", "USD@1 1", "USD@1 1.000", "USD@1 -0.00",
	} {
		_, err := groosh.ParseCanonical(input, unit)
		if err == nil || (!errors.Is(err, groosh.ErrInvalidCanonicalMoney) &&
			!errors.Is(err, groosh.ErrInvalidAmountSyntax)) {
			t.Errorf("ParseCanonical(%q) error = %v", input, err)
		}
	}
	if _, err := groosh.ParseCanonical("EUR@1 1.00", unit); !errors.Is(err, groosh.ErrCurrencyMismatch) {
		t.Fatalf("wrong currency error = %v", err)
	}
	if _, err := groosh.ParseCanonical("USD@2 1.00", unit); !errors.Is(err, groosh.ErrUnitVersionMismatch) {
		t.Fatalf("wrong version error = %v", err)
	}
}

func TestDisplayUsesPresentationExponentWithoutChangingLedgerScale(t *testing.T) {
	coarseDisplay, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        1,
		Code:             "XTS",
		ISOMinorExponent: exponent(2),
		DisplayExponent:  exponent(0),
		CashExponent:     exponent(2),
		CashIncrement:    1,
		EffectiveFrom:    testEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := mustMoney(t, 100, coarseDisplay).Display(); err != nil || got != "XTS 1" {
		t.Fatalf("exact coarse display = %q, %v", got, err)
	}
	if _, err := mustMoney(t, 123, coarseDisplay).Display(); !errors.Is(err, groosh.ErrInexactAmount) {
		t.Fatalf("inexact coarse display error = %v", err)
	}
	if got, err := mustMoney(t, 150, coarseDisplay).DisplayRounded(groosh.RoundHalfEven); err != nil || got != "XTS 2" {
		t.Fatalf("rounded coarse display = %q, %v", got, err)
	}
	if _, err := mustMoney(t, 100, coarseDisplay).DisplayRounded(0); !errors.Is(err, groosh.ErrInvalidRoundingMode) {
		t.Fatalf("missing display rounding mode error = %v", err)
	}

	fineDisplay, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        2,
		Code:             "XTS",
		ISOMinorExponent: exponent(2),
		DisplayExponent:  exponent(4),
		CashExponent:     exponent(2),
		CashIncrement:    1,
		EffectiveFrom:    testEpoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := mustMoney(t, 123, fineDisplay).Display(); err != nil || got != "XTS 1.2300" {
		t.Fatalf("fine display = %q, %v", got, err)
	}
	if got, err := mustMoney(t, 123, fineDisplay).MajorString(); err != nil || got != "1.23" {
		t.Fatalf("operational major = %q, %v", got, err)
	}
}
