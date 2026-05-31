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
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	walletRow, err := s.lockWallet(ctx, tx, params.TenantID, params.WalletID)
	if err != nil {
		return nil, err
	}
	if walletRow.AvailableBalance < params.Amount {
		return nil, ErrInsufficientFunds
	}

	now := time.Now().UTC()
	insertStmt := db.Rebind(`INSERT INTO balance_holds(
		tenant_id, wallet_id, amount, amount_remaining, reason, reference_type, reference_id,
		idempotency_key, status, expires_at, created_at, metadata
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			hold, err = s.loadExistingHold(ctx, tx, params.TenantID, params.WalletID, params.ReferenceType, params.ReferenceID)
			if err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return &hold, nil
		}
		return nil, err
	}

	walletRow.AvailableBalance -= params.Amount
	if err := s.updateWalletBalance(ctx, tx, params.TenantID, walletRow.ID, walletRow.Balance, walletRow.AvailableBalance, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
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
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	hold, err := s.lockHold(ctx, tx, tenantID, holdID)
	if err != nil {
		return err
	}
	if hold.Status != HoldStatusActive {
		if err := tx.Commit(); err != nil {
			return err
		}
		return nil
	}

	walletRow, err := s.lockWallet(ctx, tx, tenantID, hold.WalletID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	walletRow.AvailableBalance += hold.AmountRemaining
	if err := s.updateWalletBalance(ctx, tx, tenantID, walletRow.ID, walletRow.Balance, walletRow.AvailableBalance, now); err != nil {
		return err
	}

	updateStmt := db.Rebind(`UPDATE balance_holds
		SET status = ?, amount_remaining = 0, released_at = ?
		WHERE tenant_id = ? AND id = ?`)
	if _, err := tx.ExecContext(ctx, updateStmt, HoldStatusReleased, now, tenantID, hold.ID); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) loadExistingHold(ctx context.Context, tx *sqlx.Tx, tenantID string, walletID uuid.UUID, refType, refID string) (BalanceHold, error) {
	stmt := s.DB.Rebind(`SELECT * FROM balance_holds
		WHERE tenant_id = ? AND wallet_id = ? AND reference_type = ? AND reference_id = ?`)
	var hold BalanceHold
	if err := tx.GetContext(ctx, &hold, stmt, tenantID, walletID, refType, refID); err != nil {
		return BalanceHold{}, err
	}
	return hold, nil
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
