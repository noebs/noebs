package groosh

import (
	"math/big"
	"time"
)

// CurrencyUnitSpec is the boundary representation used to construct an
// immutable CurrencyUnit snapshot. Pointer fields are copied; the resulting
// CurrencyUnit never retains caller-owned mutable state.
//
// ISOMinorExponent is the operational ledger exponent for Money minor units.
// It is nullable so a catalog can represent definitions without ISO minor-unit
// metadata, but such a definition cannot construct Money. DisplayExponent and
// CashExponent are pointers because zero is meaningful and must remain
// distinguishable from an omitted boundary field.
type CurrencyUnitSpec struct {
	VersionID        int64
	Code             string
	ISOMinorExponent *uint8
	DisplayExponent  *uint8
	CashExponent     *uint8
	CashIncrement    int64
	EffectiveFrom    time.Time
	EffectiveUntil   *time.Time
}

// CurrencyUnit is an immutable, versioned snapshot of a currency's scales and
// cash rules. Its zero value is invalid. Construct values with NewCurrencyUnit.
type CurrencyUnit struct {
	versionID int64
	code      string

	isoMinorExponent    uint8
	hasISOMinorExponent bool
	displayExponent     uint8
	cashExponent        uint8
	cashIncrement       int64

	effectiveFrom     time.Time
	effectiveUntil    time.Time
	hasEffectiveUntil bool
}

// NewCurrencyUnit validates and copies a currency-unit snapshot. Codes must
// already be canonical three-letter uppercase ASCII; this constructor does not
// silently trim or normalize boundary input.
func NewCurrencyUnit(spec CurrencyUnitSpec) (CurrencyUnit, error) {
	const op = "NewCurrencyUnit"

	if spec.VersionID <= 0 {
		return CurrencyUnit{}, invalidUnitField(op, "version_id", ErrMissingVersionID,
			"must be greater than zero")
	}
	if !isCanonicalCurrencyCode(spec.Code) {
		return CurrencyUnit{}, invalidUnitField(op, "code", ErrInvalidCurrencyCode,
			"must be exactly three uppercase ASCII letters")
	}
	if spec.DisplayExponent == nil {
		return CurrencyUnit{}, invalidUnitField(op, "display_exponent", ErrMissingDisplayExponent,
			"must be present, including when its value is zero")
	}
	if spec.CashExponent == nil {
		return CurrencyUnit{}, invalidUnitField(op, "cash_exponent", ErrMissingCashExponent,
			"must be present, including when its value is zero")
	}
	if spec.CashIncrement <= 0 {
		return CurrencyUnit{}, invalidUnitField(op, "cash_increment", ErrInvalidCashRule,
			"must be greater than zero")
	}
	if spec.EffectiveFrom.IsZero() {
		return CurrencyUnit{}, invalidUnitField(op, "effective_from", ErrInvalidEffectiveInterval,
			"must be present")
	}

	from := canonicalTime(spec.EffectiveFrom)
	var until time.Time
	hasUntil := spec.EffectiveUntil != nil
	if hasUntil {
		if spec.EffectiveUntil.IsZero() {
			return CurrencyUnit{}, invalidUnitField(op, "effective_until", ErrInvalidEffectiveInterval,
				"must not be zero when present")
		}
		until = canonicalTime(*spec.EffectiveUntil)
		if !until.After(from) {
			return CurrencyUnit{}, invalidUnitField(op, "effective_until", ErrInvalidEffectiveInterval,
				"must be later than effective_from")
		}
	}

	unit := CurrencyUnit{
		versionID:         spec.VersionID,
		code:              spec.Code,
		displayExponent:   *spec.DisplayExponent,
		cashExponent:      *spec.CashExponent,
		cashIncrement:     spec.CashIncrement,
		effectiveFrom:     from,
		effectiveUntil:    until,
		hasEffectiveUntil: hasUntil,
	}
	if spec.ISOMinorExponent != nil {
		unit.isoMinorExponent = *spec.ISOMinorExponent
		unit.hasISOMinorExponent = true
		if unit.cashExponent > unit.isoMinorExponent {
			return CurrencyUnit{}, invalidUnitField(op, "cash_exponent", ErrInvalidCashRule,
				"cash quantum must be representable in ISO minor units")
		}
		if !unit.cashQuantumBig().IsInt64() {
			return CurrencyUnit{}, invalidUnitField(op, "cash_increment", ErrInvalidCashRule,
				"cash quantum does not fit in int64 minor units")
		}
	}

	return unit, nil
}

// Validate reports whether u is a valid constructed catalog snapshot. A valid
// catalog snapshot may lack ISO minor-unit metadata; Money construction applies
// the stronger operational validation.
func (u CurrencyUnit) Validate() error {
	const op = "CurrencyUnit.Validate"

	if u.versionID <= 0 {
		return invalidUnitField(op, "version_id", ErrMissingVersionID, "invalid snapshot")
	}
	if !isCanonicalCurrencyCode(u.code) {
		return invalidUnitField(op, "code", ErrInvalidCurrencyCode, "invalid snapshot")
	}
	if u.cashIncrement <= 0 {
		return invalidUnitField(op, "cash_rule", ErrInvalidCashRule, "invalid snapshot")
	}
	if u.hasISOMinorExponent &&
		(u.cashExponent > u.isoMinorExponent || !u.cashQuantumBig().IsInt64()) {
		return invalidUnitField(op, "cash_rule", ErrInvalidCashRule, "invalid snapshot")
	}
	if u.effectiveFrom.IsZero() {
		return invalidUnitField(op, "effective_from", ErrInvalidEffectiveInterval, "invalid snapshot")
	}
	if u.hasEffectiveUntil && (u.effectiveUntil.IsZero() || !u.effectiveUntil.After(u.effectiveFrom)) {
		return invalidUnitField(op, "effective_until", ErrInvalidEffectiveInterval, "invalid snapshot")
	}
	return nil
}

func (u CurrencyUnit) VersionID() int64         { return u.versionID }
func (u CurrencyUnit) Code() string             { return u.code }
func (u CurrencyUnit) DisplayExponent() uint8   { return u.displayExponent }
func (u CurrencyUnit) CashExponent() uint8      { return u.cashExponent }
func (u CurrencyUnit) CashIncrement() int64     { return u.cashIncrement }
func (u CurrencyUnit) EffectiveFrom() time.Time { return u.effectiveFrom }

// ISOMinorExponent returns (value, true) when ISO metadata is present. A
// present value of zero remains distinguishable from absence.
func (u CurrencyUnit) ISOMinorExponent() (uint8, bool) {
	return u.isoMinorExponent, u.hasISOMinorExponent
}

// MinorExponent is an alias for ISOMinorExponent and identifies the scale used
// by Money minor units.
func (u CurrencyUnit) MinorExponent() (uint8, bool) {
	return u.ISOMinorExponent()
}

// EffectiveUntil returns the exclusive end of the interval, when one exists.
func (u CurrencyUnit) EffectiveUntil() (time.Time, bool) {
	return u.effectiveUntil, u.hasEffectiveUntil
}

// IsEffectiveAt applies the snapshot's half-open [from, until) interval.
func (u CurrencyUnit) IsEffectiveAt(at time.Time) (bool, error) {
	if err := u.Validate(); err != nil {
		return false, err
	}
	if at.IsZero() {
		return false, newError("CurrencyUnit.IsEffectiveAt", "at", ErrInvalidEffectiveInterval, nil,
			"must be present")
	}
	at = canonicalTime(at)
	return !at.Before(u.effectiveFrom) && (!u.hasEffectiveUntil || at.Before(u.effectiveUntil)), nil
}

// Equal reports complete snapshot equality, not merely code equality.
func (u CurrencyUnit) Equal(other CurrencyUnit) bool {
	return u.versionID == other.versionID &&
		u.code == other.code &&
		u.isoMinorExponent == other.isoMinorExponent &&
		u.hasISOMinorExponent == other.hasISOMinorExponent &&
		u.displayExponent == other.displayExponent &&
		u.cashExponent == other.cashExponent &&
		u.cashIncrement == other.cashIncrement &&
		u.effectiveFrom.Equal(other.effectiveFrom) &&
		u.effectiveUntil.Equal(other.effectiveUntil) &&
		u.hasEffectiveUntil == other.hasEffectiveUntil
}

// SameIdentity reports whether two snapshots have the same canonical currency
// code and database version ID.
func (u CurrencyUnit) SameIdentity(other CurrencyUnit) bool {
	return u.versionID > 0 && other.versionID > 0 &&
		isCanonicalCurrencyCode(u.code) && isCanonicalCurrencyCode(other.code) &&
		u.code == other.code && u.versionID == other.versionID
}

// CashQuantumMinorUnits returns the cash increment expressed in operational
// ISO minor units.
func (u CurrencyUnit) CashQuantumMinorUnits() (int64, error) {
	if err := u.Validate(); err != nil {
		return 0, err
	}
	if !u.hasISOMinorExponent {
		return 0, newError("CurrencyUnit.CashQuantumMinorUnits", "iso_minor_exponent",
			ErrMissingISOMinorExponent, nil, "cash quantum has no operational minor-unit scale")
	}
	return u.cashQuantumBig().Int64(), nil
}

func (u CurrencyUnit) operationalExponent(op string) (uint8, error) {
	if err := u.Validate(); err != nil {
		return 0, err
	}
	if !u.hasISOMinorExponent {
		return 0, newError(op, "iso_minor_exponent", ErrMissingISOMinorExponent, nil,
			"Money requires an explicit operational exponent")
	}
	return u.isoMinorExponent, nil
}

// cashQuantumBig assumes operational validation has established that the ISO
// exponent is present and no smaller than the cash exponent.
func (u CurrencyUnit) cashQuantumBig() *big.Int {
	quantum := new(big.Int).SetInt64(u.cashIncrement)
	delta := int(u.isoMinorExponent - u.cashExponent)
	return quantum.Mul(quantum, pow10(delta))
}

func isCanonicalCurrencyCode(code string) bool {
	if len(code) != 3 {
		return false
	}
	for i := range code {
		if code[i] < 'A' || code[i] > 'Z' {
			return false
		}
	}
	return true
}

func canonicalTime(value time.Time) time.Time {
	return value.Round(0).UTC()
}
