package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) CreateOwnershipVerification(ctx context.Context, verification OwnershipVerification) (*OwnershipVerification, error) {
	if verification.TenantID == "" {
		return nil, ErrMissingTenantID
	}
	if verification.DestinationID <= 0 {
		return nil, ErrMissingDestinationID
	}
	if verification.VerificationType == "" {
		return nil, ErrMissingVerificationType
	}
	if verification.Status == "" {
		return nil, ErrMissingStatus
	}
	if verification.ExpiresAt.IsZero() {
		return nil, ErrMissingVerificationExpiry
	}
	if _, err := s.ensureDB(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO ownership_verifications(
		tenant_id, destination_id, verification_type, status, micro_deposit_amounts,
		micro_deposit_confirmed_at, card_verification_amount, document_type, document_url,
		attempts, max_attempts, expires_at, completed_at, workflow_id, reference_id, created_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored OwnershipVerification
	if err := s.DB.GetContext(ctx, &stored, stmt,
		verification.TenantID,
		verification.DestinationID,
		verification.VerificationType,
		verification.Status,
		verification.MicroDepositAmounts,
		verification.MicroDepositConfirmedAt,
		verification.CardVerificationAmount,
		verification.DocumentType,
		verification.DocumentURL,
		verification.Attempts,
		verification.MaxAttempts,
		verification.ExpiresAt,
		verification.CompletedAt,
		verification.WorkflowID,
		verification.ReferenceID,
		now,
	); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *Store) GetOwnershipVerification(ctx context.Context, tenantID string, verificationID int64) (*OwnershipVerification, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if verificationID <= 0 {
		return nil, ErrMissingVerificationID
	}
	if _, err := s.ensureDB(); err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM ownership_verifications WHERE tenant_id = ? AND id = ?")
	var verification OwnershipVerification
	if err := s.DB.GetContext(ctx, &verification, stmt, tenantID, verificationID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrVerificationNotFound
		}
		return nil, err
	}
	return &verification, nil
}
