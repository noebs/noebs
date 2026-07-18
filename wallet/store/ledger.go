package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type DoubleEntryParams struct {
	TenantID       string
	IdempotencyKey string
	Currency       string
	ReferenceType  string
	ReferenceID    string
	DebitWalletID  uuid.UUID
	CreditWalletID uuid.UUID
	Amount         int64
	Description    string
	Metadata       json.RawMessage
}

type DoubleEntryResult struct {
	TransactionID int64
	DebitEntry    *LedgerEntry
	CreditEntry   *LedgerEntry
	DebitWallet   *Wallet
	CreditWallet  *Wallet
	Existing      bool
}

type HeldDoubleEntryParams struct {
	HoldID int64
	Entry  DoubleEntryParams
}

type doubleEntryMode struct {
	DebitHoldID               int64
	AllowSystemDebitOverdraft bool
}

func (s *Store) PostDoubleEntry(ctx context.Context, params DoubleEntryParams) (*DoubleEntryResult, error) {
	if err := ValidateDoubleEntryParams(params); err != nil {
		return nil, err
	}
	return s.postDoubleEntry(ctx, params, doubleEntryMode{})
}

func (s *Store) PostHeldDoubleEntry(ctx context.Context, params HeldDoubleEntryParams) (*DoubleEntryResult, error) {
	if err := ValidateHeldDoubleEntryParams(params); err != nil {
		return nil, err
	}
	return s.postDoubleEntry(ctx, params.Entry, doubleEntryMode{DebitHoldID: params.HoldID})
}

func (s *Store) PostSystemDebitDoubleEntry(ctx context.Context, params DoubleEntryParams) (*DoubleEntryResult, error) {
	if err := ValidateDoubleEntryParams(params); err != nil {
		return nil, err
	}
	return s.postDoubleEntry(ctx, params, doubleEntryMode{AllowSystemDebitOverdraft: true})
}

func (s *Store) postDoubleEntry(ctx context.Context, params DoubleEntryParams, mode doubleEntryMode) (result *DoubleEntryResult, err error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	refID := sql.NullString{String: params.ReferenceID, Valid: params.ReferenceID != ""}

	var txID int64
	insertTx := db.Rebind(`INSERT INTO ledger_transactions(
		tenant_id, idempotency_key, currency, reference_type, reference_id, status, metadata, created_at
	) VALUES(?, ?, ?, ?, ?, 'completed', ?, ?)
	ON CONFLICT(tenant_id, idempotency_key) DO NOTHING
	RETURNING id`)
	err = tx.GetContext(ctx, &txID, insertTx,
		params.TenantID,
		params.IdempotencyKey,
		params.Currency,
		params.ReferenceType,
		refID,
		params.Metadata,
		now,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			existing, err := s.loadExistingEntries(ctx, tx, params)
			if err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return existing, nil
		}
		return nil, err
	}

	var debitHold *BalanceHold
	if mode.DebitHoldID > 0 {
		debitHold, err = s.lockHold(ctx, tx, params.TenantID, mode.DebitHoldID)
		if err != nil {
			return nil, err
		}
		if err := validateDebitHold(debitHold, params); err != nil {
			return nil, err
		}
	}

	wallets, err := s.lockWallets(ctx, tx, params.TenantID, params.DebitWalletID, params.CreditWalletID)
	if err != nil {
		return nil, err
	}
	debitWallet := wallets[params.DebitWalletID]
	creditWallet := wallets[params.CreditWalletID]
	if err := validateDoubleEntryWalletTargets(debitWallet, creditWallet, params, mode); err != nil {
		return nil, err
	}
	if debitHold == nil && !mode.AllowSystemDebitOverdraft && debitWallet.AvailableBalance < params.Amount {
		return nil, ErrInsufficientFunds
	}
	if debitHold != nil && debitWallet.Balance < params.Amount {
		return nil, ErrInsufficientFunds
	}

	debitWallet.Balance -= params.Amount
	if debitHold == nil {
		debitWallet.AvailableBalance -= params.Amount
	}
	creditWallet.Balance += params.Amount
	creditWallet.AvailableBalance += params.Amount

	updateWallet := db.Rebind(`UPDATE wallets
		SET balance = ?, available_balance = ?, version = version + 1, updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	if _, err := tx.ExecContext(ctx, updateWallet, debitWallet.Balance, debitWallet.AvailableBalance, now, params.TenantID, debitWallet.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, updateWallet, creditWallet.Balance, creditWallet.AvailableBalance, now, params.TenantID, creditWallet.ID); err != nil {
		return nil, err
	}

	debitSeq, err := s.nextWalletSequence(ctx, tx, params.TenantID, debitWallet.ID)
	if err != nil {
		return nil, err
	}
	creditSeq, err := s.nextWalletSequence(ctx, tx, params.TenantID, creditWallet.ID)
	if err != nil {
		return nil, err
	}

	debitEntry, err := s.insertEntry(ctx, tx, LedgerEntry{
		TenantID:      params.TenantID,
		TransactionID: txID,
		WalletID:      debitWallet.ID,
		EntryType:     "debit",
		Amount:        params.Amount,
		Currency:      params.Currency,
		BalanceAfter:  debitWallet.Balance,
		WalletSeq:     debitSeq,
		Status:        "completed",
		Description:   sql.NullString{String: params.Description, Valid: params.Description != ""},
		Metadata:      RawJSON(params.Metadata),
		CreatedAt:     now,
	})
	if err != nil {
		return nil, err
	}

	creditEntry, err := s.insertEntry(ctx, tx, LedgerEntry{
		TenantID:      params.TenantID,
		TransactionID: txID,
		WalletID:      creditWallet.ID,
		EntryType:     "credit",
		Amount:        params.Amount,
		Currency:      params.Currency,
		BalanceAfter:  creditWallet.Balance,
		WalletSeq:     creditSeq,
		Status:        "completed",
		Description:   sql.NullString{String: params.Description, Valid: params.Description != ""},
		Metadata:      RawJSON(params.Metadata),
		CreatedAt:     now,
	})
	if err != nil {
		return nil, err
	}

	if err := s.linkCounterEntries(ctx, tx, debitEntry.ID, creditEntry.ID); err != nil {
		return nil, err
	}

	if debitHold != nil {
		if err := s.captureDebitHold(ctx, tx, params.TenantID, debitHold, params.Amount, now); err != nil {
			return nil, err
		}
	}

	result = &DoubleEntryResult{
		TransactionID: txID,
		DebitEntry:    debitEntry,
		CreditEntry:   creditEntry,
		DebitWallet:   debitWallet,
		CreditWallet:  creditWallet,
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func validateDebitHold(hold *BalanceHold, params DoubleEntryParams) error {
	if hold.Status != HoldStatusActive {
		return ErrHoldNotActive
	}
	if hold.WalletID != params.DebitWalletID {
		return ErrHoldWalletMismatch
	}
	if hold.AmountRemaining < params.Amount {
		return ErrHoldAmountExceeded
	}
	return nil
}

func validateDoubleEntryWalletTargets(debitWallet, creditWallet *Wallet, params DoubleEntryParams, mode doubleEntryMode) error {
	if debitWallet == nil ||
		debitWallet.TenantID != params.TenantID ||
		debitWallet.ID != params.DebitWalletID ||
		creditWallet == nil ||
		creditWallet.TenantID != params.TenantID ||
		creditWallet.ID != params.CreditWalletID {
		return ErrWalletNotFound
	}
	if debitWallet.Status != WalletStatusActive || creditWallet.Status != WalletStatusActive {
		return ErrWalletInactive
	}
	if debitWallet.Currency != params.Currency || creditWallet.Currency != params.Currency {
		return ErrCurrencyMismatch
	}
	if mode.AllowSystemDebitOverdraft && debitWallet.OwnerType != OwnerTypeSystem {
		return ErrSystemDebitWalletRequired
	}
	return nil
}

func (s *Store) captureDebitHold(ctx context.Context, tx *sqlx.Tx, tenantID string, hold *BalanceHold, amount int64, now time.Time) error {
	remaining := hold.AmountRemaining - amount
	if remaining == 0 {
		stmt := s.DB.Rebind(`UPDATE balance_holds
			SET amount_remaining = 0, status = ?, captured_at = ?
			WHERE tenant_id = ? AND id = ?`)
		_, err := tx.ExecContext(ctx, stmt, HoldStatusCaptured, now, tenantID, hold.ID)
		return err
	}

	stmt := s.DB.Rebind(`UPDATE balance_holds
		SET amount_remaining = ?
		WHERE tenant_id = ? AND id = ?`)
	_, err := tx.ExecContext(ctx, stmt, remaining, tenantID, hold.ID)
	return err
}

func (s *Store) LedgerTransactionExists(ctx context.Context, tenantID, idempotencyKey string) (bool, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return false, err
	}
	if idempotencyKey == "" {
		return false, ErrMissingIdempotencyKey
	}
	db, err := s.ensureDB()
	if err != nil {
		return false, err
	}
	stmt := db.Rebind("SELECT 1 FROM ledger_transactions WHERE tenant_id = ? AND idempotency_key = ?")
	var exists int
	if err := db.GetContext(ctx, &exists, stmt, tenantID, idempotencyKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) loadExistingEntries(ctx context.Context, tx *sqlx.Tx, params DoubleEntryParams) (*DoubleEntryResult, error) {
	stmt := s.DB.Rebind("SELECT * FROM ledger_transactions WHERE tenant_id = ? AND idempotency_key = ?")
	var txn LedgerTransaction
	if err := tx.GetContext(ctx, &txn, stmt, params.TenantID, params.IdempotencyKey); err != nil {
		return nil, err
	}
	entries, err := s.listEntriesByTransaction(ctx, tx, params.TenantID, txn.ID)
	if err != nil {
		return nil, err
	}
	result := &DoubleEntryResult{TransactionID: txn.ID, Existing: true}
	for _, entry := range entries {
		if entry.EntryType == "debit" {
			result.DebitEntry = entry
		} else if entry.EntryType == "credit" {
			result.CreditEntry = entry
		}
	}
	if !existingDoubleEntryMatches(txn, result, params) {
		return nil, ErrDuplicateTransaction
	}
	return result, nil
}

func existingDoubleEntryMatches(txn LedgerTransaction, result *DoubleEntryResult, params DoubleEntryParams) bool {
	if txn.Currency != params.Currency || txn.ReferenceType != params.ReferenceType {
		return false
	}
	if txn.IdempotencyKey != params.IdempotencyKey || txn.Status != "completed" || !rawJSONMatches(txn.Metadata, params.Metadata) {
		return false
	}
	if txn.ReferenceID.Valid != (params.ReferenceID != "") {
		return false
	}
	if txn.ReferenceID.Valid && txn.ReferenceID.String != params.ReferenceID {
		return false
	}
	if result == nil || result.DebitEntry == nil || result.CreditEntry == nil {
		return false
	}
	return result.DebitEntry.WalletID == params.DebitWalletID &&
		result.CreditEntry.WalletID == params.CreditWalletID &&
		result.DebitEntry.Amount == params.Amount &&
		result.CreditEntry.Amount == params.Amount &&
		result.DebitEntry.Currency == params.Currency &&
		result.CreditEntry.Currency == params.Currency &&
		result.DebitEntry.EntryType == "debit" &&
		result.CreditEntry.EntryType == "credit" &&
		result.DebitEntry.Status == "completed" &&
		result.CreditEntry.Status == "completed" &&
		ledgerEntryDescriptionMatches(result.DebitEntry, params.Description) &&
		ledgerEntryDescriptionMatches(result.CreditEntry, params.Description) &&
		rawJSONMatches(result.DebitEntry.Metadata, params.Metadata) &&
		rawJSONMatches(result.CreditEntry.Metadata, params.Metadata)
}

func ledgerEntryDescriptionMatches(entry *LedgerEntry, description string) bool {
	if entry == nil {
		return false
	}
	if description == "" {
		return !entry.Description.Valid || entry.Description.String == ""
	}
	return entry.Description.Valid && entry.Description.String == description
}

func (s *Store) listEntriesByTransaction(ctx context.Context, tx *sqlx.Tx, tenantID string, txID int64) ([]*LedgerEntry, error) {
	stmt := s.DB.Rebind("SELECT * FROM ledger_entries WHERE tenant_id = ? AND transaction_id = ? ORDER BY id")
	var entries []*LedgerEntry
	if err := tx.SelectContext(ctx, &entries, stmt, tenantID, txID); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) lockWallets(ctx context.Context, tx *sqlx.Tx, tenantID string, debitID, creditID uuid.UUID) (map[uuid.UUID]*Wallet, error) {
	stmt := s.DB.Rebind("SELECT * FROM wallets WHERE tenant_id = ? AND id IN (?, ?) ORDER BY id FOR UPDATE")
	var rows []Wallet
	if err := tx.SelectContext(ctx, &rows, stmt, tenantID, debitID, creditID); err != nil {
		return nil, err
	}
	if len(rows) != 2 {
		return nil, ErrWalletNotFound
	}
	result := make(map[uuid.UUID]*Wallet, 2)
	for i := range rows {
		w := rows[i]
		result[w.ID] = &w
	}
	if result[debitID] == nil || result[creditID] == nil {
		return nil, ErrWalletNotFound
	}
	return result, nil
}

func (s *Store) nextWalletSequence(ctx context.Context, tx *sqlx.Tx, tenantID string, walletID uuid.UUID) (int64, error) {
	stmt := s.DB.Rebind("SELECT COALESCE(MAX(wallet_sequence), 0) + 1 FROM ledger_entries WHERE tenant_id = ? AND wallet_id = ?")
	var seq int64
	if err := tx.GetContext(ctx, &seq, stmt, tenantID, walletID); err != nil {
		return 0, err
	}
	return seq, nil
}

func (s *Store) insertEntry(ctx context.Context, tx *sqlx.Tx, entry LedgerEntry) (*LedgerEntry, error) {
	stmt := s.DB.Rebind(`INSERT INTO ledger_entries(
		tenant_id, transaction_id, wallet_id, entry_type, amount, currency,
		balance_after, wallet_sequence, status, description, metadata, created_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored LedgerEntry
	if err := tx.GetContext(ctx, &stored, stmt,
		entry.TenantID,
		entry.TransactionID,
		entry.WalletID,
		entry.EntryType,
		entry.Amount,
		entry.Currency,
		entry.BalanceAfter,
		entry.WalletSeq,
		entry.Status,
		entry.Description,
		entry.Metadata,
		entry.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *Store) linkCounterEntries(ctx context.Context, tx *sqlx.Tx, debitID, creditID int64) error {
	stmt := s.DB.Rebind("UPDATE ledger_entries SET counter_entry_id = ? WHERE id = ?")
	if _, err := tx.ExecContext(ctx, stmt, creditID, debitID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, stmt, debitID, creditID); err != nil {
		return err
	}
	return nil
}
