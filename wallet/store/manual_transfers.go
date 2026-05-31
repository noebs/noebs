package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
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
	if transfer.ApprovedBy.Valid ||
		transfer.ApprovedAt.Valid ||
		transfer.CompletedAt.Valid ||
		transfer.ProofOfPayment.Valid ||
		transfer.RejectionReason.Valid {
		return nil, ErrInvalidStatus
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO manual_transfers(
		tenant_id, workflow_id, idempotency_key, transfer_type, wallet_id, amount, currency,
		reason, status, requested_by, approved_by, proof_of_payment, psp_provider, psp_reference,
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
		transfer.RequestedBy,
		transfer.ApprovedBy,
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

func (s *Store) AddManualTransferApproval(ctx context.Context, approval ManualTransferApproval) (*ManualTransferApproval, error) {
	tenantID, err := ValidateTenantID(approval.TenantID)
	if err != nil {
		return nil, err
	}
	if approval.ManualTransferID <= 0 {
		return nil, ErrMissingManualTransferID
	}
	if approval.ApproverID <= 0 {
		return nil, ErrMissingApproverID
	}
	if err := ValidateManualTransferDecision(approval.Decision); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO manual_transfer_approvals(
		tenant_id, manual_transfer_id, approver_id, decision, reason, decided_at
	) VALUES(?, ?, ?, ?, ?, ?)
	ON CONFLICT DO NOTHING
	RETURNING *`)
	var stored ManualTransferApproval
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
		approval.ManualTransferID,
		approval.ApproverID,
		approval.Decision,
		approval.Reason,
		now,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			existing, getErr := s.getManualTransferApproval(ctx, tenantID, approval.ManualTransferID, approval.ApproverID)
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

func (s *Store) GetManualTransferByWorkflowID(ctx context.Context, workflowID string) (*ManualTransfer, error) {
	if workflowID == "" {
		return nil, ErrMissingWorkflowID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM manual_transfers WHERE workflow_id = ?")
	var transfer ManualTransfer
	if err := db.GetContext(ctx, &transfer, stmt, workflowID); err != nil {
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
		WHERE tenant_id = ? AND manual_transfer_id = ? AND approver_id = ?`)
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
		!nullInt64Equal(existing.RequestedBy, requested.RequestedBy) ||
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
		existing.ApproverID != requested.ApproverID ||
		existing.Decision != requested.Decision ||
		!nullStringEqual(existing.Reason, requested.Reason) {
		return ErrDuplicateManualApproval
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
	stmt := db.Rebind(`UPDATE manual_transfers
		SET status = ?, approved_by = ?, approved_at = ?, completed_at = ?,
			proof_of_payment = ?, rejection_reason = ?
		WHERE tenant_id = ? AND workflow_id = ?`)
	result, err := db.ExecContext(ctx, stmt,
		update.Status,
		update.ApprovedBy,
		update.ApprovedAt,
		update.CompletedAt,
		update.ProofOfPayment,
		update.RejectionReason,
		tenantID,
		workflowID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrManualTransferNotFound
	}
	return nil
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
	if filter.RequestedBy > 0 {
		query += " AND requested_by = ?"
		args = append(args, filter.RequestedBy)
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
