package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/shopspring/decimal"
)

// GetActiveRateForUnitsAt selects a legacy rate for exact immutable currency
// unit snapshots. This is the conversion-safe lookup when the caller knows the
// provenance of both amounts.
func (s *Store) GetActiveRateForUnitsAt(
	ctx context.Context,
	tenantID, baseCurrency string,
	baseCurrencyUnitID int64,
	quoteCurrency string,
	quoteCurrencyUnitID int64,
	asOf time.Time,
) (*ExchangeRate, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	baseCurrency, quoteCurrency, err = validateExchangeRatePair(baseCurrency, quoteCurrency)
	if err != nil {
		return nil, err
	}
	if err := ValidateCurrencyUnitID(baseCurrencyUnitID); err != nil {
		return nil, err
	}
	if err := ValidateCurrencyUnitID(quoteCurrencyUnitID); err != nil {
		return nil, err
	}
	if asOf.IsZero() {
		return nil, ErrMissingStartTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if err := s.validateCurrencyUnitIdentity(ctx, baseCurrency, baseCurrencyUnitID); err != nil {
		return nil, err
	}
	if err := s.validateCurrencyUnitIdentity(ctx, quoteCurrency, quoteCurrencyUnitID); err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM exchange_rates
		WHERE tenant_id = ?
		AND base_currency = ? AND base_currency_unit_version_id = ?
		AND quote_currency = ? AND quote_currency_unit_version_id = ?
		AND effective_from <= ?
		AND (effective_to IS NULL OR effective_to > ?)
		ORDER BY effective_from DESC
		LIMIT 1`)
	var rate ExchangeRate
	asOf = asOf.UTC()
	if err := db.GetContext(
		ctx,
		&rate,
		stmt,
		tenantID,
		baseCurrency,
		baseCurrencyUnitID,
		quoteCurrency,
		quoteCurrencyUnitID,
		asOf,
		asOf,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrExchangeRateNotFound
		}
		return nil, err
	}
	return &rate, nil
}

func (s *Store) ListExchangeRates(ctx context.Context, filter ExchangeRateFilter) ([]ExchangeRate, error) {
	tenantID, err := ValidateTenantID(filter.TenantID)
	if err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		return nil, ErrInvalidLimit
	}
	if filter.Offset < 0 {
		return nil, ErrInvalidOffset
	}
	if filter.BaseCurrency != "" {
		filter.BaseCurrency, err = validateExchangeRateCurrency(filter.BaseCurrency, ErrMissingBaseCurrency)
		if err != nil {
			return nil, err
		}
	}
	if filter.QuoteCurrency != "" {
		filter.QuoteCurrency, err = validateExchangeRateCurrency(filter.QuoteCurrency, ErrMissingQuoteCurrency)
		if err != nil {
			return nil, err
		}
	}
	if filter.BaseCurrency != "" && filter.BaseCurrency == filter.QuoteCurrency {
		return nil, ErrIdenticalCurrencies
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := `SELECT * FROM exchange_rates WHERE tenant_id = ?`
	args := []any{tenantID}
	if filter.BaseCurrency != "" {
		query += " AND base_currency = ?"
		args = append(args, filter.BaseCurrency)
	}
	if filter.QuoteCurrency != "" {
		query += " AND quote_currency = ?"
		args = append(args, filter.QuoteCurrency)
	}
	if filter.ActiveOnly {
		now := time.Now().UTC()
		query += " AND effective_from <= ? AND (effective_to IS NULL OR effective_to > ?)"
		args = append(args, now, now)
	}
	query += " ORDER BY effective_from DESC LIMIT ? OFFSET ?"
	args = append(args, filter.Limit, filter.Offset)
	stmt := db.Rebind(query)
	var rates []ExchangeRate
	if err := db.SelectContext(ctx, &rates, stmt, args...); err != nil {
		return nil, err
	}
	return rates, nil
}

func (s *Store) CreateExchangeRate(ctx context.Context, rate ExchangeRate) (*ExchangeRate, error) {
	tenantID, err := ValidateTenantID(rate.TenantID)
	if err != nil {
		return nil, err
	}
	rate.BaseCurrency, rate.QuoteCurrency, err = validateExchangeRatePair(rate.BaseCurrency, rate.QuoteCurrency)
	if err != nil {
		return nil, err
	}
	if err := ValidateCurrencyUnitID(rate.BaseCurrencyUnitID); err != nil {
		return nil, err
	}
	if err := ValidateCurrencyUnitID(rate.QuoteCurrencyUnitID); err != nil {
		return nil, err
	}
	if rate.SetByOperatorID <= 0 {
		return nil, ErrMissingOperatorID
	}
	if rate.EffectiveFrom.IsZero() {
		return nil, ErrMissingStartTime
	}
	if rate.EffectiveTo.Valid && !rate.EffectiveTo.Time.After(rate.EffectiveFrom) {
		return nil, ErrInvalidTimeRange
	}
	if rate.BuyRate.Cmp(decimal.Zero) <= 0 || rate.SellRate.Cmp(decimal.Zero) <= 0 {
		return nil, ErrInvalidRate
	}
	if !decimalFitsNumeric(rate.BuyRate, 18, 8) || !decimalFitsNumeric(rate.SellRate, 18, 8) {
		return nil, ErrLegacyRateNotRepresentable
	}
	if rate.Spread.Valid && rate.Spread.Decimal.IsNegative() {
		return nil, ErrInvalidRate
	}
	if rate.Spread.Valid && !decimalFitsNumeric(rate.Spread.Decimal, 8, 4) {
		return nil, ErrSpreadNotRepresentable
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	baseUnit, err := s.getCurrencyUnitIdentity(ctx, rate.BaseCurrency, rate.BaseCurrencyUnitID)
	if err != nil {
		return nil, err
	}
	quoteUnit, err := s.getCurrencyUnitIdentity(ctx, rate.QuoteCurrency, rate.QuoteCurrencyUnitID)
	if err != nil {
		return nil, err
	}
	if !currencyUnitEffectiveAt(baseUnit, rate.EffectiveFrom) || !currencyUnitEffectiveAt(quoteUnit, rate.EffectiveFrom) {
		return nil, ErrCurrencyMismatch
	}
	if _, err := s.GetOperatorIdentityByID(ctx, rate.SetByOperatorID); err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO exchange_rates(
		tenant_id, base_currency, base_currency_unit_version_id,
		quote_currency, quote_currency_unit_version_id, buy_rate, sell_rate, spread, set_by_operator_id,
		effective_from, effective_to, created_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored ExchangeRate
	if err := tx.GetContext(ctx, &stored, stmt,
		tenantID,
		rate.BaseCurrency,
		rate.BaseCurrencyUnitID,
		rate.QuoteCurrency,
		rate.QuoteCurrencyUnitID,
		rate.BuyRate,
		rate.SellRate,
		rate.Spread,
		rate.SetByOperatorID,
		rate.EffectiveFrom,
		rate.EffectiveTo,
		now,
	); err != nil {
		return nil, err
	}
	if !stored.BuyRate.Equal(rate.BuyRate) || !stored.SellRate.Equal(rate.SellRate) {
		return nil, ErrLegacyRateNotRepresentable
	}
	if stored.Spread.Valid != rate.Spread.Valid ||
		(stored.Spread.Valid && !stored.Spread.Decimal.Equal(rate.Spread.Decimal)) {
		return nil, ErrSpreadNotRepresentable
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &stored, nil
}

func validateExchangeRatePair(baseCurrency, quoteCurrency string) (string, string, error) {
	baseCurrency, err := validateExchangeRateCurrency(baseCurrency, ErrMissingBaseCurrency)
	if err != nil {
		return "", "", err
	}
	quoteCurrency, err = validateExchangeRateCurrency(quoteCurrency, ErrMissingQuoteCurrency)
	if err != nil {
		return "", "", err
	}
	if baseCurrency == quoteCurrency {
		return "", "", ErrIdenticalCurrencies
	}
	return baseCurrency, quoteCurrency, nil
}

func validateExchangeRateCurrency(currency string, missingError error) (string, error) {
	if currency == "" {
		return "", missingError
	}
	return ValidateCurrencyCode(currency)
}

func currencyUnitEffectiveAt(unit *CurrencyUnitVersion, instant time.Time) bool {
	if unit == nil || instant.IsZero() {
		return false
	}
	year, month, day := instant.UTC().Date()
	asOfDate := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	validFromYear, validFromMonth, validFromDay := unit.ValidFrom.Date()
	validFromDate := time.Date(validFromYear, validFromMonth, validFromDay, 0, 0, 0, 0, time.UTC)
	if asOfDate.Before(validFromDate) {
		return false
	}
	if !unit.ValidTo.Valid {
		return true
	}
	validToYear, validToMonth, validToDay := unit.ValidTo.Time.Date()
	validToDate := time.Date(validToYear, validToMonth, validToDay, 0, 0, 0, 0, time.UTC)
	return asOfDate.Before(validToDate)
}
