package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

type HoldParams struct {
	TenantID       string
	WalletID       uuid.UUID
	Amount         int64
	Reason         string
	ReferenceType  string
	ReferenceID    string
	IdempotencyKey string
	ExpiresAt      time.Time
	Metadata       json.RawMessage
}

func (s *Store) CreateHold(ctx context.Context, params HoldParams) (*BalanceHold, error) {
	if err := ValidateHoldParams(params); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	now, err := holdTransactionTime(ctx, tx)
	if err != nil {
		return nil, err
	}
	insertStmt := db.Rebind(`INSERT INTO balance_holds(
		tenant_id, wallet_id, amount, amount_remaining, reason, reference_type, reference_id,
		idempotency_key, status, expires_at, created_at, metadata
	) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
	WHERE ? > clock_timestamp()
	ON CONFLICT(tenant_id, wallet_id, reference_type, reference_id) DO NOTHING
	RETURNING *`)
	var hold BalanceHold
	err = tx.GetContext(ctx, &hold, insertStmt,
		params.TenantID,
		params.WalletID,
		params.Amount,
		params.Amount,
		params.Reason,
		params.ReferenceType,
		params.ReferenceID,
		params.IdempotencyKey,
		HoldStatusActive,
		params.ExpiresAt,
		now,
		params.Metadata,
		params.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			hold, err = s.loadExistingHold(ctx, tx, params)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, ErrHoldExpired
				}
				return nil, err
			}
			if !existingHoldCreateMatches(hold, params) {
				return nil, ErrDuplicateHold
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			committed = true
			return &hold, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "balance_holds_wallet_id_fkey" {
			return nil, ErrWalletNotFound
		}
		return nil, err
	}
	walletRow, err := s.lockWallet(ctx, tx, params.TenantID, params.WalletID)
	if err != nil {
		return nil, err
	}
	now, err = holdTransactionTime(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !now.Before(params.ExpiresAt) {
		return nil, ErrHoldExpired
	}
	if err := validateHoldWalletTarget(walletRow, params); err != nil {
		return nil, err
	}
	if walletRow.AvailableBalance < params.Amount {
		return nil, ErrInsufficientFunds
	}

	available, err := checkedSubtractInt64(walletRow.AvailableBalance, params.Amount)
	if err != nil {
		return nil, err
	}
	walletRow.AvailableBalance = available
	if err := s.updateWalletBalance(ctx, tx, params.TenantID, walletRow.ID, walletRow.Balance, walletRow.AvailableBalance, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return &hold, nil
}

func (s *Store) ReleaseHold(ctx context.Context, tenantID string, holdID int64) error {
	if err := ValidateReleaseHold(tenantID, holdID); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	hold, err := s.lockHold(ctx, tx, tenantID, holdID)
	if err != nil {
		return err
	}
	if hold.Status != HoldStatusActive && hold.Status != HoldStatusCommitted {
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}
	now, err := holdTransactionTime(ctx, tx)
	if err != nil {
		return err
	}
	if hold.Status == HoldStatusActive && !now.Before(hold.ExpiresAt) {
		if err := s.expireLockedHold(ctx, tx, hold, now); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}

	walletRow, err := s.lockWallet(ctx, tx, tenantID, hold.WalletID)
	if err != nil {
		return err
	}

	available, err := checkedAddInt64(walletRow.AvailableBalance, hold.AmountRemaining)
	if err != nil {
		return err
	}
	walletRow.AvailableBalance = available
	if err := s.updateWalletBalance(ctx, tx, tenantID, walletRow.ID, walletRow.Balance, walletRow.AvailableBalance, now); err != nil {
		return err
	}

	updateStmt := db.Rebind(`UPDATE balance_holds
		SET status = ?, amount_remaining = 0, released_at = ?
		WHERE tenant_id = ? AND id = ?`)
	if _, err := tx.ExecContext(ctx, updateStmt, HoldStatusReleased, now, tenantID, hold.ID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// CommitHold turns an expiring reservation into a non-expiring obligation.
// Callers use it immediately before an irreversible external side effect.
func (s *Store) CommitHold(ctx context.Context, tenantID string, holdID int64) error {
	if err := ValidateReleaseHold(tenantID, holdID); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	hold, err := s.lockHold(ctx, tx, tenantID, holdID)
	if err != nil {
		return err
	}
	if hold.Status == HoldStatusCommitted {
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}
	if hold.Status != HoldStatusActive {
		return ErrHoldNotActive
	}

	now, err := holdTransactionTime(ctx, tx)
	if err != nil {
		return err
	}
	if !now.Before(hold.ExpiresAt) {
		if err := s.expireLockedHold(ctx, tx, hold, now); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return ErrHoldExpired
	}

	stmt := db.Rebind(`UPDATE balance_holds
		SET status = ?, committed_at = ?
		WHERE tenant_id = ? AND id = ?`)
	if _, err := tx.ExecContext(ctx, stmt, HoldStatusCommitted, now, tenantID, hold.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) ExpireHolds(ctx context.Context, tenantID string, limit int) (int, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return 0, err
	}
	if limit <= 0 {
		return 0, ErrInvalidLimit
	}
	db, err := s.ensureDB()
	if err != nil {
		return 0, err
	}

	expired := 0
	for expired < limit {
		found, err := s.expireNextHold(ctx, db, tenantID)
		if err != nil {
			return expired, err
		}
		if !found {
			break
		}
		expired++
	}
	return expired, nil
}

func (s *Store) expireNextHold(ctx context.Context, db *sqlx.DB, tenantID string) (bool, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt := db.Rebind(`SELECT * FROM balance_holds
		WHERE tenant_id = ? AND status = ? AND expires_at <= clock_timestamp()
		ORDER BY expires_at, id
		LIMIT 1
		FOR UPDATE SKIP LOCKED`)
	var hold BalanceHold
	if err := tx.GetContext(ctx, &hold, stmt, tenantID, HoldStatusActive); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	now, err := holdTransactionTime(ctx, tx)
	if err != nil {
		return false, err
	}
	if err := s.expireLockedHold(ctx, tx, &hold, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) expireLockedHold(ctx context.Context, tx *sqlx.Tx, hold *BalanceHold, now time.Time) error {
	walletRow, err := s.lockWallet(ctx, tx, hold.TenantID, hold.WalletID)
	if err != nil {
		return err
	}
	available, err := checkedAddInt64(walletRow.AvailableBalance, hold.AmountRemaining)
	if err != nil {
		return err
	}
	walletRow.AvailableBalance = available
	if err := s.updateWalletBalance(ctx, tx, hold.TenantID, walletRow.ID, walletRow.Balance, walletRow.AvailableBalance, now); err != nil {
		return err
	}

	stmt := s.DB.Rebind(`UPDATE balance_holds
		SET status = ?, amount_remaining = 0, expired_at = ?
		WHERE tenant_id = ? AND id = ?`)
	_, err = tx.ExecContext(ctx, stmt, HoldStatusExpired, now, hold.TenantID, hold.ID)
	return err
}

func holdTransactionTime(ctx context.Context, tx *sqlx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.GetContext(ctx, &now, `SELECT clock_timestamp()`); err != nil {
		return time.Time{}, err
	}
	return now.UTC(), nil
}

func (s *Store) loadExistingHold(ctx context.Context, tx *sqlx.Tx, params HoldParams) (BalanceHold, error) {
	stmt := s.DB.Rebind(`SELECT * FROM balance_holds
		WHERE tenant_id = ? AND wallet_id = ? AND reference_type = ? AND reference_id = ?`)
	var hold BalanceHold
	if err := tx.GetContext(ctx, &hold, stmt, params.TenantID, params.WalletID, params.ReferenceType, params.ReferenceID); err != nil {
		return BalanceHold{}, err
	}
	return hold, nil
}

func existingHoldCreateMatches(hold BalanceHold, params HoldParams) bool {
	return hold.TenantID == params.TenantID &&
		hold.WalletID == params.WalletID &&
		hold.Amount == params.Amount &&
		hold.Reason == params.Reason &&
		hold.ReferenceType == params.ReferenceType &&
		hold.ReferenceID == params.ReferenceID &&
		hold.IdempotencyKey == params.IdempotencyKey &&
		sameHoldExpiry(hold.ExpiresAt, params.ExpiresAt) &&
		rawJSONMatches(hold.Metadata, params.Metadata)
}

func sameHoldExpiry(stored, requested time.Time) bool {
	if stored.Equal(requested) {
		return true
	}
	return stored.Sub(requested).Abs() <= time.Microsecond
}

func rawJSONMatches(stored, requested []byte) bool {
	if len(stored) == 0 && len(requested) == 0 {
		return true
	}
	var storedValue any
	var requestedValue any
	storedDecoder := json.NewDecoder(bytes.NewReader(stored))
	storedDecoder.UseNumber()
	if err := storedDecoder.Decode(&storedValue); err != nil {
		return string(stored) == string(requested)
	}
	requestedDecoder := json.NewDecoder(bytes.NewReader(requested))
	requestedDecoder.UseNumber()
	if err := requestedDecoder.Decode(&requestedValue); err != nil {
		return string(stored) == string(requested)
	}
	return reflect.DeepEqual(storedValue, requestedValue)
}

func validateHoldWalletTarget(wallet *Wallet, params HoldParams) error {
	if wallet == nil ||
		wallet.TenantID != params.TenantID ||
		wallet.ID != params.WalletID {
		return ErrWalletNotFound
	}
	if wallet.Status != WalletStatusActive {
		return ErrWalletInactive
	}
	return nil
}

func (s *Store) lockHold(ctx context.Context, tx *sqlx.Tx, tenantID string, holdID int64) (*BalanceHold, error) {
	stmt := s.DB.Rebind("SELECT * FROM balance_holds WHERE tenant_id = ? AND id = ? FOR UPDATE")
	var hold BalanceHold
	if err := tx.GetContext(ctx, &hold, stmt, tenantID, holdID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrHoldNotFound
		}
		return nil, err
	}
	return &hold, nil
}

func (s *Store) lockWallet(ctx context.Context, tx *sqlx.Tx, tenantID string, walletID uuid.UUID) (*Wallet, error) {
	stmt := s.DB.Rebind("SELECT * FROM wallets WHERE tenant_id = ? AND id = ? FOR UPDATE")
	var w Wallet
	if err := tx.GetContext(ctx, &w, stmt, tenantID, walletID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWalletNotFound
		}
		return nil, err
	}
	return &w, nil
}

func (s *Store) updateWalletBalance(ctx context.Context, tx *sqlx.Tx, tenantID string, walletID uuid.UUID, balance, available int64, now time.Time) error {
	stmt := s.DB.Rebind(`UPDATE wallets
		SET balance = ?, available_balance = ?, version = version + 1, updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	_, err := tx.ExecContext(ctx, stmt, balance, available, now, tenantID, walletID)
	return err
}
