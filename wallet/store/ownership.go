package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) CreateOwnershipVerification(ctx context.Context, verification OwnershipVerification) (*OwnershipVerification, error) {
	tenantID, err := ValidateTenantID(verification.TenantID)
	if err != nil {
		return nil, err
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
	if verification.MaxAttempts <= 0 {
		return nil, ErrMissingMaxAttempts
	}
	if verification.ExpiresAt.IsZero() {
		return nil, ErrMissingVerificationExpiry
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO ownership_verifications(
		tenant_id, destination_id, verification_type, status, micro_deposit_amounts,
		micro_deposit_confirmed_at, card_verification_amount, document_type, document_url,
		attempts, max_attempts, expires_at, completed_at, workflow_id, reference_id, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored OwnershipVerification
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
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
		now,
	); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *Store) GetOwnershipVerification(ctx context.Context, tenantID string, verificationID int64) (*OwnershipVerification, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if verificationID <= 0 {
		return nil, ErrMissingVerificationID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM ownership_verifications WHERE tenant_id = ? AND id = ?")
	var verification OwnershipVerification
	if err := db.GetContext(ctx, &verification, stmt, tenantID, verificationID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrVerificationNotFound
		}
		return nil, err
	}
	return &verification, nil
}

func (s *Store) UpdateOwnershipVerificationStatus(ctx context.Context, tenantID string, verificationID int64, status string, completedAt time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if verificationID <= 0 {
		return ErrMissingVerificationID
	}
	if status == "" {
		return ErrMissingStatus
	}
	if completedAt.IsZero() {
		return ErrMissingVerificationTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := db.Rebind(`UPDATE ownership_verifications
		SET status = ?, completed_at = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	result, err := db.ExecContext(ctx, stmt, status, completedAt, completedAt, tenantID, verificationID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrVerificationNotFound
	}
	return nil
}
