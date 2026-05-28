package store

import (
	"context"
	"database/sql"
)

func (s *Store) GetLimits(ctx context.Context, tenantID, kycTier, txType, currency string) (*TransactionLimit, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
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
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM transaction_limits
		WHERE tenant_id = ? AND kyc_tier = ? AND transaction_type = ? AND currency = ? AND is_active = TRUE
		LIMIT 1`)
	var limit TransactionLimit
	if err := db.GetContext(ctx, &limit, stmt, tenantID, kycTier, txType, currency); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrTransactionLimitNotFound
		}
		return nil, err
	}
	return &limit, nil
}
