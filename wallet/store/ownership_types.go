package store

import (
	"database/sql"
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
	MicroDepositAmounts     []int64        `db:"micro_deposit_amounts"`
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
