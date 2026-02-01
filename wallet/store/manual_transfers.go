package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) CreateManualTransfer(ctx context.Context, transfer ManualTransfer) (*ManualTransfer, error) {
	if transfer.TenantID == "" {
		return nil, ErrMissingTenantID
	}
	if transfer.WorkflowID == "" {
		return nil, ErrMissingWorkflowID
	}
	if transfer.IdempotencyKey == "" {
		return nil, ErrMissingIdempotencyKey
	}
	if transfer.TransferType == "" {
		return nil, ErrMissingTransferType
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
	if transfer.Status == "" {
		return nil, ErrMissingStatus
	}
	if _, err := s.ensureDB(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO manual_transfers(
		tenant_id, workflow_id, idempotency_key, transfer_type, wallet_id, amount, currency,
		reason, status, requested_by, approved_by, proof_of_payment, psp_provider, psp_reference,
		rejection_reason, requested_at, approved_at, completed_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored ManualTransfer
	if err := s.DB.GetContext(ctx, &stored, stmt,
		transfer.TenantID,
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
		return nil, err
	}
	return &stored, nil
}

func (s *Store) AddManualTransferApproval(ctx context.Context, approval ManualTransferApproval) (*ManualTransferApproval, error) {
	if approval.TenantID == "" {
		return nil, ErrMissingTenantID
	}
	if approval.ManualTransferID <= 0 {
		return nil, ErrMissingManualTransferID
	}
	if approval.ApproverID <= 0 {
		return nil, ErrMissingApproverID
	}
	if approval.Decision == "" {
		return nil, ErrMissingDecision
	}
	if _, err := s.ensureDB(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO manual_transfer_approvals(
		tenant_id, manual_transfer_id, approver_id, decision, reason, decided_at
	) VALUES(?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored ManualTransferApproval
	if err := s.DB.GetContext(ctx, &stored, stmt,
		approval.TenantID,
		approval.ManualTransferID,
		approval.ApproverID,
		approval.Decision,
		approval.Reason,
		now,
	); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *Store) GetManualTransferByWorkflow(ctx context.Context, tenantID, workflowID string) (*ManualTransfer, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if workflowID == "" {
		return nil, ErrMissingWorkflowID
	}
	if _, err := s.ensureDB(); err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM manual_transfers WHERE tenant_id = ? AND workflow_id = ?")
	var transfer ManualTransfer
	if err := s.DB.GetContext(ctx, &transfer, stmt, tenantID, workflowID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrManualTransferNotFound
		}
		return nil, err
	}
	return &transfer, nil
}
