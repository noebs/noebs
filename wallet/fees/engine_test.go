package fees

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

func TestCalculateFeeRejectsOverflow(t *testing.T) {
	tests := []struct {
		name    string
		config  walletstore.FeeConfig
		wantFee int64
		wantErr error
	}{
		{
			name: "maximum percentage fee",
			config: walletstore.FeeConfig{
				PercentageFee: decimal.NewFromInt(100),
			},
			wantFee: math.MaxInt64,
		},
		{
			name: "percentage conversion overflow",
			config: walletstore.FeeConfig{
				PercentageFee: decimal.RequireFromString("100.0001"),
			},
			wantErr: walletstore.ErrAmountOverflow,
		},
		{
			name: "flat fee addition overflow",
			config: walletstore.FeeConfig{
				PercentageFee: decimal.NewFromInt(100),
				FlatFee:       1,
			},
			wantErr: walletstore.ErrAmountOverflow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := calculateFee(&tt.config, math.MaxInt64)
			if err != tt.wantErr {
				t.Fatalf("calculateFee() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && result.TotalFee != tt.wantFee {
				t.Fatalf("calculateFee() total = %d, want %d", result.TotalFee, tt.wantFee)
			}
		})
	}
}

func TestCalculateFeeUsesExplicitHalfAwayFromZeroPolicy(t *testing.T) {
	for _, test := range []struct {
		name       string
		amount     int64
		percentage string
		want       int64
	}{
		{name: "positive midpoint rounds up", amount: 1, percentage: "50", want: 1},
		{name: "below midpoint rounds down", amount: 1, percentage: "49.9999", want: 0},
		{name: "above midpoint rounds up", amount: 1, percentage: "50.0001", want: 1},
		{name: "non-midpoint remains nearest", amount: 3, percentage: "50", want: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := calculateFee(&walletstore.FeeConfig{
				PercentageFee: decimal.RequireFromString(test.percentage),
			}, test.amount)
			if err != nil {
				t.Fatal(err)
			}
			if result.PercentageFee != test.want || result.TotalFee != test.want {
				t.Fatalf("calculateFee() = %+v, want percentage and total %d", result, test.want)
			}
		})
	}
}
