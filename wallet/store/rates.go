package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) GetActiveRate(ctx context.Context, tenantID, baseCurrency, quoteCurrency string) (*ExchangeRate, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
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
