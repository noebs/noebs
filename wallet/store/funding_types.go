package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	FundingSourceStatusUnverified = "unverified"
	FundingSourceStatusPending    = "pending"
	FundingSourceStatusVerified   = "verified"
)

type FundingSource struct {
	ID                 int64           `db:"id"`
	TenantID           string          `db:"tenant_id"`
	WalletID           uuid.UUID       `db:"wallet_id"`
	SourceType         string          `db:"source_type"`
	PSPProvider        sql.NullString  `db:"psp_provider"`
	ExternalReference  sql.NullString  `db:"external_reference"`
	VerificationStatus string          `db:"verification_status"`
	VerifiedAt         sql.NullTime    `db:"verified_at"`
	VerifiedBy         sql.NullString  `db:"verified_by"`
	Currency           string          `db:"currency"`
	SourceDetails      json.RawMessage `db:"source_details"`
	TotalFunded        int64           `db:"total_funded"`
	LastFundedAt       sql.NullTime    `db:"last_funded_at"`
	TotalWithdrawn     int64           `db:"total_withdrawn"`
	LastWithdrawnAt    sql.NullTime    `db:"last_withdrawn_at"`
	SupportsWithdrawal bool            `db:"supports_withdrawal"`
	WithdrawalMethod   json.RawMessage `db:"withdrawal_method"`
	CreatedAt          time.Time       `db:"created_at"`
	UpdatedAt          time.Time       `db:"updated_at"`
}

func ValidateFundingSourceVerificationStatus(status string) error {
	switch status {
	case "":
		return ErrMissingStatus
	case FundingSourceStatusUnverified,
		FundingSourceStatusPending,
		FundingSourceStatusVerified:
		return nil
	default:
		return ErrInvalidStatus
	}
}

func ValidateFundingSourceVerification(source FundingSource) error {
	if err := ValidateFundingSourceVerificationStatus(source.VerificationStatus); err != nil {
		return err
	}
	if source.VerificationStatus == FundingSourceStatusVerified {
		if !source.VerifiedAt.Valid || source.VerifiedAt.Time.IsZero() {
			return ErrMissingVerificationTime
		}
		return nil
	}
	if source.VerifiedAt.Valid {
		return ErrInvalidVerificationTime
	}
	return nil
}

func ValidateFundingSourceReadyForWithdrawal(source *FundingSource) error {
	if source == nil {
		return ErrFundingSourceNotFound
	}
	if source.VerificationStatus != FundingSourceStatusVerified {
		return ErrFundingSourceNotVerified
	}
	if !source.VerifiedAt.Valid || source.VerifiedAt.Time.IsZero() {
		return ErrMissingVerificationTime
	}
	if !source.SupportsWithdrawal || len(source.WithdrawalMethod) == 0 {
		return ErrFundingSourceNotWithdrawable
	}
	if !source.PSPProvider.Valid || source.PSPProvider.String == "" || source.PSPProvider.String != strings.TrimSpace(source.PSPProvider.String) {
		return ErrMissingProviderCode
	}
	return nil
}

type LedgerFundingLink struct {
	ID                      int64         `db:"id"`
	TenantID                string        `db:"tenant_id"`
	LedgerEntryID           int64         `db:"ledger_entry_id"`
	FundingSourceID         int64         `db:"funding_source_id"`
	WithdrawalReservationID sql.NullInt64 `db:"withdrawal_reservation_id"`
	Amount                  int64         `db:"amount"`
	Currency                string        `db:"currency"`
	CreatedAt               time.Time     `db:"created_at"`
}

const (
	FundingSourceReservationReserved = "reserved"
	FundingSourceReservationConsumed = "consumed"
	FundingSourceReservationReleased = "released"
)

type FundingSourceWithdrawalReservation struct {
	ID              int64         `db:"id"`
	TenantID        string        `db:"tenant_id"`
	WorkflowID      string        `db:"workflow_id"`
	FundingSourceID int64         `db:"funding_source_id"`
	Amount          int64         `db:"amount"`
	Currency        string        `db:"currency"`
	ProviderCode    string        `db:"provider_code"`
	Status          string        `db:"status"`
	LedgerEntryID   sql.NullInt64 `db:"ledger_entry_id"`
	CreatedAt       time.Time     `db:"created_at"`
	ConsumedAt      sql.NullTime  `db:"consumed_at"`
	ReleasedAt      sql.NullTime  `db:"released_at"`
}

type ReserveFundingSourceWithdrawalParams struct {
	TenantID           string
	WorkflowID         string
	CandidateSourceIDs []int64
	WalletID           uuid.UUID
	Amount             int64
	Currency           string
	ProviderCode       string
}

type FundingSourceWithdrawalReservationResult struct {
	Reservation FundingSourceWithdrawalReservation
	Source      FundingSource
}

type ReleaseFundingSourceWithdrawalParams struct {
	TenantID   string
	WorkflowID string
	ReleasedAt time.Time
}
