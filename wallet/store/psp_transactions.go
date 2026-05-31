package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type PSPStatusUpdate struct {
	Status           string
	PSPTransactionID sql.NullString
	ResponseCode     sql.NullString
	ResponseMessage  sql.NullString
	RawResponse      RawJSON
	ConfirmedAt      sql.NullTime
	LastPolledAt     sql.NullTime
	NextPollAt       sql.NullTime
	RetryCount       int
	LastErrorType    sql.NullString
	LastErrorAt      sql.NullTime
}

type PSPTransactionFilter struct {
	TenantID        string
	Status          string
	Provider        string
	Direction       string
	ClientReference string
	Start           time.Time
	End             time.Time
	Limit           int
	Offset          int
}

func (s *Store) CreatePSPTransaction(ctx context.Context, txn PSPTransaction) (*PSPTransaction, error) {
	tenantID, err := ValidateTenantID(txn.TenantID)
	if err != nil {
		return nil, err
	}
	if txn.PSPProvider == "" {
		return nil, ErrMissingProviderCode
	}
	if txn.IdempotencyKey == "" {
		return nil, ErrMissingIdempotencyKey
	}
	if txn.ClientReference == "" {
		return nil, ErrMissingClientReference
	}
	if txn.Direction == "" {
		return nil, ErrMissingDirection
	}
	if txn.Amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if txn.Currency == "" {
		return nil, ErrMissingCurrency
	}
	if err := ValidatePSPTransactionStatus(txn.Status); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if existing, err := s.GetPSPTransactionByReference(ctx, tenantID, txn.ClientReference); err == nil {
		if err := ValidatePSPTransactionCreateReplay(existing, txn); err != nil {
			return nil, err
		}
		return existing, nil
	} else if !errors.Is(err, ErrPSPTransactionNotFound) {
		return nil, err
	}
	if existing, err := s.getPSPTransactionByProviderIdempotency(ctx, tenantID, txn.PSPProvider, txn.IdempotencyKey); err == nil {
		if err := ValidatePSPTransactionCreateReplay(existing, txn); err != nil {
			return nil, err
		}
		return existing, nil
	} else if !errors.Is(err, ErrPSPTransactionNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO psp_transactions(
		tenant_id, psp_provider, psp_transaction_id, idempotency_key, client_reference,
		direction, amount, fee_amount, net_amount, currency, status, workflow_id,
		response_code, response_message, raw_request, raw_response, created_at,
		confirmed_at, last_polled_at, next_poll_at, reconciled_at, retry_count,
		lock_token, lock_expires_at, last_error_type, last_error_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)

	var stored PSPTransaction
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
		txn.PSPProvider,
		txn.PSPTransactionID,
		txn.IdempotencyKey,
		txn.ClientReference,
		txn.Direction,
		txn.Amount,
		txn.FeeAmount,
		txn.NetAmount,
		txn.Currency,
		txn.Status,
		txn.WorkflowID,
		txn.ResponseCode,
		txn.ResponseMessage,
		txn.RawRequest,
		txn.RawResponse,
		now,
		txn.ConfirmedAt,
		txn.LastPolledAt,
		txn.NextPollAt,
		txn.ReconciledAt,
		txn.RetryCount,
		txn.LockToken,
		txn.LockExpiresAt,
		txn.LastErrorType,
		txn.LastErrorAt,
	); err != nil {
		if existing, getErr := s.GetPSPTransactionByReference(ctx, tenantID, txn.ClientReference); getErr == nil {
			if err := ValidatePSPTransactionCreateReplay(existing, txn); err != nil {
				return nil, err
			}
			return existing, nil
		}
		if existing, getErr := s.getPSPTransactionByProviderIdempotency(ctx, tenantID, txn.PSPProvider, txn.IdempotencyKey); getErr == nil {
			if err := ValidatePSPTransactionCreateReplay(existing, txn); err != nil {
				return nil, err
			}
			return existing, nil
		}
		return nil, err
	}
	return &stored, nil
}

func ValidatePSPTransactionCreateReplay(existing *PSPTransaction, requested PSPTransaction) error {
	if existing == nil || !pspTransactionCreateReplayMatches(*existing, requested) {
		return ErrDuplicateTransaction
	}
	return nil
}

func pspTransactionCreateReplayMatches(existing PSPTransaction, requested PSPTransaction) bool {
	return existing.TenantID == requested.TenantID &&
		existing.PSPProvider == requested.PSPProvider &&
		existing.IdempotencyKey == requested.IdempotencyKey &&
		existing.ClientReference == requested.ClientReference &&
		existing.Direction == requested.Direction &&
		existing.Amount == requested.Amount &&
		existing.Currency == requested.Currency &&
		nullInt64Equal(existing.FeeAmount, requested.FeeAmount) &&
		nullInt64Equal(existing.NetAmount, requested.NetAmount) &&
		requestedNullStringMatches(existing.PSPTransactionID, requested.PSPTransactionID) &&
		requestedNullStringMatches(existing.WorkflowID, requested.WorkflowID) &&
		rawJSONMatches([]byte(existing.RawRequest), []byte(requested.RawRequest))
}

func nullInt64Equal(left, right sql.NullInt64) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Int64 == right.Int64)
}

func requestedNullStringMatches(existing, requested sql.NullString) bool {
	if !requested.Valid {
		return true
	}
	return existing.Valid && existing.String == requested.String
}

func (s *Store) GetPSPTransactionByReference(ctx context.Context, tenantID, clientReference string) (*PSPTransaction, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if clientReference == "" {
		return nil, ErrMissingClientReference
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM psp_transactions WHERE tenant_id = ? AND client_reference = ?")
	var txn PSPTransaction
	if err := db.GetContext(ctx, &txn, stmt, tenantID, clientReference); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrPSPTransactionNotFound
		}
		return nil, err
	}
	return &txn, nil
}

func (s *Store) getPSPTransactionByProviderIdempotency(ctx context.Context, tenantID, providerCode, idempotencyKey string) (*PSPTransaction, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM psp_transactions WHERE tenant_id = ? AND psp_provider = ? AND idempotency_key = ?")
	var txn PSPTransaction
	if err := db.GetContext(ctx, &txn, stmt, tenantID, providerCode, idempotencyKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPSPTransactionNotFound
		}
		return nil, err
	}
	return &txn, nil
}

func (s *Store) UpdatePSPTransactionStatus(ctx context.Context, tenantID, clientReference string, update PSPStatusUpdate) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if clientReference == "" {
		return ErrMissingClientReference
	}
	if err := ValidatePSPTransactionStatus(update.Status); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	existing, err := s.GetPSPTransactionByReference(ctx, tenantID, clientReference)
	if err != nil {
		return err
	}
	if err := ValidatePSPStatusUpdate(existing, update); err != nil {
		return err
	}
	stmt := db.Rebind(`UPDATE psp_transactions
			SET status = ?, psp_transaction_id = COALESCE(?, psp_transaction_id),
				response_code = COALESCE(?, response_code),
				response_message = COALESCE(?, response_message),
				raw_response = COALESCE(?, raw_response),
				confirmed_at = COALESCE(?, confirmed_at),
				last_polled_at = COALESCE(?, last_polled_at),
				next_poll_at = COALESCE(?, next_poll_at),
				retry_count = CASE WHEN ? = 0 THEN retry_count ELSE ? END,
				last_error_type = ?, last_error_at = ?
			WHERE tenant_id = ? AND client_reference = ?
			AND (status NOT IN ('success', 'failed', 'cancelled') OR status = ?)`)
	result, err := db.ExecContext(ctx, stmt,
		update.Status,
		update.PSPTransactionID,
		update.ResponseCode,
		update.ResponseMessage,
		update.RawResponse,
		update.ConfirmedAt,
		update.LastPolledAt,
		update.NextPollAt,
		update.RetryCount,
		update.RetryCount,
		update.LastErrorType,
		update.LastErrorAt,
		tenantID,
		clientReference,
		update.Status,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		stmt := db.Rebind("SELECT status FROM psp_transactions WHERE tenant_id = ? AND client_reference = ?")
		var currentStatus string
		if err := db.GetContext(ctx, &currentStatus, stmt, tenantID, clientReference); err != nil {
			if err == sql.ErrNoRows {
				return ErrPSPTransactionNotFound
			}
			return err
		}
		if PSPTransactionStatusTerminal(currentStatus) {
			return ValidatePSPStatusTransition(currentStatus, update.Status)
		}
		return ErrPSPTransactionNotFound
	}
	return nil
}

func ValidatePSPStatusUpdate(existing *PSPTransaction, update PSPStatusUpdate) error {
	if existing == nil {
		return ErrPSPTransactionNotFound
	}
	if err := ValidatePSPStatusTransition(existing.Status, update.Status); err != nil {
		return err
	}
	if existing.PSPTransactionID.Valid && update.PSPTransactionID.Valid && existing.PSPTransactionID.String != update.PSPTransactionID.String {
		return ErrDuplicateTransaction
	}
	if existing.Status == update.Status && PSPTransactionStatusTerminal(existing.Status) {
		if nullStringRewriteConflict(existing.ResponseCode, update.ResponseCode) ||
			nullStringRewriteConflict(existing.ResponseMessage, update.ResponseMessage) ||
			rawJSONRewriteConflict(existing.RawResponse, update.RawResponse) ||
			nullTimeRewriteConflict(existing.ConfirmedAt, update.ConfirmedAt) {
			return ErrDuplicateTransaction
		}
	}
	return nil
}

func nullStringRewriteConflict(existing, update sql.NullString) bool {
	return existing.Valid && update.Valid && existing.String != update.String
}

func nullTimeRewriteConflict(existing, update sql.NullTime) bool {
	return existing.Valid && update.Valid && !nullTimeEqual(existing, update)
}

func rawJSONRewriteConflict(existing, update RawJSON) bool {
	return len(existing) > 0 && len(update) > 0 && !rawJSONMatches([]byte(existing), []byte(update))
}

func (s *Store) ListPSPTransactionsForPolling(ctx context.Context, tenantID string, limit int) ([]PSPTransaction, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	stmt := db.Rebind(`SELECT * FROM psp_transactions
		WHERE tenant_id = ?
		AND status IN ('initiated', 'processing', 'pending')
		AND (next_poll_at IS NULL OR next_poll_at <= ?)
		AND (lock_expires_at IS NULL OR lock_expires_at <= ?)
		ORDER BY next_poll_at NULLS FIRST, created_at ASC
		LIMIT ?`)
	var rows []PSPTransaction
	if err := db.SelectContext(ctx, &rows, stmt, tenantID, now, now, limit); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) ListPSPTransactions(ctx context.Context, filter PSPTransactionFilter) ([]PSPTransaction, error) {
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
	if filter.Start.IsZero() != filter.End.IsZero() {
		if filter.Start.IsZero() {
			return nil, ErrMissingStartTime
		}
		return nil, ErrMissingEndTime
	}
	if !filter.Start.IsZero() && filter.Start.After(filter.End) {
		return nil, ErrInvalidTimeRange
	}
	if filter.Status != "" {
		if err := ValidatePSPTransactionStatus(filter.Status); err != nil {
			return nil, err
		}
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := `SELECT * FROM psp_transactions WHERE tenant_id = ?`
	args := []any{tenantID}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	if filter.Provider != "" {
		query += " AND psp_provider = ?"
		args = append(args, filter.Provider)
	}
	if filter.Direction != "" {
		query += " AND direction = ?"
		args = append(args, filter.Direction)
	}
	if filter.ClientReference != "" {
		query += " AND client_reference = ?"
		args = append(args, filter.ClientReference)
	}
	if !filter.Start.IsZero() && !filter.End.IsZero() {
		query += " AND created_at >= ? AND created_at <= ?"
		args = append(args, filter.Start, filter.End)
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, filter.Limit, filter.Offset)
	stmt := db.Rebind(query)
	var rows []PSPTransaction
	if err := db.SelectContext(ctx, &rows, stmt, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) ListPendingWithdrawalApprovals(ctx context.Context, tenantID string, limit, offset int) ([]PSPTransaction, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	if offset < 0 {
		return nil, ErrInvalidOffset
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM psp_transactions
		WHERE tenant_id = ?
		AND direction = 'outbound'
		AND status IN ('initiated', 'pending', 'held')
		AND (raw_request->>'approval_required') = 'true'
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`)
	var rows []PSPTransaction
	if err := db.SelectContext(ctx, &rows, stmt, tenantID, limit, offset); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) ListPSPTransactionsByStatus(ctx context.Context, tenantID, status string, start, end time.Time, limit int) ([]PSPTransaction, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if err := ValidatePSPTransactionStatus(status); err != nil {
		return nil, err
	}
	if start.IsZero() {
		return nil, ErrMissingStartTime
	}
	if end.IsZero() {
		return nil, ErrMissingEndTime
	}
	if start.After(end) {
		return nil, ErrInvalidTimeRange
	}
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM psp_transactions
		WHERE tenant_id = ? AND status = ? AND created_at >= ? AND created_at <= ?
		ORDER BY created_at ASC
		LIMIT ?`)
	var rows []PSPTransaction
	if err := db.SelectContext(ctx, &rows, stmt, tenantID, status, start, end, limit); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) TryAcquirePSPTransactionLock(ctx context.Context, tenantID, clientReference, lockToken string, lockExpiresAt time.Time) (bool, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return false, err
	}
	if clientReference == "" {
		return false, ErrMissingClientReference
	}
	if lockToken == "" {
		return false, ErrMissingLockToken
	}
	if lockExpiresAt.IsZero() {
		return false, ErrMissingLockExpiry
	}
	db, err := s.ensureDB()
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	stmt := db.Rebind(`UPDATE psp_transactions
		SET lock_token = ?, lock_expires_at = ?
		WHERE tenant_id = ? AND client_reference = ?
		AND (lock_expires_at IS NULL OR lock_expires_at <= ?)`)
	result, err := db.ExecContext(ctx, stmt, lockToken, lockExpiresAt, tenantID, clientReference, now)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		checkStmt := db.Rebind("SELECT 1 FROM psp_transactions WHERE tenant_id = ? AND client_reference = ?")
		var exists int
		if err := db.GetContext(ctx, &exists, checkStmt, tenantID, clientReference); err != nil {
			if err == sql.ErrNoRows {
				return false, ErrPSPTransactionNotFound
			}
			return false, err
		}
		return false, nil
	}
	return true, nil
}
