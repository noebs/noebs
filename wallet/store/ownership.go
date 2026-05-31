package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
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
	if err := ValidateOwnershipVerificationStatus(verification.Status); err != nil {
		return nil, err
	}
	if verification.Status != OwnershipVerificationStatusPending {
		return nil, ErrInvalidStatus
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
	ON CONFLICT DO NOTHING
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
		if errors.Is(err, sql.ErrNoRows) {
			existing, replayErr := s.getOwnershipVerificationByReplayKey(ctx, tenantID, verification)
			if replayErr != nil {
				return nil, replayErr
			}
			if err := ValidateOwnershipVerificationCreateReplay(existing, verification); err != nil {
				return nil, err
			}
			return existing, nil
		}
		return nil, err
	}
	return &stored, nil
}

func (s *Store) getOwnershipVerificationByReplayKey(ctx context.Context, tenantID string, verification OwnershipVerification) (*OwnershipVerification, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if verification.WorkflowID.Valid && strings.TrimSpace(verification.WorkflowID.String) != "" {
		stmt := db.Rebind("SELECT * FROM ownership_verifications WHERE tenant_id = ? AND destination_id = ? AND workflow_id = ?")
		var existing OwnershipVerification
		if err := db.GetContext(ctx, &existing, stmt, tenantID, verification.DestinationID, verification.WorkflowID.String); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		} else {
			return &existing, nil
		}
	}
	if verification.ReferenceID.Valid && strings.TrimSpace(verification.ReferenceID.String) != "" {
		stmt := db.Rebind("SELECT * FROM ownership_verifications WHERE tenant_id = ? AND destination_id = ? AND reference_id = ?")
		var existing OwnershipVerification
		if err := db.GetContext(ctx, &existing, stmt, tenantID, verification.DestinationID, verification.ReferenceID.String); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrDuplicateVerification
			}
			return nil, err
		}
		return &existing, nil
	}
	return nil, ErrDuplicateVerification
}

func ValidateOwnershipVerificationCreateReplay(existing *OwnershipVerification, requested OwnershipVerification) error {
	if existing == nil ||
		existing.TenantID != requested.TenantID ||
		existing.DestinationID != requested.DestinationID ||
		existing.VerificationType != requested.VerificationType ||
		!slices.Equal(existing.MicroDepositAmounts, requested.MicroDepositAmounts) ||
		!nullTimeEqual(existing.MicroDepositConfirmedAt, requested.MicroDepositConfirmedAt) ||
		!nullInt64Equal(existing.CardVerificationAmount, requested.CardVerificationAmount) ||
		!nullStringEqual(existing.DocumentType, requested.DocumentType) ||
		!nullStringEqual(existing.DocumentURL, requested.DocumentURL) ||
		existing.MaxAttempts != requested.MaxAttempts ||
		!timeEqualAtDBPrecision(existing.ExpiresAt, requested.ExpiresAt) ||
		!nullStringEqual(existing.WorkflowID, requested.WorkflowID) ||
		!nullStringEqual(existing.ReferenceID, requested.ReferenceID) {
		return ErrDuplicateVerification
	}
	return nil
}

func ValidateOwnershipVerificationStatusTransition(current, next string) error {
	if err := ValidateOwnershipVerificationStatus(current); err != nil {
		return err
	}
	if err := ValidateOwnershipVerificationStatus(next); err != nil {
		return err
	}
	if current == OwnershipVerificationStatusPending && next != OwnershipVerificationStatusPending {
		return nil
	}
	if current == next && current != OwnershipVerificationStatusPending {
		return nil
	}
	return ErrInvalidStatusTransition
}

func nullTimeEqual(left, right sql.NullTime) bool {
	return left.Valid == right.Valid && (!left.Valid || timeEqualAtDBPrecision(left.Time, right.Time))
}

func timeEqualAtDBPrecision(left, right time.Time) bool {
	diff := left.Sub(right)
	if diff < 0 {
		diff = -diff
	}
	return diff < time.Microsecond
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
	if err := ValidateOwnershipVerificationStatus(status); err != nil {
		return err
	}
	if completedAt.IsZero() {
		return ErrMissingVerificationTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	existing, err := s.GetOwnershipVerification(ctx, tenantID, verificationID)
	if err != nil {
		return err
	}
	if err := ValidateOwnershipVerificationStatusTransition(existing.Status, status); err != nil {
		return err
	}
	if existing.Status == status {
		if !existing.CompletedAt.Valid || !timeEqualAtDBPrecision(existing.CompletedAt.Time, completedAt) {
			return ErrInvalidStatusTransition
		}
		return nil
	}
	stmt := db.Rebind(`UPDATE ownership_verifications
		SET status = ?, completed_at = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND status = ?`)
	result, err := db.ExecContext(ctx, stmt, status, completedAt, completedAt, tenantID, verificationID, existing.Status)
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
