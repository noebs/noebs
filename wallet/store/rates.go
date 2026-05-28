package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/shopspring/decimal"
)

func (s *Store) GetActiveRate(ctx context.Context, tenantID, baseCurrency, quoteCurrency string) (*ExchangeRate, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if baseCurrency == "" {
		return nil, ErrMissingBaseCurrency
	}
	if quoteCurrency == "" {
		return nil, ErrMissingQuoteCurrency
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM exchange_rates
		WHERE tenant_id = ? AND base_currency = ? AND quote_currency = ?
		AND (effective_to IS NULL OR effective_to > ?)
		ORDER BY effective_from DESC
		LIMIT 1`)
	var rate ExchangeRate
	now := time.Now().UTC()
	if err := db.GetContext(ctx, &rate, stmt, tenantID, baseCurrency, quoteCurrency, now); err != nil {
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
		query += " AND (effective_to IS NULL OR effective_to > ?)"
		args = append(args, time.Now().UTC())
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
	if rate.BaseCurrency == "" {
		return nil, ErrMissingBaseCurrency
	}
	if rate.QuoteCurrency == "" {
		return nil, ErrMissingQuoteCurrency
	}
	if rate.SetBy == "" {
		return nil, ErrMissingSetBy
	}
	if rate.EffectiveFrom.IsZero() {
		return nil, ErrMissingStartTime
	}
	if rate.BuyRate.Cmp(decimal.Zero) <= 0 || rate.SellRate.Cmp(decimal.Zero) <= 0 {
		return nil, ErrInvalidRate
	}
	if rate.Spread.Valid && rate.Spread.Decimal.IsNegative() {
		return nil, ErrInvalidRate
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO exchange_rates(
		tenant_id, base_currency, quote_currency, buy_rate, sell_rate, spread, set_by,
		effective_from, effective_to, created_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored ExchangeRate
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
		rate.BaseCurrency,
		rate.QuoteCurrency,
		rate.BuyRate,
		rate.SellRate,
		rate.Spread,
		rate.SetBy,
		rate.EffectiveFrom,
		rate.EffectiveTo,
		now,
	); err != nil {
		return nil, err
	}
	return &stored, nil
}
