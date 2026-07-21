package store

import "context"

// validateCurrencyUnitIdentity verifies an explicitly supplied immutable unit
// snapshot. It deliberately does not resolve a current unit from a currency
// code: callers at the boundary must choose the version they intend to use.
func (s *Store) validateCurrencyUnitIdentity(ctx context.Context, currency string, currencyUnitID int64) error {
	_, err := s.getCurrencyUnitIdentity(ctx, currency, currencyUnitID)
	return err
}

func (s *Store) getCurrencyUnitIdentity(ctx context.Context, currency string, currencyUnitID int64) (*CurrencyUnitVersion, error) {
	currency, err := ValidateCurrencyCode(currency)
	if err != nil {
		return nil, err
	}
	if currencyUnitID == 0 {
		return nil, ErrMissingCurrencyUnitID
	}
	if currencyUnitID < 0 {
		return nil, ErrInvalidCurrencyUnitID
	}
	unit, err := s.GetCurrencyUnitByID(ctx, currencyUnitID)
	if err != nil {
		return nil, err
	}
	if unit.CurrencyCode != currency {
		return nil, ErrCurrencyMismatch
	}
	if !unit.ISOMinorExponent.Valid {
		return nil, ErrCurrencyScaleUnavailable
	}
	return unit, nil
}
