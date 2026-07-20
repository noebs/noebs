package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type WithdrawalDestination struct {
	ID                    int64           `db:"id"`
	TenantID              string          `db:"tenant_id"`
	WalletID              uuid.UUID       `db:"wallet_id"`
	DestinationType       string          `db:"destination_type"`
	PSPProvider           sql.NullString  `db:"psp_provider"`
	DestinationDetails    json.RawMessage `db:"destination_details"`
	DisplayName           sql.NullString  `db:"display_name"`
	Currency              string          `db:"currency"`
	Country               sql.NullString  `db:"country"`
	LinkedFundingSourceID int64           `db:"linked_funding_source_id"`
	IsActive              bool            `db:"is_active"`
	LastUsedAt            sql.NullTime    `db:"last_used_at"`
	TotalWithdrawn        int64           `db:"total_withdrawn"`
	CreatedAt             time.Time       `db:"created_at"`
	UpdatedAt             time.Time       `db:"updated_at"`
}

type LedgerWithdrawalDestinationLink struct {
	ID            int64     `db:"id"`
	TenantID      string    `db:"tenant_id"`
	LedgerEntryID int64     `db:"ledger_entry_id"`
	DestinationID int64     `db:"destination_id"`
	Amount        int64     `db:"amount"`
	Currency      string    `db:"currency"`
	CreatedAt     time.Time `db:"created_at"`
}

func ValidateWithdrawalDestinationReadyForWithdrawal(dest *WithdrawalDestination) error {
	if dest == nil || !dest.IsActive {
		return ErrDestinationNotFound
	}
	if dest.LinkedFundingSourceID <= 0 {
		return ErrDestinationNotVerified
	}
	return nil
}
