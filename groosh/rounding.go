package groosh

import "math/big"

// RoundingMode specifies how a rational value is rounded to an integer. The
// zero value is invalid so every operation must receive an explicit policy.
type RoundingMode uint8

const (
	RoundHalfEven RoundingMode = iota + 1
	RoundHalfAwayFromZero
	RoundTowardZero
	RoundFloor
	RoundCeiling
)

func (mode RoundingMode) String() string {
	switch mode {
	case RoundHalfEven:
		return "half_even"
	case RoundHalfAwayFromZero:
		return "half_away_from_zero"
	case RoundTowardZero:
		return "toward_zero"
	case RoundFloor:
		return "floor"
	case RoundCeiling:
		return "ceiling"
	default:
		return "invalid"
	}
}

// ParseRoundingMode parses the stable names used by persistence and transport
// boundaries. Empty or unknown values are errors; no mode is defaulted.
func ParseRoundingMode(value string) (RoundingMode, error) {
	switch value {
	case "half_even":
		return RoundHalfEven, nil
	case "half_away_from_zero":
		return RoundHalfAwayFromZero, nil
	case "toward_zero":
		return RoundTowardZero, nil
	case "floor":
		return RoundFloor, nil
	case "ceiling":
		return RoundCeiling, nil
	default:
		return 0, newError("ParseRoundingMode", "rounding_mode", ErrInvalidRoundingMode, nil,
			"unknown rounding mode")
	}
}

// RoundMinorUnits rounds an exact rational minor-unit value to the int64
// ledger domain using an explicit policy. The input is copied and is never
// mutated. This is useful for percentage fees and other scalar calculations
// whose result is already denominated in a known currency unit.
func RoundMinorUnits(value *big.Rat, mode RoundingMode) (int64, error) {
	const op = "RoundMinorUnits"
	if value == nil {
		return 0, newError(op, "value", ErrInvalidAmount, nil, "rational value is nil")
	}
	rounded, err := roundRat(new(big.Rat).Set(value), mode)
	if err != nil {
		return 0, err
	}
	if !rounded.IsInt64() {
		return 0, overflowError(op)
	}
	return rounded.Int64(), nil
}

func (mode RoundingMode) validate(op string) error {
	switch mode {
	case RoundHalfEven, RoundHalfAwayFromZero, RoundTowardZero, RoundFloor, RoundCeiling:
		return nil
	default:
		return newError(op, "rounding_mode", ErrInvalidRoundingMode, nil,
			"rounding mode must be explicit")
	}
}

// roundRat rounds value to an integer without mutating value.
func roundRat(value *big.Rat, mode RoundingMode) (*big.Int, error) {
	if err := mode.validate("roundRat"); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, newError("roundRat", "value", ErrInvalidAmount, nil, "rational value is nil")
	}

	numerator := new(big.Int).Set(value.Num())
	denominator := new(big.Int).Set(value.Denom())
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() == 0 {
		return quotient, nil
	}

	step := func() {
		if numerator.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}

	switch mode {
	case RoundTowardZero:
		return quotient, nil
	case RoundFloor:
		if numerator.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		}
		return quotient, nil
	case RoundCeiling:
		if numerator.Sign() > 0 {
			quotient.Add(quotient, big.NewInt(1))
		}
		return quotient, nil
	case RoundHalfAwayFromZero, RoundHalfEven:
		twiceRemainder := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1)
		comparison := twiceRemainder.Cmp(denominator)
		if comparison > 0 || (comparison == 0 &&
			(mode == RoundHalfAwayFromZero || quotient.Bit(0) == 1)) {
			step()
		}
		return quotient, nil
	default:
		panic("unreachable rounding mode")
	}
}

func pow10(exponent int) *big.Int {
	if exponent < 0 {
		panic("negative decimal exponent")
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}
