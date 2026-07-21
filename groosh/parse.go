package groosh

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// ParseMajor parses a strict locale-independent major-unit decimal using the
// unit's ISO minor exponent. It accepts only an optional leading sign, ASCII
// digits, and an optional decimal point. Whitespace, grouping, currency
// symbols, and exponent notation are rejected. Fractional digits beyond the
// operational exponent are accepted only when every excess digit is zero, so
// parsing never rounds.
func ParseMajor(amount string, unit CurrencyUnit) (Money, error) {
	const op = "ParseMajor"
	exponentValue, err := unit.operationalExponent(op)
	if err != nil {
		return Money{}, invalidMoneyError(op, err)
	}
	if amount == "" {
		return Money{}, invalidAmountSyntax(op, "amount is empty")
	}

	negative := false
	start := 0
	if amount[0] == '+' || amount[0] == '-' {
		negative = amount[0] == '-'
		start = 1
		if len(amount) == 1 {
			return Money{}, invalidAmountSyntax(op, "sign must be followed by digits")
		}
	}

	dot := -1
	for i := start; i < len(amount); i++ {
		switch char := amount[i]; {
		case char == '.':
			if dot >= 0 {
				return Money{}, invalidAmountSyntax(op, "more than one decimal point")
			}
			dot = i
		case char < '0' || char > '9':
			return Money{}, invalidAmountSyntax(op, "only ASCII decimal digits are allowed")
		}
	}

	whole := amount[start:]
	frac := ""
	if dot >= 0 {
		whole = amount[start:dot]
		frac = amount[dot+1:]
		if whole == "" || frac == "" {
			return Money{}, invalidAmountSyntax(op, "digits are required on both sides of a decimal point")
		}
	}
	if whole == "" {
		return Money{}, invalidAmountSyntax(op, "whole-number digits are required")
	}

	exponent := int(exponentValue)
	if len(frac) > exponent {
		for i := exponent; i < len(frac); i++ {
			if frac[i] != '0' {
				return Money{}, newError(op, "amount", ErrInexactAmount, ErrInvalidAmount,
					"fraction exceeds the ISO minor exponent")
			}
		}
		frac = frac[:exponent]
	}

	whole = strings.TrimLeft(whole, "0")
	if len(whole) > 19 {
		return Money{}, overflowError(op)
	}
	if len(frac) < exponent {
		frac += strings.Repeat("0", exponent-len(frac))
	}

	digits := strings.TrimLeft(whole+frac, "0")
	if digits == "" {
		return Money{unit: unit}, nil
	}
	if len(digits) > 19 {
		return Money{}, overflowError(op)
	}
	magnitude, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return Money{}, overflowError(op)
	}

	var minor int64
	if negative {
		if magnitude > uint64(1)<<63 {
			return Money{}, overflowError(op)
		}
		if magnitude == uint64(1)<<63 {
			minor = math.MinInt64
		} else {
			minor = -int64(magnitude)
		}
	} else {
		if magnitude > math.MaxInt64 {
			return Money{}, overflowError(op)
		}
		minor = int64(magnitude)
	}

	return Money{minorUnits: minor, unit: unit}, nil
}

// ParseCanonical parses the exact, version-bound form emitted by
// Money.CanonicalString: "CCC@version major". Noncanonical numeric spellings
// such as leading plus signs, redundant zeroes, omitted fixed-scale digits, or
// negative zero are rejected even when they are numerically exact.
func ParseCanonical(value string, unit CurrencyUnit) (Money, error) {
	const op = "ParseCanonical"
	if _, err := unit.operationalExponent(op); err != nil {
		return Money{}, invalidMoneyError(op, err)
	}

	space := strings.IndexByte(value, ' ')
	if space < 0 || strings.IndexByte(value[space+1:], ' ') >= 0 {
		return Money{}, invalidCanonicalError(op, nil,
			"expected CCC@version followed by one ASCII space and a major amount")
	}
	identity := value[:space]
	at := strings.IndexByte(identity, '@')
	if at != 3 || !isCanonicalCurrencyCode(identity[:at]) {
		return Money{}, invalidCanonicalError(op, nil, "invalid currency identity")
	}
	if identity[:3] != unit.code {
		return Money{}, newError(op, "currency", ErrCurrencyMismatch, nil,
			identity[:3]+" != "+unit.code)
	}
	versionText := identity[at+1:]
	if versionText == "" || (len(versionText) > 1 && versionText[0] == '0') {
		return Money{}, invalidCanonicalError(op, nil, "invalid currency unit version")
	}
	for i := range versionText {
		if versionText[i] < '0' || versionText[i] > '9' {
			return Money{}, invalidCanonicalError(op, nil, "invalid currency unit version")
		}
	}
	version, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || version <= 0 {
		return Money{}, invalidCanonicalError(op, err, "invalid currency unit version")
	}
	if version != unit.versionID {
		return Money{}, newError(op, "unit_version", ErrUnitVersionMismatch, nil,
			strconv.FormatInt(version, 10)+" != "+strconv.FormatInt(unit.versionID, 10))
	}

	money, err := ParseMajor(value[space+1:], unit)
	if err != nil {
		return Money{}, invalidCanonicalError(op, err, "invalid canonical major amount")
	}
	canonical, err := money.CanonicalString()
	if err != nil {
		return Money{}, invalidCanonicalError(op, err, "could not format parsed amount")
	}
	if canonical != value {
		return Money{}, invalidCanonicalError(op, nil, "value is exact but not canonical")
	}
	return money, nil
}

// MajorString returns the fixed-scale locale-independent major amount using
// the operational ISO minor exponent.
func (m Money) MajorString() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	return formatScaledInteger(big.NewInt(m.minorUnits), m.unit.isoMinorExponent), nil
}

// MinorString returns the signed base-10 minor-unit count without exposing it
// as an imprecise JSON number at frontend boundaries.
func (m Money) MinorString() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	return strconv.FormatInt(m.minorUnits, 10), nil
}

// CanonicalString returns a deterministic, lossless representation containing
// the currency code, unit version ID, and fixed operational scale.
func (m Money) CanonicalString() (string, error) {
	major, err := m.MajorString()
	if err != nil {
		return "", err
	}
	return m.unit.code + "@" + strconv.FormatInt(m.unit.versionID, 10) + " " + major, nil
}

// Display returns a code-prefixed amount at the snapshot's display exponent.
// It fails with ErrInexactAmount when reducing the operational scale would
// discard value; use DisplayRounded to make that policy explicit.
func (m Money) Display() (string, error) {
	major, err := m.displayMajorExact()
	if err != nil {
		return "", err
	}
	return m.unit.code + " " + major, nil
}

// DisplayRounded returns a code-prefixed amount at the display exponent using
// the explicit mode when scale reduction is inexact.
func (m Money) DisplayRounded(mode RoundingMode) (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	if err := mode.validate("Money.DisplayRounded"); err != nil {
		return "", err
	}
	exact := m.displayMinorRat()
	displayMinor, err := roundRat(exact, mode)
	if err != nil {
		return "", err
	}
	return m.unit.code + " " + formatScaledInteger(displayMinor, m.unit.displayExponent), nil
}

// String implements fmt.Stringer using the version-bound canonical form. Code
// that can receive invalid zero values should call CanonicalString directly.
func (m Money) String() string {
	value, err := m.CanonicalString()
	if err != nil {
		return "<invalid money>"
	}
	return value
}

// MarshalText emits the version-bound canonical representation. Unmarshalling
// requires an explicit catalog snapshot and is therefore performed by
// ParseCanonical rather than an implicit global resolver.
func (m Money) MarshalText() ([]byte, error) {
	value, err := m.CanonicalString()
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}

func (m Money) displayMajorExact() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	exact := m.displayMinorRat()
	if !exact.IsInt() {
		return "", newError("Money.Display", "display_exponent", ErrInexactAmount,
			ErrInvalidAmount, "display scale reduction requires an explicit rounding mode")
	}
	return formatScaledInteger(exact.Num(), m.unit.displayExponent), nil
}

func (m Money) displayMinorRat() *big.Rat {
	exact := new(big.Rat).SetInt64(m.minorUnits)
	exact.Mul(exact, new(big.Rat).SetInt(pow10(int(m.unit.displayExponent))))
	exact.Quo(exact, new(big.Rat).SetInt(pow10(int(m.unit.isoMinorExponent))))
	return exact
}

func formatScaledInteger(value *big.Int, exponent uint8) string {
	negative := value.Sign() < 0
	magnitude := new(big.Int).Abs(new(big.Int).Set(value))
	digits := magnitude.String()
	scale := int(exponent)
	if scale > 0 {
		if len(digits) <= scale {
			digits = strings.Repeat("0", scale-len(digits)+1) + digits
		}
		split := len(digits) - scale
		digits = digits[:split] + "." + digits[split:]
	}
	if negative {
		digits = "-" + digits
	}
	return digits
}

func int64Magnitude(value int64) uint64 {
	if value >= 0 {
		return uint64(value)
	}
	return uint64(-(value + 1)) + 1
}

func invalidAmountSyntax(op, detail string) error {
	return newError(op, "amount", ErrInvalidAmountSyntax, ErrInvalidAmount, detail)
}

func invalidCanonicalError(op string, cause error, detail string) error {
	return wrapError(op, "value", ErrInvalidCanonicalMoney, ErrInvalidAmount, cause, detail)
}
