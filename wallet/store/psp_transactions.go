package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
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
	LockToken        sql.NullString
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
	if err := validatePSPDecisionDeadline(txn); err != nil {
		return nil, err
	}
	if err := validatePSPDepositIntent(txn); err != nil {
		return nil, err
	}
	if err := validatePSPTransactionDirectionAuthority(txn); err != nil {
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

	stmt := db.Rebind(`INSERT INTO psp_transactions(
		tenant_id, psp_provider, psp_transaction_id, idempotency_key, client_reference,
		direction, wallet_id, owner_type, owner_id, withdrawal_destination_id, allow_return_to_source,
		amount, fee_amount, net_amount, currency, status, workflow_id,
		response_code, response_message, raw_request, raw_response, approval_timeout_seconds, decision_deadline_at, deposit_intent_id, created_at,
		confirmed_at, last_polled_at, next_poll_at, reconciled_at, retry_count,
		lock_token, lock_expires_at, last_error_type, last_error_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, clock_timestamp() + (? * interval '1 second'), ?, clock_timestamp(), ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)

	var stored PSPTransaction
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
		txn.PSPProvider,
		txn.PSPTransactionID,
		txn.IdempotencyKey,
		txn.ClientReference,
		txn.Direction,
		txn.WalletID,
		txn.OwnerType,
		txn.OwnerID,
		txn.WithdrawalDestinationID,
		txn.AllowReturnToSource,
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
		txn.ApprovalTimeoutSeconds,
		txn.ApprovalTimeoutSeconds,
		txn.DepositIntentID,
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
		nullUUIDEqual(existing.WalletID, requested.WalletID) &&
		nullStringEqual(existing.OwnerType, requested.OwnerType) &&
		nullStringEqual(existing.OwnerID, requested.OwnerID) &&
		nullInt64Equal(existing.WithdrawalDestinationID, requested.WithdrawalDestinationID) &&
		nullBoolEqual(existing.AllowReturnToSource, requested.AllowReturnToSource) &&
		existing.Amount == requested.Amount &&
		existing.Currency == requested.Currency &&
		nullInt64Equal(existing.FeeAmount, requested.FeeAmount) &&
		nullInt64Equal(existing.NetAmount, requested.NetAmount) &&
		nullInt64Equal(existing.ApprovalTimeoutSeconds, requested.ApprovalTimeoutSeconds) &&
		nullInt64Equal(existing.DepositIntentID, requested.DepositIntentID) &&
		requestedNullStringMatches(existing.PSPTransactionID, requested.PSPTransactionID) &&
		requestedNullStringMatches(existing.WorkflowID, requested.WorkflowID) &&
		rawJSONMatches([]byte(existing.RawRequest), []byte(requested.RawRequest))
}

func validatePSPDepositIntent(txn PSPTransaction) error {
	switch txn.Direction {
	case "inbound":
		if !txn.DepositIntentID.Valid {
			return ErrMissingDepositIntentID
		}
		if txn.DepositIntentID.Int64 <= 0 {
			return ErrInvalidDepositIntentID
		}
	case "outbound":
		if txn.DepositIntentID.Valid {
			return ErrInvalidDepositIntentID
		}
	}
	return nil
}

func validatePSPTransactionDirectionAuthority(txn PSPTransaction) error {
	switch txn.Direction {
	case "inbound":
		if txn.WalletID.Valid || txn.OwnerType.Valid || txn.OwnerID.Valid ||
			txn.WithdrawalDestinationID.Valid || txn.AllowReturnToSource.Valid {
			return ErrInvalidWithdrawalRequest
		}
	case "outbound":
		if !txn.WalletID.Valid || txn.WalletID.UUID == uuid.Nil {
			return ErrMissingWalletID
		}
		if !txn.OwnerType.Valid || txn.OwnerType.String == "" {
			return ErrMissingOwnerType
		}
		if !OwnerTypeValid(txn.OwnerType.String) {
			return ErrInvalidOwnerType
		}
		if !txn.OwnerID.Valid || txn.OwnerID.String == "" {
			return ErrMissingOwnerID
		}
		if strings.TrimSpace(txn.OwnerID.String) != txn.OwnerID.String {
			return ErrInvalidWithdrawalRequest
		}
		if !txn.AllowReturnToSource.Valid {
			return ErrMissingReturnToSourcePolicy
		}
		if txn.WithdrawalDestinationID.Valid && txn.WithdrawalDestinationID.Int64 <= 0 {
			return ErrInvalidDestinationID
		}
		if !txn.AllowReturnToSource.Bool && !txn.WithdrawalDestinationID.Valid {
			return ErrMissingDestinationID
		}
	}
	return nil
}

func validatePSPDecisionDeadline(txn PSPTransaction) error {
	var request struct {
		ApprovalRequired       *bool `json:"approval_required"`
		ApprovalTimeoutSeconds int   `json:"approval_timeout_seconds"`
		HoldExpirySeconds      int   `json:"hold_expiry_seconds"`
	}
	if len(txn.RawRequest) > 0 {
		if err := json.Unmarshal(txn.RawRequest, &request); err != nil {
			return err
		}
	}
	if request.ApprovalRequired != nil && *request.ApprovalRequired {
		if !txn.ApprovalTimeoutSeconds.Valid || txn.ApprovalTimeoutSeconds.Int64 <= 0 {
			return ErrMissingApprovalTimeout
		}
		if request.ApprovalTimeoutSeconds <= 0 || request.HoldExpirySeconds <= 0 {
			return ErrMissingApprovalTimeout
		}
		expected := min(request.ApprovalTimeoutSeconds, request.HoldExpirySeconds)
		if txn.ApprovalTimeoutSeconds.Int64 != int64(expected) {
			return ErrInvalidApprovalTimeout
		}
		if txn.DecisionDeadlineAt.Valid {
			return ErrInvalidApprovalTimeout
		}
		return nil
	}
	if txn.ApprovalTimeoutSeconds.Valid || txn.DecisionDeadlineAt.Valid {
		return ErrInvalidApprovalTimeout
	}
	return nil
}

func nullInt64Equal(left, right sql.NullInt64) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Int64 == right.Int64)
}

func nullBoolEqual(left, right sql.NullBool) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Bool == right.Bool)
}

func nullUUIDEqual(left, right uuid.NullUUID) bool {
	return left.Valid == right.Valid && (!left.Valid || left.UUID == right.UUID)
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

func (s *Store) GetPSPTransactionByWorkflow(ctx context.Context, tenantID, workflowID string) (*PSPTransaction, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if err := validateBoundedIdentifier(workflowID, 255, ErrMissingWorkflowID, ErrInvalidWorkflowID); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM psp_transactions WHERE tenant_id = ? AND workflow_id = ?")
	var txn PSPTransaction
	if err := db.GetContext(ctx, &txn, stmt, tenantID, workflowID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	existing, err := getPSPTransactionForUpdate(ctx, tx, tenantID, clientReference)
	if err != nil {
		return err
	}
	if err := ValidatePSPStatusUpdate(existing, update); err != nil {
		return err
	}
	if _, err := updatePSPTransactionRow(ctx, tx, tenantID, clientReference, update, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ApplyExternalPSPStatus(ctx context.Context, tenantID, clientReference string, update PSPStatusUpdate, workflowSignal *PSPWorkflowSignal) (*PSPTransaction, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if clientReference == "" {
		return nil, ErrMissingClientReference
	}
	if err := ValidatePSPTransactionStatus(update.Status); err != nil {
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
	defer func() { _ = tx.Rollback() }()
	existing, err := getPSPTransactionForUpdate(ctx, tx, tenantID, clientReference)
	if err != nil {
		return nil, err
	}
	if err := ValidatePSPStatusTransition(existing.Status, update.Status); err != nil {
		return nil, err
	}
	if existing.PSPTransactionID.Valid && update.PSPTransactionID.Valid && existing.PSPTransactionID.String != update.PSPTransactionID.String {
		return nil, ErrDuplicateTransaction
	}
	if PSPTransactionStatusTerminal(existing.Status) {
		return existing, nil
	}
	terminal := PSPTransactionStatusTerminal(update.Status)
	switch {
	case terminal && existing.WorkflowID.Valid && workflowSignal == nil:
		return nil, ErrMissingWorkflowSignal
	case terminal && !existing.WorkflowID.Valid && workflowSignal != nil:
		return nil, ErrMissingWorkflowID
	case !terminal && workflowSignal != nil:
		return nil, ErrInvalidStatusTransition
	}
	if err := ValidatePSPStatusUpdate(existing, update); err != nil {
		return nil, err
	}
	workflowSignalPayload, err := encodePSPWorkflowSignal(existing, update, workflowSignal)
	if err != nil {
		return nil, err
	}
	stored, err := applyExternalPSPStatusRow(ctx, tx, tenantID, clientReference, update, workflowSignalPayload)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stored, nil
}

func applyExternalPSPStatusRow(ctx context.Context, tx pspTransactionTx, tenantID, clientReference string, update PSPStatusUpdate, workflowSignal RawJSON) (*PSPTransaction, error) {
	query := `UPDATE psp_transactions
		SET status = ?, psp_transaction_id = COALESCE(?, psp_transaction_id),
			response_code = COALESCE(?, response_code),
			response_message = COALESCE(?, response_message),
			raw_response = COALESCE(?, raw_response),
			confirmed_at = COALESCE(?, confirmed_at),
			workflow_signal_payload = COALESCE(workflow_signal_payload, ?)`
	args := []any{
		update.Status,
		update.PSPTransactionID,
		update.ResponseCode,
		update.ResponseMessage,
		update.RawResponse,
		update.ConfirmedAt,
		workflowSignal,
	}
	if update.LockToken.Valid {
		query += `, last_polled_at = COALESCE(?, last_polled_at),
			next_poll_at = COALESCE(?, next_poll_at),
			retry_count = CASE WHEN ? = 0 THEN retry_count ELSE ? END,
			last_error_type = ?, last_error_at = ?`
		args = append(args,
			update.LastPolledAt,
			update.NextPollAt,
			update.RetryCount,
			update.RetryCount,
			update.LastErrorType,
			update.LastErrorAt,
		)
	}
	query += ` WHERE tenant_id = ? AND client_reference = ?
		AND status NOT IN ('success', 'failed', 'cancelled')`
	args = append(args, tenantID, clientReference)
	if update.LockToken.Valid {
		query += " AND lock_token = ?"
		args = append(args, update.LockToken.String)
	}
	query += " RETURNING *"
	var stored PSPTransaction
	if err := tx.GetContext(ctx, &stored, tx.Rebind(query), args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) && update.LockToken.Valid {
			return nil, ErrPSPTransactionLockLost
		}
		return nil, err
	}
	return &stored, nil
}

func ParsePSPWorkflowSignal(payload RawJSON) (PSPWorkflowSignal, error) {
	if len(payload) == 0 {
		return PSPWorkflowSignal{}, ErrMissingWorkflowSignal
	}
	var signal PSPWorkflowSignal
	if err := json.Unmarshal(payload, &signal); err != nil {
		return PSPWorkflowSignal{}, err
	}
	if err := ValidatePSPTransactionStatus(signal.Status); err != nil {
		return PSPWorkflowSignal{}, err
	}
	if !PSPTransactionStatusTerminal(signal.Status) {
		return PSPWorkflowSignal{}, ErrInvalidStatusTransition
	}
	return signal, nil
}

func encodePSPWorkflowSignal(existing *PSPTransaction, update PSPStatusUpdate, signal *PSPWorkflowSignal) (RawJSON, error) {
	if signal == nil {
		return nil, nil
	}
	if err := ValidatePSPTransactionStatus(signal.Status); err != nil {
		return nil, err
	}
	if signal.Status != update.Status || !PSPTransactionStatusTerminal(signal.Status) {
		return nil, ErrInvalidStatusTransition
	}
	expectedProviderTxID := existing.PSPTransactionID
	if update.PSPTransactionID.Valid {
		expectedProviderTxID = update.PSPTransactionID
	}
	if expectedProviderTxID.Valid && signal.ProviderTxID != expectedProviderTxID.String {
		return nil, ErrDuplicateTransaction
	}
	if signal.Status == PSPStatusSuccess {
		if signal.ProviderTxID == "" {
			return nil, ErrMissingPSPTransactionID
		}
		if signal.Amount != existing.Amount {
			return nil, ErrInvalidAmount
		}
		if signal.Currency != existing.Currency {
			return nil, ErrCurrencyMismatch
		}
	}
	if len(update.RawResponse) > 0 && !rawJSONMatches(update.RawResponse, signal.RawResponse) {
		return nil, ErrDuplicateTransaction
	}
	payload, err := json.Marshal(signal)
	if err != nil {
		return nil, err
	}
	return RawJSON(payload), nil
}

func (s *Store) AcknowledgePSPWorkflowSignal(ctx context.Context, tenantID, clientReference string, deliveredAt time.Time, lockToken string) (*PSPTransaction, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if clientReference == "" {
		return nil, ErrMissingClientReference
	}
	if deliveredAt.IsZero() {
		return nil, ErrMissingWorkflowSignalDeliveryTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, err := getPSPTransactionForUpdate(ctx, tx, tenantID, clientReference)
	if err != nil {
		return nil, err
	}
	if len(existing.WorkflowSignalPayload) == 0 {
		if lockToken == "" {
			return existing, nil
		}
		return nil, ErrMissingWorkflowSignal
	}
	if existing.WorkflowSignalDeliveredAt.Valid {
		return existing, nil
	}
	if lockToken != "" && (!existing.LockToken.Valid || existing.LockToken.String != lockToken) {
		return nil, ErrPSPTransactionLockLost
	}
	stmt := tx.Rebind(`UPDATE psp_transactions
		SET workflow_signal_delivered_at = ?, lock_token = NULL, lock_expires_at = NULL
		WHERE tenant_id = ? AND client_reference = ?
		RETURNING *`)
	var stored PSPTransaction
	if err := tx.GetContext(ctx, &stored, stmt, deliveredAt, tenantID, clientReference); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &stored, nil
}

type pspTransactionTx interface {
	Rebind(string) string
	GetContext(context.Context, any, string, ...any) error
}

func getPSPTransactionForUpdate(ctx context.Context, tx pspTransactionTx, tenantID, clientReference string) (*PSPTransaction, error) {
	stmt := tx.Rebind("SELECT * FROM psp_transactions WHERE tenant_id = ? AND client_reference = ? FOR UPDATE")
	var txn PSPTransaction
	if err := tx.GetContext(ctx, &txn, stmt, tenantID, clientReference); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPSPTransactionNotFound
		}
		return nil, err
	}
	return &txn, nil
}

func updatePSPTransactionRow(ctx context.Context, tx pspTransactionTx, tenantID, clientReference string, update PSPStatusUpdate, workflowSignal RawJSON) (*PSPTransaction, error) {
	query := `UPDATE psp_transactions
			SET status = ?, psp_transaction_id = COALESCE(?, psp_transaction_id),
				response_code = COALESCE(?, response_code),
				response_message = COALESCE(?, response_message),
				raw_response = COALESCE(?, raw_response),
				confirmed_at = COALESCE(?, confirmed_at),
				last_polled_at = COALESCE(?, last_polled_at),
				next_poll_at = COALESCE(?, next_poll_at),
				retry_count = CASE WHEN ? = 0 THEN retry_count ELSE ? END,
				last_error_type = ?, last_error_at = ?,
				workflow_signal_payload = COALESCE(workflow_signal_payload, ?)
			WHERE tenant_id = ? AND client_reference = ?
			AND (status NOT IN ('success', 'failed', 'cancelled') OR status = ?)`
	args := []any{
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
		workflowSignal,
		tenantID,
		clientReference,
		update.Status,
	}
	if update.LockToken.Valid {
		query += " AND lock_token = ?"
		args = append(args, update.LockToken.String)
	}
	query += " RETURNING *"
	var stored PSPTransaction
	err := tx.GetContext(ctx, &stored, tx.Rebind(query), args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if update.LockToken.Valid {
				return nil, ErrPSPTransactionLockLost
			}
			return nil, ErrPSPTransactionNotFound
		}
		return nil, err
	}
	return &stored, nil
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
	return existing.Valid && update.Valid && !existing.Time.Equal(update.Time)
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
		AND (lock_expires_at IS NULL OR lock_expires_at <= ?)
		AND (
			(workflow_signal_payload IS NOT NULL AND workflow_signal_delivered_at IS NULL)
			OR (
				status IN ('initiated', 'processing', 'pending', 'held')
				AND (next_poll_at IS NULL OR next_poll_at <= ?)
			)
		)
		ORDER BY (workflow_signal_payload IS NOT NULL AND workflow_signal_delivered_at IS NULL) DESC,
			next_poll_at NULLS FIRST, created_at ASC
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
		AND (lock_expires_at IS NULL OR lock_expires_at <= ?)
		AND (
			(workflow_signal_payload IS NOT NULL AND workflow_signal_delivered_at IS NULL)
			OR (
				status IN ('initiated', 'processing', 'pending', 'held')
				AND (next_poll_at IS NULL OR next_poll_at <= ?)
			)
		)`)
	result, err := db.ExecContext(ctx, stmt, lockToken, lockExpiresAt, tenantID, clientReference, now, now)
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
