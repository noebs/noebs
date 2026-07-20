package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Store) CreateManualTransfer(ctx context.Context, transfer ManualTransfer) (*ManualTransfer, error) {
	tenantID, err := ValidateTenantID(transfer.TenantID)
	if err != nil {
		return nil, err
	}
	if transfer.WorkflowID == "" {
		return nil, ErrMissingWorkflowID
	}
	if transfer.IdempotencyKey == "" {
		return nil, ErrMissingIdempotencyKey
	}
	if err := ValidateManualTransferType(transfer.TransferType); err != nil {
		return nil, err
	}
	if transfer.Amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if transfer.Currency == "" {
		return nil, ErrMissingCurrency
	}
	if transfer.Reason == "" {
		return nil, ErrMissingReason
	}
	if err := ValidateManualTransferStatus(transfer.Status); err != nil {
		return nil, err
	}
	if transfer.Status != ManualTransferStatusPending {
		return nil, ErrInvalidStatus
	}
	if transfer.ApprovedByOperatorID.Valid ||
		transfer.ApprovedAt.Valid ||
		transfer.CompletedAt.Valid ||
		transfer.ProofOfPayment.Valid ||
		transfer.RejectionReason.Valid {
		return nil, ErrInvalidStatus
	}
	walletID, err := manualTransferWalletID(transfer.WalletID)
	if err != nil {
		return nil, err
	}
	if transfer.RequestedByOperatorID <= 0 {
		return nil, ErrMissingRequesterID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	wallet, err := s.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		return nil, err
	}
	requester, err := s.GetOperatorIdentityByID(ctx, transfer.RequestedByOperatorID)
	if err != nil {
		return nil, err
	}
	transfer.TenantID = tenantID
	if err := ValidateManualTransferCreateTarget(wallet, requester, transfer); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO manual_transfers(
		tenant_id, workflow_id, idempotency_key, transfer_type, wallet_id, amount, currency,
		reason, status, requested_by_operator_id, approved_by_operator_id, proof_of_payment, psp_provider, psp_reference,
		rejection_reason, requested_at, approved_at, completed_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT DO NOTHING
	RETURNING *`)
	var stored ManualTransfer
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
		transfer.WorkflowID,
		transfer.IdempotencyKey,
		transfer.TransferType,
		transfer.WalletID,
		transfer.Amount,
		transfer.Currency,
		transfer.Reason,
		transfer.Status,
		transfer.RequestedByOperatorID,
		transfer.ApprovedByOperatorID,
		transfer.ProofOfPayment,
		transfer.PSPProvider,
		transfer.PSPReference,
		transfer.RejectionReason,
		now,
		transfer.ApprovedAt,
		transfer.CompletedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			existing, getErr := s.getManualTransferByWorkflowOrIdempotency(ctx, tenantID, transfer.WorkflowID, transfer.IdempotencyKey)
			if getErr != nil {
				return nil, getErr
			}
			transfer.TenantID = tenantID
			if err := ValidateManualTransferCreateReplay(existing, transfer); err != nil {
				return nil, err
			}
			return existing, nil
		}
		return nil, err
	}
	return &stored, nil
}

func (s *Store) getManualTransferByID(ctx context.Context, tenantID string, manualTransferID int64) (*ManualTransfer, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM manual_transfers WHERE tenant_id = ? AND id = ?")
	var transfer ManualTransfer
	if err := db.GetContext(ctx, &transfer, stmt, tenantID, manualTransferID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrManualTransferNotFound
		}
		return nil, err
	}
	return &transfer, nil
}

func (s *Store) AddManualTransferApproval(ctx context.Context, approval ManualTransferApproval) (*ManualTransferApproval, error) {
	tenantID, err := ValidateTenantID(approval.TenantID)
	if err != nil {
		return nil, err
	}
	if approval.ManualTransferID <= 0 {
		return nil, ErrMissingManualTransferID
	}
	if approval.DecidedByOperatorID <= 0 {
		return nil, ErrMissingApproverID
	}
	if err := ValidateManualTransferDecision(approval.Decision); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	transfer, err := s.getManualTransferByID(ctx, tenantID, approval.ManualTransferID)
	if err != nil {
		return nil, err
	}
	approver, err := s.GetOperatorIdentityByID(ctx, approval.DecidedByOperatorID)
	if err != nil {
		return nil, err
	}
	approval.TenantID = tenantID
	if err := ValidateManualTransferApprovalTarget(transfer, approver, approval); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO manual_transfer_approvals(
		tenant_id, manual_transfer_id, decided_by_operator_id, decision, reason, decided_at
	) VALUES(?, ?, ?, ?, ?, ?)
	ON CONFLICT DO NOTHING
	RETURNING *`)
	var stored ManualTransferApproval
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
		approval.ManualTransferID,
		approval.DecidedByOperatorID,
		approval.Decision,
		approval.Reason,
		now,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			existing, getErr := s.getManualTransferApproval(ctx, tenantID, approval.ManualTransferID, approval.DecidedByOperatorID)
			if getErr != nil {
				return nil, getErr
			}
			approval.TenantID = tenantID
			if err := ValidateManualTransferApprovalReplay(existing, approval); err != nil {
				return nil, err
			}
			return existing, nil
		}
		return nil, err
	}
	return &stored, nil
}

func (s *Store) GetManualTransferByWorkflow(ctx context.Context, tenantID, workflowID string) (*ManualTransfer, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if workflowID == "" {
		return nil, ErrMissingWorkflowID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM manual_transfers WHERE tenant_id = ? AND workflow_id = ?")
	var transfer ManualTransfer
	if err := db.GetContext(ctx, &transfer, stmt, tenantID, workflowID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrManualTransferNotFound
		}
		return nil, err
	}
	return &transfer, nil
}

func (s *Store) getManualTransferByIdempotency(ctx context.Context, tenantID, idempotencyKey string) (*ManualTransfer, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM manual_transfers WHERE tenant_id = ? AND idempotency_key = ?")
	var transfer ManualTransfer
	if err := db.GetContext(ctx, &transfer, stmt, tenantID, idempotencyKey); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrManualTransferNotFound
		}
		return nil, err
	}
	return &transfer, nil
}

func (s *Store) getManualTransferByWorkflowOrIdempotency(ctx context.Context, tenantID, workflowID, idempotencyKey string) (*ManualTransfer, error) {
	existing, err := s.GetManualTransferByWorkflow(ctx, tenantID, workflowID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrManualTransferNotFound) {
		return nil, err
	}
	return s.getManualTransferByIdempotency(ctx, tenantID, idempotencyKey)
}

func (s *Store) getManualTransferApproval(ctx context.Context, tenantID string, manualTransferID, approverID int64) (*ManualTransferApproval, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM manual_transfer_approvals
		WHERE tenant_id = ? AND manual_transfer_id = ? AND decided_by_operator_id = ?`)
	var approval ManualTransferApproval
	if err := db.GetContext(ctx, &approval, stmt, tenantID, manualTransferID, approverID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrManualTransferNotFound
		}
		return nil, err
	}
	return &approval, nil
}

func ValidateManualTransferCreateReplay(existing *ManualTransfer, requested ManualTransfer) error {
	if existing == nil ||
		existing.TenantID != requested.TenantID ||
		existing.WorkflowID != requested.WorkflowID ||
		existing.IdempotencyKey != requested.IdempotencyKey ||
		existing.TransferType != requested.TransferType ||
		!nullStringEqual(existing.WalletID, requested.WalletID) ||
		existing.Amount != requested.Amount ||
		existing.Currency != requested.Currency ||
		existing.Reason != requested.Reason ||
		existing.RequestedByOperatorID != requested.RequestedByOperatorID ||
		!nullStringEqual(existing.PSPProvider, requested.PSPProvider) ||
		!nullStringEqual(existing.PSPReference, requested.PSPReference) {
		return ErrDuplicateManualTransfer
	}
	return nil
}

func ValidateManualTransferApprovalReplay(existing *ManualTransferApproval, requested ManualTransferApproval) error {
	if existing == nil ||
		existing.TenantID != requested.TenantID ||
		existing.ManualTransferID != requested.ManualTransferID ||
		existing.DecidedByOperatorID != requested.DecidedByOperatorID ||
		existing.Decision != requested.Decision ||
		!nullStringEqual(existing.Reason, requested.Reason) {
		return ErrDuplicateManualApproval
	}
	return nil
}

func manualTransferWalletID(walletID sql.NullString) (uuid.UUID, error) {
	if !walletID.Valid || walletID.String == "" {
		return uuid.Nil, ErrMissingWalletID
	}
	parsed, err := uuid.Parse(walletID.String)
	if err != nil || parsed == uuid.Nil {
		return uuid.Nil, ErrMissingWalletID
	}
	return parsed, nil
}

func ValidateManualTransferCreateTarget(wallet *Wallet, requester *OperatorIdentity, transfer ManualTransfer) error {
	walletID, err := manualTransferWalletID(transfer.WalletID)
	if err != nil {
		return err
	}
	if wallet == nil ||
		wallet.TenantID != transfer.TenantID ||
		wallet.ID != walletID {
		return ErrWalletNotFound
	}
	if wallet.Status != WalletStatusActive {
		return ErrWalletInactive
	}
	if wallet.Currency != transfer.Currency {
		return ErrCurrencyMismatch
	}
	if transfer.RequestedByOperatorID <= 0 {
		return ErrMissingRequesterID
	}
	if requester == nil || requester.ID != transfer.RequestedByOperatorID {
		return ErrOperatorIdentityNotFound
	}
	return nil
}

func ValidateManualTransferApprovalTarget(transfer *ManualTransfer, approver *OperatorIdentity, approval ManualTransferApproval) error {
	if transfer == nil ||
		transfer.TenantID != approval.TenantID ||
		transfer.ID != approval.ManualTransferID {
		return ErrManualTransferNotFound
	}
	if approver == nil || approver.ID != approval.DecidedByOperatorID {
		return ErrOperatorIdentityNotFound
	}
	if transfer.Status != ManualTransferStatusPending {
		return ErrInvalidStatusTransition
	}
	if transfer.RequestedByOperatorID == approval.DecidedByOperatorID {
		return ErrApproverIsRequester
	}
	return nil
}

func (s *Store) UpdateManualTransferStatus(ctx context.Context, tenantID, workflowID string, update ManualTransferStatusUpdate) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if workflowID == "" {
		return ErrMissingWorkflowID
	}
	if err := ValidateManualTransferStatus(update.Status); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	current, err := s.GetManualTransferByWorkflow(ctx, tenantID, workflowID)
	if err != nil {
		return err
	}
	if err := ValidateManualTransferStatusTransition(current, update); err != nil {
		return err
	}
	if update.Status == ManualTransferStatusApproved {
		approval, err := s.getManualTransferApproval(ctx, tenantID, current.ID, update.ApprovedByOperatorID.Int64)
		if err != nil {
			return err
		}
		if approval.Decision != ManualTransferStatusApproved {
			return ErrInvalidDecision
		}
	}
	update = mergeManualTransferStatusUpdate(current, update)
	stmt := db.Rebind(`UPDATE manual_transfers
		SET status = ?, approved_by_operator_id = ?, approved_at = ?, completed_at = ?,
			proof_of_payment = ?, rejection_reason = ?
		WHERE tenant_id = ? AND workflow_id = ? AND status = ?`)
	result, err := db.ExecContext(ctx, stmt,
		update.Status,
		update.ApprovedByOperatorID,
		update.ApprovedAt,
		update.CompletedAt,
		update.ProofOfPayment,
		update.RejectionReason,
		tenantID,
		workflowID,
		current.Status,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrInvalidStatusTransition
	}
	return nil
}

func ValidateManualTransferStatusTransition(current *ManualTransfer, update ManualTransferStatusUpdate) error {
	if current == nil {
		return ErrManualTransferNotFound
	}
	if err := validateManualTransferStoredStatusShape(current); err != nil {
		return err
	}
	if err := ValidateManualTransferStatus(update.Status); err != nil {
		return err
	}
	switch update.Status {
	case ManualTransferStatusPending:
		return ErrInvalidStatusTransition
	case ManualTransferStatusApproved:
		return validateManualTransferApprovalStatusUpdate(current, update)
	case ManualTransferStatusRejected:
		return validateManualTransferRejectionStatusUpdate(current, update)
	case ManualTransferStatusCompleted:
		return validateManualTransferCompletionStatusUpdate(current, update)
	default:
		return ErrInvalidStatus
	}
}

func validateManualTransferStoredStatusShape(current *ManualTransfer) error {
	if current.RequestedByOperatorID <= 0 {
		return ErrMissingRequesterID
	}
	if err := ValidateManualTransferStatus(current.Status); err != nil {
		return err
	}
	switch current.Status {
	case ManualTransferStatusPending:
		if current.ApprovedByOperatorID.Valid ||
			current.ApprovedAt.Valid ||
			current.CompletedAt.Valid ||
			current.ProofOfPayment.Valid ||
			current.RejectionReason.Valid {
			return ErrInvalidStatus
		}
	case ManualTransferStatusApproved:
		if err := validateManualTransferStoredApprovalEvidence(current); err != nil {
			return err
		}
		if current.CompletedAt.Valid || current.RejectionReason.Valid {
			return ErrInvalidStatus
		}
	case ManualTransferStatusRejected:
		if !validManualTransferText(current.RejectionReason) {
			return ErrMissingReason
		}
		if current.ApprovedByOperatorID.Valid ||
			current.ApprovedAt.Valid ||
			current.CompletedAt.Valid ||
			current.ProofOfPayment.Valid {
			return ErrInvalidStatus
		}
	case ManualTransferStatusCompleted:
		if err := validateManualTransferStoredApprovalEvidence(current); err != nil {
			return err
		}
		if !validManualTransferTime(current.CompletedAt) {
			return ErrMissingCompletionTime
		}
		if current.RejectionReason.Valid {
			return ErrInvalidStatus
		}
	}
	return nil
}

func validateManualTransferStoredApprovalEvidence(current *ManualTransfer) error {
	if !current.ApprovedByOperatorID.Valid || current.ApprovedByOperatorID.Int64 <= 0 {
		return ErrMissingApproverID
	}
	if current.RequestedByOperatorID == current.ApprovedByOperatorID.Int64 {
		return ErrApproverIsRequester
	}
	if !validManualTransferTime(current.ApprovedAt) {
		return ErrMissingApprovalTime
	}
	if !validManualTransferText(current.ProofOfPayment) {
		return ErrMissingProofOfPayment
	}
	return nil
}

func validateManualTransferApprovalStatusUpdate(current *ManualTransfer, update ManualTransferStatusUpdate) error {
	if current.Status != ManualTransferStatusPending && current.Status != ManualTransferStatusApproved {
		return ErrInvalidStatusTransition
	}
	if !update.ApprovedByOperatorID.Valid || update.ApprovedByOperatorID.Int64 <= 0 {
		return ErrMissingApproverID
	}
	if current.RequestedByOperatorID == update.ApprovedByOperatorID.Int64 {
		return ErrApproverIsRequester
	}
	if !validManualTransferTime(update.ApprovedAt) {
		return ErrMissingApprovalTime
	}
	if !validManualTransferText(update.ProofOfPayment) {
		return ErrMissingProofOfPayment
	}
	if update.CompletedAt.Valid || update.RejectionReason.Valid {
		return ErrInvalidStatus
	}
	if current.Status == ManualTransferStatusApproved && !manualTransferApprovalReplayMatches(current, update) {
		return ErrInvalidStatusTransition
	}
	return nil
}

func validateManualTransferRejectionStatusUpdate(current *ManualTransfer, update ManualTransferStatusUpdate) error {
	if current.Status != ManualTransferStatusPending && current.Status != ManualTransferStatusRejected {
		return ErrInvalidStatusTransition
	}
	if !validManualTransferText(update.RejectionReason) {
		return ErrMissingReason
	}
	if update.ApprovedByOperatorID.Valid || update.ApprovedAt.Valid || update.CompletedAt.Valid || update.ProofOfPayment.Valid {
		return ErrInvalidStatus
	}
	if current.Status == ManualTransferStatusRejected && !manualTransferRejectionReplayMatches(current, update) {
		return ErrInvalidStatusTransition
	}
	return nil
}

func validateManualTransferCompletionStatusUpdate(current *ManualTransfer, update ManualTransferStatusUpdate) error {
	if current.Status != ManualTransferStatusApproved && current.Status != ManualTransferStatusCompleted {
		return ErrInvalidStatusTransition
	}
	if !validManualTransferTime(update.CompletedAt) {
		return ErrMissingCompletionTime
	}
	if update.RejectionReason.Valid {
		return ErrInvalidStatus
	}
	if !current.ApprovedByOperatorID.Valid || current.ApprovedByOperatorID.Int64 <= 0 {
		return ErrMissingApproverID
	}
	if !validManualTransferTime(current.ApprovedAt) {
		return ErrMissingApprovalTime
	}
	if !validManualTransferText(current.ProofOfPayment) {
		return ErrMissingProofOfPayment
	}
	if update.ApprovedByOperatorID.Valid && update.ApprovedByOperatorID.Int64 != current.ApprovedByOperatorID.Int64 {
		return ErrInvalidStatus
	}
	if update.ApprovedAt.Valid && !sameManualTransferTime(update.ApprovedAt.Time, current.ApprovedAt.Time) {
		return ErrInvalidStatus
	}
	if update.ProofOfPayment.Valid && update.ProofOfPayment.String != current.ProofOfPayment.String {
		return ErrInvalidStatus
	}
	if current.Status == ManualTransferStatusCompleted && !manualTransferCompletionReplayMatches(current, update) {
		return ErrInvalidStatusTransition
	}
	return nil
}

func mergeManualTransferStatusUpdate(current *ManualTransfer, update ManualTransferStatusUpdate) ManualTransferStatusUpdate {
	if current == nil {
		return update
	}
	if !update.ApprovedByOperatorID.Valid {
		update.ApprovedByOperatorID = current.ApprovedByOperatorID
	}
	if !update.ApprovedAt.Valid {
		update.ApprovedAt = current.ApprovedAt
	}
	if !update.ProofOfPayment.Valid {
		update.ProofOfPayment = current.ProofOfPayment
	}
	if !update.RejectionReason.Valid {
		update.RejectionReason = current.RejectionReason
	}
	if !update.CompletedAt.Valid {
		update.CompletedAt = current.CompletedAt
	}
	return update
}

func manualTransferApprovalReplayMatches(current *ManualTransfer, update ManualTransferStatusUpdate) bool {
	return current.ApprovedByOperatorID.Valid &&
		current.ApprovedByOperatorID.Int64 == update.ApprovedByOperatorID.Int64 &&
		sameManualTransferNullTime(current.ApprovedAt, update.ApprovedAt) &&
		nullStringEqual(current.ProofOfPayment, update.ProofOfPayment)
}

func manualTransferRejectionReplayMatches(current *ManualTransfer, update ManualTransferStatusUpdate) bool {
	return nullStringEqual(current.RejectionReason, update.RejectionReason)
}

func manualTransferCompletionReplayMatches(current *ManualTransfer, update ManualTransferStatusUpdate) bool {
	return sameManualTransferNullTime(current.CompletedAt, update.CompletedAt)
}

func validManualTransferText(value sql.NullString) bool {
	return value.Valid && strings.TrimSpace(value.String) != ""
}

func validManualTransferTime(value sql.NullTime) bool {
	return value.Valid && !value.Time.IsZero()
}

func sameManualTransferNullTime(a, b sql.NullTime) bool {
	if a.Valid != b.Valid {
		return false
	}
	if !a.Valid {
		return true
	}
	return sameManualTransferTime(a.Time, b.Time)
}

func sameManualTransferTime(a, b time.Time) bool {
	if a.Equal(b) {
		return true
	}
	return a.Sub(b).Abs() <= time.Microsecond
}

func (s *Store) ListManualTransfers(ctx context.Context, filter ManualTransferFilter) ([]ManualTransfer, error) {
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
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := `SELECT * FROM manual_transfers WHERE tenant_id = ?`
	args := []any{tenantID}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	if filter.TransferType != "" {
		query += " AND transfer_type = ?"
		args = append(args, filter.TransferType)
	}
	if filter.WalletID != "" {
		query += " AND wallet_id = ?"
		args = append(args, filter.WalletID)
	}
	if filter.RequesterOperatorID > 0 {
		query += " AND requested_by_operator_id = ?"
		args = append(args, filter.RequesterOperatorID)
	}
	if !filter.Start.IsZero() && !filter.End.IsZero() {
		query += " AND requested_at >= ? AND requested_at <= ?"
		args = append(args, filter.Start, filter.End)
	}
	query += " ORDER BY requested_at DESC LIMIT ? OFFSET ?"
	args = append(args, filter.Limit, filter.Offset)
	stmt := db.Rebind(query)
	var transfers []ManualTransfer
	if err := db.SelectContext(ctx, &transfers, stmt, args...); err != nil {
		return nil, err
	}
	return transfers, nil
}

func (s *Store) ListManualTransferApprovals(ctx context.Context, tenantID string, manualTransferID int64) ([]ManualTransferApproval, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if manualTransferID <= 0 {
		return nil, ErrMissingManualTransferID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM manual_transfer_approvals
		WHERE tenant_id = ? AND manual_transfer_id = ?
		ORDER BY decided_at ASC`)
	var approvals []ManualTransferApproval
	if err := db.SelectContext(ctx, &approvals, stmt, tenantID, manualTransferID); err != nil {
		return nil, err
	}
	return approvals, nil
}

func (s *Store) ListManualTransfersByStatus(ctx context.Context, tenantID, status string, limit, offset int) ([]ManualTransfer, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if err := ValidateManualTransferStatus(status); err != nil {
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
	stmt := db.Rebind(`SELECT * FROM manual_transfers
		WHERE tenant_id = ? AND status = ?
		ORDER BY requested_at DESC
		LIMIT ? OFFSET ?`)
	var transfers []ManualTransfer
	if err := db.SelectContext(ctx, &transfers, stmt, tenantID, status, limit, offset); err != nil {
		return nil, err
	}
	return transfers, nil
}
