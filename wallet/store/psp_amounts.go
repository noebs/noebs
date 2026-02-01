package store

import (
	"context"
	"database/sql"
)

func validatePSPTransactionAmount(amount PSPTransactionAmount) error {
	if amount.TenantID == "" {
		return ErrMissingTenantID
	}
	if amount.PSPTransactionID <= 0 {
		return ErrMissingPSPTransactionID
	}
	if amount.AmountKind == "" {
		return ErrMissingAmountKind
	}
	if !amount.AmountKind.Valid() {
		return ErrInvalidAmountKind
	}
	if amount.Amount <= 0 {
		return ErrInvalidAmount
	}
	if amount.Currency == "" {
		return ErrMissingCurrency
	}
	if amount.FxRate.Valid {
		if !amount.FxBaseCurrency.Valid || !amount.FxQuoteCurrency.Valid {
			return ErrMissingFXCurrency
		}
	}
	if (amount.FxBaseCurrency.Valid || amount.FxQuoteCurrency.Valid) && !amount.FxRate.Valid {
		return ErrMissingFXRate
	}
	return nil
}

func buildPSPTransactionAmount(tenantID string, pspTransactionID int64, input PSPTransactionAmountInput) PSPTransactionAmount {
	return PSPTransactionAmount{
		TenantID:         tenantID,
		PSPTransactionID: pspTransactionID,
		AmountKind:       input.AmountKind,
		Amount:           input.Amount,
		Currency:         input.Currency,
		FxRate:           input.FxRate,
		FxBaseCurrency:   nullString(input.FxBaseCurrency),
		FxQuoteCurrency:  nullString(input.FxQuoteCurrency),
		FxSource:         nullString(input.FxSource),
	}
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func (s *Store) AddPSPTransactionAmount(ctx context.Context, amount PSPTransactionAmount) (*PSPTransactionAmount, error) {
	if err := validatePSPTransactionAmount(amount); err != nil {
		return nil, err
	}

	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	stmt := db.Rebind(`INSERT INTO psp_transaction_amounts(
		tenant_id, psp_transaction_id, amount_kind, amount, currency,
		fx_rate, fx_base_currency, fx_quote_currency, fx_source
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored PSPTransactionAmount
	if err := db.GetContext(ctx, &stored, stmt,
		amount.TenantID,
		amount.PSPTransactionID,
		amount.AmountKind,
		amount.Amount,
		amount.Currency,
		amount.FxRate,
		amount.FxBaseCurrency,
		amount.FxQuoteCurrency,
		amount.FxSource,
	); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *Store) AddPSPTransactionAmounts(ctx context.Context, tenantID string, pspTransactionID int64, amounts []PSPTransactionAmountInput) ([]PSPTransactionAmount, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if pspTransactionID <= 0 {
		return nil, ErrMissingPSPTransactionID
	}
	if len(amounts) == 0 {
		return nil, ErrMissingAmounts
	}
	prepared := make([]PSPTransactionAmount, 0, len(amounts))
	for _, input := range amounts {
		amount := buildPSPTransactionAmount(tenantID, pspTransactionID, input)
		if err := validatePSPTransactionAmount(amount); err != nil {
			return nil, err
		}
		prepared = append(prepared, amount)
	}

	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}

	stmt := db.Rebind(`INSERT INTO psp_transaction_amounts(
		tenant_id, psp_transaction_id, amount_kind, amount, currency,
		fx_rate, fx_base_currency, fx_quote_currency, fx_source
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	stored := make([]PSPTransactionAmount, 0, len(prepared))
	for _, amount := range prepared {
		var row PSPTransactionAmount
		if err := tx.GetContext(ctx, &row, stmt,
			amount.TenantID,
			amount.PSPTransactionID,
			amount.AmountKind,
			amount.Amount,
			amount.Currency,
			amount.FxRate,
			amount.FxBaseCurrency,
			amount.FxQuoteCurrency,
			amount.FxSource,
		); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		stored = append(stored, row)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stored, nil
}

func (s *Store) ListPSPTransactionAmounts(ctx context.Context, tenantID string, pspTransactionID int64) ([]PSPTransactionAmount, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if pspTransactionID <= 0 {
		return nil, ErrMissingPSPTransactionID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM psp_transaction_amounts
		WHERE tenant_id = ? AND psp_transaction_id = ?
		ORDER BY id`)
	var rows []PSPTransactionAmount
	if err := db.SelectContext(ctx, &rows, stmt, tenantID, pspTransactionID); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) ListPSPTransactionAmountsByKind(ctx context.Context, tenantID string, pspTransactionID int64, kind PSPAmountKind) ([]PSPTransactionAmount, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if pspTransactionID <= 0 {
		return nil, ErrMissingPSPTransactionID
	}
	if kind == "" {
		return nil, ErrMissingAmountKind
	}
	if !kind.Valid() {
		return nil, ErrInvalidAmountKind
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM psp_transaction_amounts
		WHERE tenant_id = ? AND psp_transaction_id = ? AND amount_kind = ?
		ORDER BY id`)
	var rows []PSPTransactionAmount
	if err := db.SelectContext(ctx, &rows, stmt, tenantID, pspTransactionID, kind); err != nil {
		if err == sql.ErrNoRows {
			return []PSPTransactionAmount{}, nil
		}
		return nil, err
	}
	return rows, nil
}
