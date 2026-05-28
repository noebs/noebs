package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type WalletLedgerEntryFilter struct {
	TenantID  string
	WalletID  uuid.UUID
	EntryType string
	Limit     int
	Offset    int
}

type WalletLedgerEntry struct {
	ID             int64          `db:"id"`
	TenantID       string         `db:"tenant_id"`
	TransactionID  int64          `db:"transaction_id"`
	WalletID       uuid.UUID      `db:"wallet_id"`
	EntryType      string         `db:"entry_type"`
	Amount         int64          `db:"amount"`
	Currency       string         `db:"currency"`
	BalanceAfter   int64          `db:"balance_after"`
	WalletSequence int64          `db:"wallet_sequence"`
	Status         string         `db:"status"`
	ReferenceType  string         `db:"reference_type"`
	ReferenceID    sql.NullString `db:"reference_id"`
	Description    sql.NullString `db:"description"`
	Metadata       RawJSON        `db:"metadata"`
	CreatedAt      time.Time      `db:"created_at"`
}

func (s *Store) ListWalletLedgerEntries(ctx context.Context, filter WalletLedgerEntryFilter) ([]WalletLedgerEntry, error) {
	tenantID, err := ValidateTenantID(filter.TenantID)
	if err != nil {
		return nil, err
	}
	if filter.WalletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	if filter.EntryType != "" && filter.EntryType != "debit" && filter.EntryType != "credit" {
		return nil, ErrInvalidDirection
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
	query := `SELECT
			le.id,
			le.tenant_id,
			le.transaction_id,
			le.wallet_id,
			le.entry_type,
			le.amount,
			le.currency,
			le.balance_after,
			le.wallet_sequence,
			le.status,
			lt.reference_type,
			lt.reference_id,
			le.description,
			le.metadata,
			le.created_at
		FROM ledger_entries le
		JOIN ledger_transactions lt ON lt.tenant_id = le.tenant_id AND lt.id = le.transaction_id
		WHERE le.tenant_id = ? AND le.wallet_id = ?`
	args := []any{tenantID, filter.WalletID}
	if filter.EntryType != "" {
		query += " AND le.entry_type = ?"
		args = append(args, filter.EntryType)
	}
	query += " ORDER BY le.wallet_sequence DESC LIMIT ? OFFSET ?"
	args = append(args, filter.Limit, filter.Offset)
	stmt := db.Rebind(query)
	var rows []WalletLedgerEntry
	if err := db.SelectContext(ctx, &rows, stmt, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) LedgerTransactionExistsByReference(ctx context.Context, tenantID, referenceType, referenceID string) (bool, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return false, err
	}
	if referenceType == "" {
		return false, ErrMissingReferenceType
	}
	if referenceID == "" {
		return false, ErrMissingReferenceID
	}
	db, err := s.ensureDB()
	if err != nil {
		return false, err
	}
	stmt := db.Rebind("SELECT 1 FROM ledger_transactions WHERE tenant_id = ? AND reference_type = ? AND reference_id = ? LIMIT 1")
	var exists int
	if err := db.GetContext(ctx, &exists, stmt, tenantID, referenceType, referenceID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
