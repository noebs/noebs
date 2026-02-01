package store

import (
	"context"
	"database/sql"
)

func (s *Store) GetLimits(ctx context.Context, tenantID, kycTier, txType, currency string) (*TransactionLimit, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if kycTier == "" {
		return nil, ErrMissingKYCTier
	}
	if txType == "" {
		return nil, ErrMissingTransactionType
	}
	if currency == "" {
		return nil, ErrMissingCurrency
	}
	if _, err := s.ensureDB(); err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind(`SELECT * FROM transaction_limits
		WHERE tenant_id = ? AND kyc_tier = ? AND transaction_type = ? AND currency = ? AND is_active = TRUE
		LIMIT 1`)
	var limit TransactionLimit
	if err := s.DB.GetContext(ctx, &limit, stmt, tenantID, kycTier, txType, currency); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrTransactionLimitNotFound
		}
		return nil, err
	}
	return &limit, nil
}
