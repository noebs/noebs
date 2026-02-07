package store

import (
	"context"
	"database/sql"
	"time"
)

type PSPStatusUpdate struct {
	Status           string
	PSPTransactionID sql.NullString
	ResponseCode     sql.NullString
	ResponseMessage  sql.NullString
	ConfirmedAt      sql.NullTime
	LastPolledAt     sql.NullTime
	NextPollAt       sql.NullTime
	RetryCount       int
	LastErrorType    sql.NullString
	LastErrorAt      sql.NullTime
}

func (s *Store) CreatePSPTransaction(ctx context.Context, txn PSPTransaction) (*PSPTransaction, error) {
	if txn.TenantID == "" {
		return nil, ErrMissingTenantID
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
	if txn.Status == "" {
		return nil, ErrMissingStatus
	}
	db, err := s.ensureDB()
	if err != nil {
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
		txn.TenantID,
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
		return nil, err
	}
	return &stored, nil
}

func (s *Store) GetPSPTransactionByReference(ctx context.Context, tenantID, clientReference string) (*PSPTransaction, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
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

func (s *Store) UpdatePSPTransactionStatus(ctx context.Context, tenantID, clientReference string, update PSPStatusUpdate) error {
	if tenantID == "" {
		return ErrMissingTenantID
	}
	if clientReference == "" {
		return ErrMissingClientReference
	}
	if update.Status == "" {
		return ErrMissingStatus
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := db.Rebind(`UPDATE psp_transactions
		SET status = ?, psp_transaction_id = COALESCE(?, psp_transaction_id),
			response_code = ?, response_message = ?,
			confirmed_at = COALESCE(?, confirmed_at),
			last_polled_at = COALESCE(?, last_polled_at),
			next_poll_at = COALESCE(?, next_poll_at),
			retry_count = CASE WHEN ? = 0 THEN retry_count ELSE ? END,
			last_error_type = ?, last_error_at = ?
		WHERE tenant_id = ? AND client_reference = ?`)
	result, err := db.ExecContext(ctx, stmt,
		update.Status,
		update.PSPTransactionID,
		update.ResponseCode,
		update.ResponseMessage,
		update.ConfirmedAt,
		update.LastPolledAt,
		update.NextPollAt,
		update.RetryCount,
		update.RetryCount,
		update.LastErrorType,
		update.LastErrorAt,
		tenantID,
		clientReference,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrPSPTransactionNotFound
	}
	return nil
}

func (s *Store) ListPSPTransactionsForPolling(ctx context.Context, tenantID string, limit int) ([]PSPTransaction, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
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

func (s *Store) ListPSPTransactionsByStatus(ctx context.Context, tenantID, status string, start, end time.Time, limit int) ([]PSPTransaction, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if status == "" {
		return nil, ErrMissingStatus
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
	if tenantID == "" {
		return false, ErrMissingTenantID
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
