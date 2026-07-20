package validation

import (
	"math"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

func TestDecimalToInt64Bounds(t *testing.T) {
	tests := []struct {
		name    string
		value   decimal.Decimal
		want    int64
		wantErr error
	}{
		{name: "minimum", value: decimal.NewFromInt(math.MinInt64), want: math.MinInt64},
		{name: "below minimum", value: decimal.NewFromInt(math.MinInt64).Sub(decimal.NewFromInt(1)), wantErr: walletstore.ErrAmountOverflow},
		{name: "maximum", value: decimal.NewFromInt(math.MaxInt64), want: math.MaxInt64},
		{name: "above maximum", value: decimal.NewFromInt(math.MaxInt64).Add(decimal.NewFromInt(1)), wantErr: walletstore.ErrAmountOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decimalToInt64(tt.value)
			if err != tt.wantErr {
				t.Fatalf("decimalToInt64() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("decimalToInt64() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCheckedAddInt64Bounds(t *testing.T) {
	tests := []struct {
		name    string
		left    int64
		right   int64
		want    int64
		wantErr error
	}{
		{name: "maximum", left: math.MaxInt64, want: math.MaxInt64},
		{name: "above maximum", left: math.MaxInt64, right: 1, wantErr: walletstore.ErrAmountOverflow},
		{name: "minimum", left: math.MinInt64, want: math.MinInt64},
		{name: "below minimum", left: math.MinInt64, right: -1, wantErr: walletstore.ErrAmountOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := checkedAddInt64(tt.left, tt.right)
			if err != tt.wantErr {
				t.Fatalf("checkedAddInt64() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("checkedAddInt64() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCheckedSubtractInt64Bounds(t *testing.T) {
	tests := []struct {
		name    string
		left    int64
		right   int64
		want    int64
		wantErr error
	}{
		{name: "maximum", left: math.MaxInt64, want: math.MaxInt64},
		{name: "above maximum", left: math.MaxInt64, right: -1, wantErr: walletstore.ErrAmountOverflow},
		{name: "minimum", left: math.MinInt64, want: math.MinInt64},
		{name: "below minimum", left: math.MinInt64, right: 1, wantErr: walletstore.ErrAmountOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := checkedSubtractInt64(tt.left, tt.right)
			if err != tt.wantErr {
				t.Fatalf("checkedSubtractInt64() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("checkedSubtractInt64() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestConvertPositiveAmountBounds(t *testing.T) {
	tests := []struct {
		name    string
		rate    decimal.Decimal
		want    int64
		wantErr error
	}{
		{name: "maximum", rate: decimal.NewFromInt(1), want: math.MaxInt64},
		{name: "overflow", rate: decimal.NewFromInt(2), wantErr: walletstore.ErrAmountOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convertPositiveAmount(math.MaxInt64, tt.rate)
			if err != tt.wantErr {
				t.Fatalf("convertPositiveAmount() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("convertPositiveAmount() = %d, want %d", got, tt.want)
			}
		})
	}
}
