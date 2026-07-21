package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) GetFeeConfigForAmount(ctx context.Context, tenantID, txType, currency string, currencyUnitID, amount int64) (*FeeConfig, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
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
	if amount <= 0 {
		return nil, ErrInvalidAmount
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if err := s.validateCurrencyUnitIdentity(ctx, currency, currencyUnitID); err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM fee_configs
		WHERE tenant_id = ? AND transaction_type = ? AND currency = ?
		AND currency_unit_version_id = ? AND is_active = TRUE
		AND tier_min <= ? AND (tier_max IS NULL OR tier_max >= ?)
		ORDER BY tier_min DESC
		LIMIT 1`)
	var cfg FeeConfig
	if err := db.GetContext(ctx, &cfg, stmt, tenantID, txType, currency, currencyUnitID, amount, amount); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrFeeConfigNotFound
		}
		return nil, err
	}
	return &cfg, nil
}

func (s *Store) ListFeeConfigs(ctx context.Context, filter FeeConfigFilter) ([]FeeConfig, error) {
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
	query := `SELECT * FROM fee_configs WHERE tenant_id = ?`
	args := []any{tenantID}
	if filter.TransactionType != "" {
		query += " AND transaction_type = ?"
		args = append(args, filter.TransactionType)
	}
	if filter.Currency != "" {
		filter.Currency, err = ValidateCurrencyCode(filter.Currency)
		if err != nil {
			return nil, err
		}
		query += " AND currency = ?"
		args = append(args, filter.Currency)
	}
	if filter.CurrencyUnitID != 0 {
		if err := ValidateCurrencyUnitID(filter.CurrencyUnitID); err != nil {
			return nil, err
		}
	}
	if filter.CurrencyUnitID > 0 {
		query += " AND currency_unit_version_id = ?"
		args = append(args, filter.CurrencyUnitID)
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if filter.Currency != "" && filter.CurrencyUnitID > 0 {
		if err := s.validateCurrencyUnitIdentity(ctx, filter.Currency, filter.CurrencyUnitID); err != nil {
			return nil, err
		}
	}
	if filter.ActiveOnly {
		query += " AND is_active = TRUE"
	}
	query += " ORDER BY transaction_type, currency, currency_unit_version_id, tier_min ASC LIMIT ? OFFSET ?"
	args = append(args, filter.Limit, filter.Offset)
	stmt := db.Rebind(query)
	var cfgs []FeeConfig
	if err := db.SelectContext(ctx, &cfgs, stmt, args...); err != nil {
		return nil, err
	}
	return cfgs, nil
}

func (s *Store) CreateFeeConfig(ctx context.Context, cfg FeeConfig) (*FeeConfig, error) {
	tenantID, err := ValidateTenantID(cfg.TenantID)
	if err != nil {
		return nil, err
	}
	if cfg.TransactionType == "" {
		return nil, ErrMissingTransactionType
	}
	cfg.Currency, err = ValidateCurrencyCode(cfg.Currency)
	if err != nil {
		return nil, err
	}
	if err := ValidateCurrencyUnitID(cfg.CurrencyUnitID); err != nil {
		return nil, err
	}
	if cfg.TierMin < 0 {
		return nil, ErrInvalidAmount
	}
	if cfg.TierMax.Valid && cfg.TierMax.Int64 < cfg.TierMin {
		return nil, ErrInvalidAmount
	}
	if cfg.PercentageFee.IsNegative() {
		return nil, ErrInvalidPercentage
	}
	if !decimalFitsNumeric(cfg.PercentageFee, 8, 4) {
		return nil, ErrFeePercentageNotRepresentable
	}
	if cfg.FlatFee < 0 || cfg.MinFee < 0 {
		return nil, ErrInvalidAmount
	}
	if cfg.MaxFee.Valid && cfg.MaxFee.Int64 < cfg.MinFee {
		return nil, ErrInvalidAmount
	}
	if cfg.CreatedByOperatorID <= 0 {
		return nil, ErrMissingOperatorID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if err := s.validateCurrencyUnitIdentity(ctx, cfg.Currency, cfg.CurrencyUnitID); err != nil {
		return nil, err
	}
	if _, err := s.GetOperatorIdentityByID(ctx, cfg.CreatedByOperatorID); err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO fee_configs(
		tenant_id, transaction_type, currency, currency_unit_version_id, tier_min, tier_max, percentage_fee,
		flat_fee, min_fee, max_fee, fee_account_code, is_active, created_by_operator_id, created_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored FeeConfig
	if err := tx.GetContext(ctx, &stored, stmt,
		tenantID,
		cfg.TransactionType,
		cfg.Currency,
		cfg.CurrencyUnitID,
		cfg.TierMin,
		cfg.TierMax,
		cfg.PercentageFee,
		cfg.FlatFee,
		cfg.MinFee,
		cfg.MaxFee,
		cfg.FeeAccountCode,
		cfg.IsActive,
		cfg.CreatedByOperatorID,
		now,
	); err != nil {
		return nil, err
	}
	if !stored.PercentageFee.Equal(cfg.PercentageFee) {
		return nil, ErrFeePercentageNotRepresentable
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &stored, nil
}
