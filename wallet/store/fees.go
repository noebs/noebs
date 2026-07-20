package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) GetFeeConfigForAmount(ctx context.Context, tenantID, txType, currency string, amount int64) (*FeeConfig, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if txType == "" {
		return nil, ErrMissingTransactionType
	}
	if currency == "" {
		return nil, ErrMissingCurrency
	}
	if amount <= 0 {
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
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := `SELECT * FROM fee_configs WHERE tenant_id = ?`
	args := []any{tenantID}
	if filter.TransactionType != "" {
		query += " AND transaction_type = ?"
		args = append(args, filter.TransactionType)
	}
	if filter.Currency != "" {
		query += " AND currency = ?"
		args = append(args, filter.Currency)
	}
	if filter.ActiveOnly {
		query += " AND is_active = TRUE"
	}
	query += " ORDER BY transaction_type, currency, tier_min ASC LIMIT ? OFFSET ?"
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
	if cfg.Currency == "" {
		return nil, ErrMissingCurrency
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
	if _, err := s.GetOperatorIdentityByID(ctx, cfg.CreatedByOperatorID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO fee_configs(
		tenant_id, transaction_type, currency, tier_min, tier_max, percentage_fee,
		flat_fee, min_fee, max_fee, fee_account_code, is_active, created_by_operator_id, created_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored FeeConfig
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
		cfg.TransactionType,
		cfg.Currency,
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
	return &stored, nil
}
