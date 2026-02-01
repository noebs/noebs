package store

import (
	"context"
	"database/sql"
)

func (s *Store) GetFeeConfigForAmount(ctx context.Context, tenantID, txType, currency string, amount int64) (*FeeConfig, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if txType == "" {
		return nil, ErrMissingTransactionType
	}
	if currency == "" {
		return nil, ErrMissingCurrency
	}
	if amount < 0 {
		return nil, ErrInvalidAmount
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM fee_configs
		WHERE tenant_id = ? AND transaction_type = ? AND currency = ? AND is_active = TRUE
		AND tier_min <= ? AND (tier_max IS NULL OR tier_max >= ?)
		ORDER BY tier_min DESC
		LIMIT 1`)
	var cfg FeeConfig
	if err := db.GetContext(ctx, &cfg, stmt, tenantID, txType, currency, amount, amount); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrFeeConfigNotFound
		}
		return nil, err
	}
	return &cfg, nil
}
