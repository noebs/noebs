// Package groosh provides exact, currency-aware monetary values.
package groosh

import (
	"errors"
	"fmt"
)

// Sentinel errors are intentionally stable so callers can classify failures
// with errors.Is without inspecting error strings.
var (
	ErrInvalidCurrencyUnit      = errors.New("invalid currency unit")
	ErrMissingVersionID         = errors.New("missing currency unit version ID")
	ErrInvalidCurrencyCode      = errors.New("invalid currency code")
	ErrMissingISOMinorExponent  = errors.New("missing ISO minor exponent")
	ErrMissingDisplayExponent   = errors.New("missing display exponent")
	ErrMissingCashExponent      = errors.New("missing cash exponent")
	ErrInvalidCashRule          = errors.New("invalid cash rounding rule")
	ErrInvalidEffectiveInterval = errors.New("invalid effective interval")

	ErrInvalidMoney          = errors.New("invalid money")
	ErrInvalidAmount         = errors.New("invalid amount")
	ErrInvalidAmountSyntax   = errors.New("invalid amount syntax")
	ErrInexactAmount         = errors.New("amount is not exactly representable")
	ErrInvalidCanonicalMoney = errors.New("invalid canonical money")

	ErrCurrencyMismatch       = errors.New("currency mismatch")
	ErrUnitVersionMismatch    = errors.New("currency unit version mismatch")
	ErrUnitDefinitionMismatch = errors.New("currency unit definition mismatch")

	ErrOverflow            = errors.New("monetary overflow")
	ErrInvalidRate         = errors.New("invalid conversion rate")
	ErrInvalidRoundingMode = errors.New("invalid rounding mode")
	ErrInvalidAllocation   = errors.New("invalid allocation")
)

// OpError adds operation and field context while preserving stable error
// classification. Kind is the most specific sentinel; Category is an optional
// broader sentinel (for example ErrInvalidCurrencyUnit).
type OpError struct {
	Op       string
	Field    string
	Kind     error
	Category error
	Cause    error
	Detail   string
}

func (e *OpError) Error() string {
	if e == nil {
		return ""
	}

	message := "groosh"
	if e.Op != "" {
		message += "." + e.Op
	}
	if e.Field != "" {
		message += " " + e.Field
	}
	if e.Kind != nil {
		message += ": " + e.Kind.Error()
	}
	if e.Cause != nil && e.Cause != e.Kind {
		message += ": " + e.Cause.Error()
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

// Unwrap exposes a wrapped cause when present, otherwise the specific sentinel.
func (e *OpError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Cause != nil {
		return e.Cause
	}
	return e.Kind
}

// Is also matches the broader category, when one is present.
func (e *OpError) Is(target error) bool {
	if e == nil {
		return false
	}
	return target == e.Kind || target == e.Category || (e.Cause != nil && errors.Is(e.Cause, target))
}

func wrapError(op, field string, kind, category, cause error, detail string) error {
	return &OpError{
		Op:       op,
		Field:    field,
		Kind:     kind,
		Category: category,
		Cause:    cause,
		Detail:   detail,
	}
}

func newError(op, field string, kind, category error, detail string) error {
	if kind == nil {
		kind = errors.New("unknown error")
	}
	return &OpError{
		Op:       op,
		Field:    field,
		Kind:     kind,
		Category: category,
		Detail:   detail,
	}
}

func overflowError(op string) error {
	return newError(op, "minor_units", ErrOverflow, nil, "result does not fit in int64")
}

func invalidUnitField(op, field string, kind error, detail string) error {
	return newError(op, field, kind, ErrInvalidCurrencyUnit, detail)
}

func invalidMoneyError(op string, cause error) error {
	return wrapError(op, "money", ErrInvalidMoney, nil, cause, "")
}

func mismatchError(op string, left, right CurrencyUnit) error {
	switch {
	case left.code != right.code:
		return newError(op, "currency", ErrCurrencyMismatch, nil,
			fmt.Sprintf("%s != %s", left.code, right.code))
	case left.versionID != right.versionID:
		return newError(op, "unit_version", ErrUnitVersionMismatch, nil,
			fmt.Sprintf("%d != %d", left.versionID, right.versionID))
	default:
		return newError(op, "unit", ErrUnitDefinitionMismatch, nil,
			"snapshots with the same identity have different definitions")
	}
}
