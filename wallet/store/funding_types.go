package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
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

type LedgerFundingLink struct {
	ID              int64     `db:"id"`
	TenantID        string    `db:"tenant_id"`
	LedgerEntryID   int64     `db:"ledger_entry_id"`
	FundingSourceID int64     `db:"funding_source_id"`
	Amount          int64     `db:"amount"`
	Currency        string    `db:"currency"`
	CreatedAt       time.Time `db:"created_at"`
}
