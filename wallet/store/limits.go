package store

import (
	"context"
	"database/sql"
)

func (s *Store) GetLimits(ctx context.Context, tenantID, kycTier, txType, currency string, currencyUnitID int64) (*TransactionLimit, error) {
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
	currency, err = ValidateCurrencyCode(currency)
	if err != nil {
		return nil, err
	}
	if err := ValidateCurrencyUnitID(currencyUnitID); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if err := s.validateCurrencyUnitIdentity(ctx, currency, currencyUnitID); err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM transaction_limits
		WHERE tenant_id = ? AND kyc_tier = ? AND transaction_type = ? AND currency = ?
		AND currency_unit_version_id = ? AND is_active = TRUE
		LIMIT 1`)
	var limit TransactionLimit
	if err := db.GetContext(ctx, &limit, stmt, tenantID, kycTier, txType, currency, currencyUnitID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrTransactionLimitNotFound
		}
		return nil, err
	}
	return &limit, nil
}
