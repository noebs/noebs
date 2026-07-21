package rates

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/adonese/noebs/groosh"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

// ConvertMinorUnits converts a positive amount between explicit, versioned
// currency-unit snapshots. rate is quote-major-units per base-major-unit; it
// is never applied directly to minor units.
func ConvertMinorUnits(
	amount int64,
	rate decimal.Decimal,
	baseUnit, quoteUnit *walletstore.CurrencyUnitVersion,
	rounding groosh.RoundingMode,
) (int64, error) {
	if amount <= 0 {
		return 0, walletstore.ErrInvalidAmount
	}
	if rate.Cmp(decimal.Zero) <= 0 {
		return 0, walletstore.ErrInvalidRate
	}

	base, err := currencyUnit(baseUnit)
	if err != nil {
		return 0, err
	}
	quote, err := currencyUnit(quoteUnit)
	if err != nil {
		return 0, err
	}

	money, err := groosh.NewMoney(amount, base)
	if err != nil {
		return 0, classifyGrooshError(err)
	}
	exactRate, err := groosh.ParseRate(rate.String())
	if err != nil {
		return 0, classifyGrooshError(err)
	}
	converted, err := groosh.Convert(money, quote, exactRate, rounding)
	if err != nil {
		return 0, classifyGrooshError(err)
	}
	if converted.MinorUnits() <= 0 {
		return 0, walletstore.ErrInvalidAmount
	}
	return converted.MinorUnits(), nil
}

func currencyUnit(stored *walletstore.CurrencyUnitVersion) (groosh.CurrencyUnit, error) {
	if stored == nil {
		return groosh.CurrencyUnit{}, walletstore.ErrCurrencyNotFound
	}
	if _, err := walletstore.ValidateCurrencyCode(stored.CurrencyCode); err != nil {
		return groosh.CurrencyUnit{}, fmt.Errorf("%w: %s", walletstore.ErrCurrencyScaleUnavailable, stored.CurrencyCode)
	}
	if !stored.ISOMinorExponent.Valid {
		return groosh.CurrencyUnit{}, walletstore.ErrCurrencyScaleUnavailable
	}
	if stored.ISOMinorExponent.Int16 < 0 || stored.ISOMinorExponent.Int16 > math.MaxUint8 ||
		stored.DisplayExponent < 0 || stored.DisplayExponent > math.MaxUint8 ||
		stored.CashExponent < 0 || stored.CashExponent > math.MaxUint8 {
		return groosh.CurrencyUnit{}, fmt.Errorf("%w: %s", walletstore.ErrCurrencyScaleUnavailable, stored.CurrencyCode)
	}

	isoExponent := uint8(stored.ISOMinorExponent.Int16)
	displayExponent := uint8(stored.DisplayExponent)
	cashExponent := uint8(stored.CashExponent)
	var effectiveUntil *time.Time
	if stored.ValidTo.Valid {
		value := stored.ValidTo.Time
		effectiveUntil = &value
	}
	unit, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        stored.ID,
		Code:             stored.CurrencyCode,
		ISOMinorExponent: &isoExponent,
		DisplayExponent:  &displayExponent,
		CashExponent:     &cashExponent,
		CashIncrement:    stored.CashRoundingIncrement,
		EffectiveFrom:    stored.ValidFrom,
		EffectiveUntil:   effectiveUntil,
	})
	if err != nil {
		return groosh.CurrencyUnit{}, fmt.Errorf("%w: %s: %v", walletstore.ErrCurrencyScaleUnavailable, stored.CurrencyCode, err)
	}
	return unit, nil
}

func classifyGrooshError(err error) error {
	switch {
	case errors.Is(err, groosh.ErrInvalidRate):
		return walletstore.ErrInvalidRate
	case errors.Is(err, groosh.ErrOverflow):
		return walletstore.ErrAmountOverflow
	case errors.Is(err, groosh.ErrInvalidAmount):
		return walletstore.ErrInvalidAmount
	case errors.Is(err, groosh.ErrMissingISOMinorExponent), errors.Is(err, groosh.ErrInvalidCurrencyUnit):
		return walletstore.ErrCurrencyScaleUnavailable
	default:
		return err
	}
}
