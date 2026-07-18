package store

import (
	"database/sql"
	"strings"
	"time"
)

const (
	DestinationOwnershipStatusUnverified = "unverified"
	DestinationOwnershipStatusPending    = "pending"
	DestinationOwnershipStatusVerified   = "verified"
	DestinationOwnershipStatusRejected   = "rejected"

	OwnershipVerificationStatusPending  = "pending"
	OwnershipVerificationStatusVerified = "verified"
	OwnershipVerificationStatusFailed   = "failed"
	OwnershipVerificationStatusExpired  = "expired"
)

func ValidateDestinationOwnershipStatus(status string) error {
	switch status {
	case "":
		return ErrMissingStatus
	case DestinationOwnershipStatusUnverified,
		DestinationOwnershipStatusPending,
		DestinationOwnershipStatusVerified,
		DestinationOwnershipStatusRejected:
		return nil
	default:
		return ErrInvalidStatus
	}
}

func ValidateWithdrawalDestinationOwnership(dest WithdrawalDestination) error {
	if err := ValidateDestinationOwnershipStatus(dest.OwnershipStatus); err != nil {
		return err
	}
	if dest.OwnershipStatus == DestinationOwnershipStatusVerified {
		if !dest.OwnershipVerifiedAt.Valid || dest.OwnershipVerifiedAt.Time.IsZero() {
			return ErrMissingVerificationTime
		}
		return nil
	}
	if dest.OwnershipVerifiedAt.Valid {
		return ErrInvalidVerificationTime
	}
	return nil
}

func ValidateWithdrawalDestinationReadyForWithdrawal(dest *WithdrawalDestination) error {
	if dest == nil {
		return ErrDestinationNotFound
	}
	if dest.OwnershipStatus != DestinationOwnershipStatusVerified {
		return ErrDestinationNotVerified
	}
	if !dest.OwnershipVerifiedAt.Valid || dest.OwnershipVerifiedAt.Time.IsZero() {
		return ErrMissingVerificationTime
	}
	return nil
}

func ValidateWithdrawalDestinationOwnershipTransition(current *WithdrawalDestination, next WithdrawalDestination) error {
	if current == nil {
		return ErrDestinationNotFound
	}
	if err := ValidateWithdrawalDestinationOwnership(*current); err != nil {
		return err
	}
	if err := ValidateWithdrawalDestinationOwnership(next); err != nil {
		return err
	}
	if current.OwnershipStatus == DestinationOwnershipStatusVerified {
		if next.OwnershipStatus != DestinationOwnershipStatusVerified {
			return ErrInvalidStatusTransition
		}
		if !nullTimeEqual(current.OwnershipVerifiedAt, next.OwnershipVerifiedAt) {
			return ErrInvalidStatusTransition
		}
		return nil
	}
	return nil
}

func ValidateOwnershipVerificationDestination(destination *WithdrawalDestination, verification OwnershipVerification) error {
	if destination == nil ||
		destination.TenantID != verification.TenantID ||
		destination.ID != verification.DestinationID {
		return ErrDestinationNotFound
	}
	if !destination.IsActive {
		return ErrDestinationNotFound
	}
	if destination.OwnershipStatus == DestinationOwnershipStatusVerified {
		return ErrInvalidStatusTransition
	}
	method := strings.TrimSpace(destination.OwnershipVerificationMethod.String)
	if !destination.OwnershipVerificationMethod.Valid || method == "" {
		return ErrMissingVerificationType
	}
	if strings.TrimSpace(verification.VerificationType) != method {
		return ErrInvalidVerificationType
	}
	return nil
}

func ValidateOwnershipVerificationStatus(status string) error {
	switch status {
	case "":
		return ErrMissingStatus
	case OwnershipVerificationStatusPending,
		OwnershipVerificationStatusVerified,
		OwnershipVerificationStatusFailed,
		OwnershipVerificationStatusExpired:
		return nil
	default:
		return ErrInvalidStatus
	}
}

type OwnershipVerification struct {
	ID                      int64          `db:"id"`
	TenantID                string         `db:"tenant_id"`
	DestinationID           int64          `db:"destination_id"`
	VerificationType        string         `db:"verification_type"`
	Status                  string         `db:"status"`
	MicroDepositAmounts     Int64Array     `db:"micro_deposit_amounts"`
	MicroDepositConfirmedAt sql.NullTime   `db:"micro_deposit_confirmed_at"`
	CardVerificationAmount  sql.NullInt64  `db:"card_verification_amount"`
	DocumentType            sql.NullString `db:"document_type"`
	DocumentURL             sql.NullString `db:"document_url"`
	Attempts                int            `db:"attempts"`
	MaxAttempts             int            `db:"max_attempts"`
	ExpiresAt               time.Time      `db:"expires_at"`
	CompletedAt             sql.NullTime   `db:"completed_at"`
	WorkflowID              sql.NullString `db:"workflow_id"`
	ReferenceID             sql.NullString `db:"reference_id"`
	CreatedAt               time.Time      `db:"created_at"`
	UpdatedAt               sql.NullTime   `db:"updated_at"`
}
