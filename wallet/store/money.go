package store

import (
	"context"
	"database/sql"
	"time"
)

type CurrencyUnitVersion struct {
	ID                    int64          `db:"id"`
	CurrencyCode          string         `db:"currency_code"`
	NumericCode           sql.NullString `db:"numeric_code"`
	Name                  string         `db:"name"`
	Kind                  string         `db:"kind"`
	IsActive              bool           `db:"is_active"`
	ISOMinorExponent      sql.NullInt16  `db:"iso_minor_exponent"`
	DisplayExponent       int16          `db:"display_exponent"`
	CashExponent          int16          `db:"cash_exponent"`
	CashRoundingIncrement int64          `db:"cash_rounding_increment"`
	ValidFrom             time.Time      `db:"valid_from"`
	ValidTo               sql.NullTime   `db:"valid_to"`
	Source                string         `db:"source"`
	SourceRevision        string         `db:"source_revision"`
	SourcePublishedOn     time.Time      `db:"source_published_on"`
	CreatedAt             time.Time      `db:"created_at"`
}

func ValidateCurrencyCode(code string) (string, error) {
	if code == "" {
		return "", ErrMissingCurrency
	}
	if len(code) != 3 {
		return "", ErrInvalidCurrency
	}
	for index := range len(code) {
		if code[index] < 'A' || code[index] > 'Z' {
			return "", ErrInvalidCurrency
		}
	}
	return code, nil
}

func (s *Store) GetCurrencyUnit(ctx context.Context, currencyCode string, asOf time.Time) (*CurrencyUnitVersion, error) {
	currencyCode, err := ValidateCurrencyCode(currencyCode)
	if err != nil {
		return nil, err
	}
	if asOf.IsZero() {
		return nil, ErrMissingStartTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := db.Rebind(`SELECT
		unit.id,
		unit.currency_code,
		currency.numeric_code,
		currency.name,
		currency.kind,
		currency.is_active,
		unit.iso_minor_exponent,
		unit.display_exponent,
		unit.cash_exponent,
		unit.cash_rounding_increment,
		unit.valid_from,
		unit.valid_to,
		unit.source,
		unit.source_revision,
		unit.source_published_on,
		unit.created_at
	FROM currency_unit_versions unit
	JOIN currencies currency ON currency.code = unit.currency_code
	WHERE unit.currency_code = ?
	  AND unit.valid_from <= CAST(? AS DATE)
	  AND (unit.valid_to IS NULL OR unit.valid_to > CAST(? AS DATE))
	ORDER BY unit.valid_from DESC
	LIMIT 1`)
	var unit CurrencyUnitVersion
	asOfDate := asOf.UTC().Format(time.DateOnly)
	if err := db.GetContext(ctx, &unit, query, currencyCode, asOfDate, asOfDate); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCurrencyNotFound
		}
		return nil, err
	}
	return &unit, nil
}

func (s *Store) GetCurrencyUnitByID(ctx context.Context, currencyUnitID int64) (*CurrencyUnitVersion, error) {
	if currencyUnitID == 0 {
		return nil, ErrMissingCurrencyUnitID
	}
	if currencyUnitID < 0 {
		return nil, ErrInvalidCurrencyUnitID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := db.Rebind(`SELECT
		unit.id,
		unit.currency_code,
		currency.numeric_code,
		currency.name,
		currency.kind,
		currency.is_active,
		unit.iso_minor_exponent,
		unit.display_exponent,
		unit.cash_exponent,
		unit.cash_rounding_increment,
		unit.valid_from,
		unit.valid_to,
		unit.source,
		unit.source_revision,
		unit.source_published_on,
		unit.created_at
	FROM currency_unit_versions unit
	JOIN currencies currency ON currency.code = unit.currency_code
	WHERE unit.id = ?`)
	var unit CurrencyUnitVersion
	if err := db.GetContext(ctx, &unit, query, currencyUnitID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCurrencyNotFound
		}
		return nil, err
	}
	return &unit, nil
}

func (s *Store) ListCurrencyUnits(ctx context.Context, asOf time.Time, activeOnly bool) ([]CurrencyUnitVersion, error) {
	if asOf.IsZero() {
		return nil, ErrMissingStartTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := `SELECT
		unit.id,
		unit.currency_code,
		currency.numeric_code,
		currency.name,
		currency.kind,
		currency.is_active,
		unit.iso_minor_exponent,
		unit.display_exponent,
		unit.cash_exponent,
		unit.cash_rounding_increment,
		unit.valid_from,
		unit.valid_to,
		unit.source,
		unit.source_revision,
		unit.source_published_on,
		unit.created_at
	FROM currency_unit_versions unit
	JOIN currencies currency ON currency.code = unit.currency_code
	WHERE unit.valid_from <= CAST(? AS DATE)
	  AND (unit.valid_to IS NULL OR unit.valid_to > CAST(? AS DATE))`
	asOfDate := asOf.UTC().Format(time.DateOnly)
	args := []any{asOfDate, asOfDate}
	if activeOnly {
		query += " AND currency.is_active = ?"
		args = append(args, true)
	}
	query += " ORDER BY unit.currency_code"
	var units []CurrencyUnitVersion
	if err := db.SelectContext(ctx, &units, db.Rebind(query), args...); err != nil {
		return nil, err
	}
	return units, nil
}
