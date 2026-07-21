package groosh

import (
	"math/big"
)

// Convert converts amount using an exact quote-major-per-base-major rate.
// For example, a rate of 3/2 means one base major unit buys 1.5 target major
// units. The target's ISO minor exponent is applied before the explicit
// rounding mode, and the result is checked against the int64 minor-unit domain.
//
// Convert copies rate before use and never mutates caller-owned big numbers.
func Convert(amount Money, target CurrencyUnit, rate *big.Rat, mode RoundingMode) (Money, error) {
	const op = "Convert"
	if err := amount.Validate(); err != nil {
		return Money{}, err
	}
	if _, err := target.operationalExponent(op); err != nil {
		return Money{}, wrapError(op, "target_unit", ErrInvalidMoney, nil, err, "")
	}
	if err := mode.validate(op); err != nil {
		return Money{}, err
	}
	if rate == nil || rate.Sign() <= 0 {
		return Money{}, newError(op, "rate", ErrInvalidRate, nil,
			"quote-major-per-base-major rate must be positive")
	}

	exact := new(big.Rat).SetInt64(amount.minorUnits)
	exact.Mul(exact, new(big.Rat).Set(rate))
	exact.Mul(exact, new(big.Rat).SetInt(pow10(int(target.isoMinorExponent))))
	exact.Quo(exact, new(big.Rat).SetInt(pow10(int(amount.unit.isoMinorExponent))))

	minor, err := roundRat(exact, mode)
	if err != nil {
		return Money{}, err
	}
	if !minor.IsInt64() {
		return Money{}, overflowError(op)
	}
	return Money{minorUnits: minor.Int64(), unit: target}, nil
}

// ConvertTo is the method form of Convert.
func (m Money) ConvertTo(target CurrencyUnit, rate *big.Rat, mode RoundingMode) (Money, error) {
	return Convert(m, target, rate, mode)
}

// ParseRate parses a positive, exact, locale-independent decimal rate. It does
// not accept signs, whitespace, grouping, fractions, or exponent notation.
func ParseRate(value string) (*big.Rat, error) {
	const op = "ParseRate"
	if value == "" {
		return nil, newError(op, "rate", ErrInvalidRate, nil, "rate is empty")
	}

	dot := -1
	for i := range value {
		switch char := value[i]; {
		case char == '.':
			if dot >= 0 {
				return nil, newError(op, "rate", ErrInvalidRate, nil, "more than one decimal point")
			}
			dot = i
		case char < '0' || char > '9':
			return nil, newError(op, "rate", ErrInvalidRate, nil,
				"only ASCII decimal digits are allowed")
		}
	}

	whole, fraction := value, ""
	if dot >= 0 {
		whole, fraction = value[:dot], value[dot+1:]
		if whole == "" || fraction == "" {
			return nil, newError(op, "rate", ErrInvalidRate, nil,
				"digits are required on both sides of a decimal point")
		}
	}

	numerator := new(big.Int)
	if _, ok := numerator.SetString(whole+fraction, 10); !ok {
		return nil, newError(op, "rate", ErrInvalidRate, nil, "could not parse rate")
	}
	if numerator.Sign() <= 0 {
		return nil, newError(op, "rate", ErrInvalidRate, nil, "rate must be greater than zero")
	}
	return new(big.Rat).SetFrac(numerator, pow10(len(fraction))), nil
}
