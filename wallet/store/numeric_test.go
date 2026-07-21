package store

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestDecimalFitsNumericExactly(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		precision int
		scale     int
		want      bool
	}{
		{name: "zero", value: "0", precision: 8, scale: 4, want: true},
		{name: "fee upper boundary", value: "9999.9999", precision: 8, scale: 4, want: true},
		{name: "fee lower boundary", value: "-9999.9999", precision: 8, scale: 4, want: true},
		{name: "fee fractional overflow", value: "0.00001", precision: 8, scale: 4},
		{name: "fee integer overflow", value: "10000", precision: 8, scale: 4},
		{name: "legacy rate upper boundary", value: "9999999999.99999999", precision: 18, scale: 8, want: true},
		{name: "legacy rate fractional overflow", value: "1.000000001", precision: 18, scale: 8},
		{name: "legacy rate integer overflow", value: "10000000000", precision: 18, scale: 8},
		{name: "trailing zero normalization remains exact", value: "1.230000000", precision: 18, scale: 8, want: true},
		{name: "integer-only numeric", value: "999", precision: 3, scale: 0, want: true},
		{name: "integer-only fractional overflow", value: "1.1", precision: 3, scale: 0},
		{name: "invalid precision", value: "1", precision: 0, scale: 0},
		{name: "invalid negative scale", value: "1", precision: 8, scale: -1},
		{name: "scale exceeds precision", value: "1", precision: 8, scale: 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decimalFitsNumeric(decimal.RequireFromString(tt.value), tt.precision, tt.scale)
			if got != tt.want {
				t.Fatalf("decimalFitsNumeric(%s, %d, %d) = %t, want %t", tt.value, tt.precision, tt.scale, got, tt.want)
			}
		})
	}

	if decimalFitsNumeric(decimal.New(1, 1_000_000), 18, 8) {
		t.Fatal("huge positive exponent unexpectedly fits NUMERIC(18,8)")
	}
	if decimalFitsNumeric(decimal.New(1, -1_000_000), 18, 8) {
		t.Fatal("huge negative exponent unexpectedly fits NUMERIC(18,8)")
	}
	if !decimalFitsNumeric(decimal.New(0, -1_000_000), 18, 8) {
		t.Fatal("zero with huge negative exponent must remain exactly representable")
	}
}
