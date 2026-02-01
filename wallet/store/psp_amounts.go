package store

import (
	"context"
	"database/sql"
)

func (s *Store) AddPSPTransactionAmount(ctx context.Context, amount PSPTransactionAmount) (*PSPTransactionAmount, error) {
	if amount.TenantID == "" {
		return nil, ErrMissingTenantID
	}
	if amount.PSPTransactionID <= 0 {
		return nil, ErrMissingPSPTransactionID
	}
	if amount.AmountKind == "" {
		return nil, ErrMissingAmountKind
	}
	if !amount.AmountKind.Valid() {
		return nil, ErrInvalidAmountKind
	}
	if amount.Amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if amount.Currency == "" {
		return nil, ErrMissingCurrency
	}
	if amount.FxRate.Valid {
		if !amount.FxBaseCurrency.Valid || !amount.FxQuoteCurrency.Valid {
			return nil, ErrMissingFXCurrency
		}
	}
	if (amount.FxBaseCurrency.Valid || amount.FxQuoteCurrency.Valid) && !amount.FxRate.Valid {
		return nil, ErrMissingFXRate
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
