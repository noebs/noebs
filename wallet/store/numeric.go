package store

import (
	"strings"

	"github.com/shopspring/decimal"
)

// decimalFitsNumeric reports whether value can be stored exactly in a
// PostgreSQL NUMERIC(precision, scale) column. PostgreSQL rounds excess
// fractional digits, so callers must reject values that do not fit instead of
// allowing the database to mutate monetary policy silently.
func decimalFitsNumeric(value decimal.Decimal, precision, scale int) bool {
	if precision <= 0 || scale < 0 || scale > precision {
		return false
	}

	digits := strings.TrimPrefix(value.Coefficient().String(), "-")
	normalizedDigits := strings.TrimRight(digits, "0")
	if normalizedDigits == "" {
		return true
	}

	// Removing trailing coefficient zeroes raises the exponent without
	// changing the value. Use int64 so an int32 exponent plus a very large
	// coefficient cannot overflow while validating untrusted decimal input.
	normalizedExponent := int64(value.Exponent()) + int64(len(digits)-len(normalizedDigits))
	if normalizedExponent < -int64(scale) {
		return false
	}

	integerDigits := int64(len(normalizedDigits)) + normalizedExponent
	if integerDigits < 0 {
		integerDigits = 0
	}
	return integerDigits <= int64(precision-scale)
}
