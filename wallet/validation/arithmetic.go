package validation

import (
	"math"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

var (
	minInt64Decimal = decimal.NewFromInt(math.MinInt64)
	maxInt64Decimal = decimal.NewFromInt(math.MaxInt64)
)

func decimalToInt64(value decimal.Decimal) (int64, error) {
	if value.LessThan(minInt64Decimal) || value.GreaterThan(maxInt64Decimal) {
		return 0, walletstore.ErrAmountOverflow
	}
	return value.IntPart(), nil
}

func checkedAddInt64(left, right int64) (int64, error) {
	if (right > 0 && left > math.MaxInt64-right) || (right < 0 && left < math.MinInt64-right) {
		return 0, walletstore.ErrAmountOverflow
	}
	return left + right, nil
}

func checkedSubtractInt64(left, right int64) (int64, error) {
	if (right > 0 && left < math.MinInt64+right) || (right < 0 && left > math.MaxInt64+right) {
		return 0, walletstore.ErrAmountOverflow
	}
	return left - right, nil
}
