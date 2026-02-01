package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

func (s *Store) GetDailyUsage(ctx context.Context, tenantID string, walletID uuid.UUID, txType string) (int64, error) {
	if tenantID == "" {
		return 0, ErrMissingTenantID
	}
	if walletID == uuid.Nil {
		return 0, ErrMissingWalletID
	}
	if txType == "" {
		return 0, ErrMissingTransactionType
	}
	db, err := s.ensureDB()
	if err != nil {
		return 0, err
	}
	start := time.Now().UTC().Truncate(24 * time.Hour)
	stmt := db.Rebind(`SELECT COALESCE(SUM(le.amount), 0)
		FROM ledger_entries le
		JOIN ledger_transactions lt ON lt.id = le.transaction_id AND lt.tenant_id = le.tenant_id
		WHERE le.tenant_id = ? AND le.wallet_id = ? AND le.entry_type = 'debit'
		AND lt.reference_type = ? AND le.status = 'completed' AND lt.status = 'completed'
		AND le.created_at >= ?`)
	var total int64
	if err := db.GetContext(ctx, &total, stmt, tenantID, walletID, txType, start); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Store) GetMonthlyUsage(ctx context.Context, tenantID string, walletID uuid.UUID, txType string) (int64, error) {
	if tenantID == "" {
		return 0, ErrMissingTenantID
	}
	if walletID == uuid.Nil {
		return 0, ErrMissingWalletID
	}
	if txType == "" {
		return 0, ErrMissingTransactionType
	}
	db, err := s.ensureDB()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	stmt := db.Rebind(`SELECT COALESCE(SUM(le.amount), 0)
		FROM ledger_entries le
		JOIN ledger_transactions lt ON lt.id = le.transaction_id AND lt.tenant_id = le.tenant_id
		WHERE le.tenant_id = ? AND le.wallet_id = ? AND le.entry_type = 'debit'
		AND lt.reference_type = ? AND le.status = 'completed' AND lt.status = 'completed'
		AND le.created_at >= ?`)
	var total int64
	if err := db.GetContext(ctx, &total, stmt, tenantID, walletID, txType, start); err != nil {
		return 0, err
	}
	return total, nil
}
