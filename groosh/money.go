package groosh

import (
	"math"
	"math/big"
)

// Money is an immutable signed count of operational minor units paired with
// the exact CurrencyUnit snapshot that gives those units meaning. Its zero
// value is invalid; a monetary zero is constructed with NewMoney(0, unit).
type Money struct {
	minorUnits int64
	unit       CurrencyUnit
}

// NewMoney constructs a Money value from an explicit minor-unit count.
func NewMoney(minorUnits int64, unit CurrencyUnit) (Money, error) {
	if _, err := unit.operationalExponent("NewMoney"); err != nil {
		return Money{}, invalidMoneyError("NewMoney", err)
	}
	return Money{minorUnits: minorUnits, unit: unit}, nil
}

// New is a concise alias for NewMoney.
func New(minorUnits int64, unit CurrencyUnit) (Money, error) {
	return NewMoney(minorUnits, unit)
}

// Validate reports whether m carries a valid currency-unit snapshot.
func (m Money) Validate() error {
	if _, err := m.unit.operationalExponent("Money.Validate"); err != nil {
		return invalidMoneyError("Money.Validate", err)
	}
	return nil
}

func (m Money) MinorUnits() int64    { return m.minorUnits }
func (m Money) Unit() CurrencyUnit   { return m.unit }
func (m Money) CurrencyCode() string { return m.unit.code }
func (m Money) UnitVersionID() int64 { return m.unit.versionID }

// IsZero reports whether the stored minor-unit count is zero. Like Sign, it
// reports zero for an invalid Money zero value; callers that can receive an
// unconstructed value must call Validate first.
func (m Money) IsZero() bool { return m.minorUnits == 0 }

// Sign returns -1, 0, or +1 according to the monetary amount. It returns zero
// for an invalid Money; callers that can receive zero values should Validate.
func (m Money) Sign() int {
	switch {
	case m.minorUnits < 0:
		return -1
	case m.minorUnits > 0:
		return 1
	default:
		return 0
	}
}

// Add adds compatible monetary values with exact int64 overflow detection.
func (m Money) Add(other Money) (Money, error) {
	if err := compatible("Money.Add", m, other); err != nil {
		return Money{}, err
	}
	if (other.minorUnits > 0 && m.minorUnits > math.MaxInt64-other.minorUnits) ||
		(other.minorUnits < 0 && m.minorUnits < math.MinInt64-other.minorUnits) {
		return Money{}, overflowError("Money.Add")
	}
	return Money{minorUnits: m.minorUnits + other.minorUnits, unit: m.unit}, nil
}

// Sub subtracts compatible monetary values with exact int64 overflow detection.
func (m Money) Sub(other Money) (Money, error) {
	if err := compatible("Money.Sub", m, other); err != nil {
		return Money{}, err
	}
	if (other.minorUnits < 0 && m.minorUnits > math.MaxInt64+other.minorUnits) ||
		(other.minorUnits > 0 && m.minorUnits < math.MinInt64+other.minorUnits) {
		return Money{}, overflowError("Money.Sub")
	}
	return Money{minorUnits: m.minorUnits - other.minorUnits, unit: m.unit}, nil
}

// Neg returns the additive inverse of m.
func (m Money) Neg() (Money, error) {
	if err := m.Validate(); err != nil {
		return Money{}, err
	}
	if m.minorUnits == math.MinInt64 {
		return Money{}, overflowError("Money.Neg")
	}
	return Money{minorUnits: -m.minorUnits, unit: m.unit}, nil
}

// Abs returns the absolute value of m.
func (m Money) Abs() (Money, error) {
	if err := m.Validate(); err != nil {
		return Money{}, err
	}
	if m.minorUnits == math.MinInt64 {
		return Money{}, overflowError("Money.Abs")
	}
	if m.minorUnits < 0 {
		return Money{minorUnits: -m.minorUnits, unit: m.unit}, nil
	}
	return m, nil
}

// Cmp compares compatible values, returning -1, 0, or +1.
func (m Money) Cmp(other Money) (int, error) {
	if err := compatible("Money.Cmp", m, other); err != nil {
		return 0, err
	}
	switch {
	case m.minorUnits < other.minorUnits:
		return -1, nil
	case m.minorUnits > other.minorUnits:
		return 1, nil
	default:
		return 0, nil
	}
}

// QuantizeCash rounds m to the snapshot's cash quantum using an explicit mode.
func (m Money) QuantizeCash(mode RoundingMode) (Money, error) {
	if err := m.Validate(); err != nil {
		return Money{}, err
	}
	if err := mode.validate("Money.QuantizeCash"); err != nil {
		return Money{}, err
	}

	quantum := m.unit.cashQuantumBig()
	ratio := new(big.Rat).SetFrac(big.NewInt(m.minorUnits), quantum)
	rounded, err := roundRat(ratio, mode)
	if err != nil {
		return Money{}, err
	}
	rounded.Mul(rounded, quantum)
	if !rounded.IsInt64() {
		return Money{}, overflowError("Money.QuantizeCash")
	}
	return Money{minorUnits: rounded.Int64(), unit: m.unit}, nil
}

// IsCashQuantized reports whether m is already an exact multiple of the cash
// quantum.
func (m Money) IsCashQuantized() (bool, error) {
	if err := m.Validate(); err != nil {
		return false, err
	}
	value := big.NewInt(m.minorUnits)
	return new(big.Int).Rem(value, m.unit.cashQuantumBig()).Sign() == 0, nil
}

func compatible(op string, left, right Money) error {
	if err := left.Validate(); err != nil {
		return err
	}
	if err := right.Validate(); err != nil {
		return err
	}
	if !left.unit.Equal(right.unit) {
		return mismatchError(op, left.unit, right.unit)
	}
	return nil
}
