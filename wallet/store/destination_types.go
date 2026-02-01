package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type WithdrawalDestination struct {
	ID                          int64           `db:"id"`
	TenantID                    string          `db:"tenant_id"`
	WalletID                    uuid.UUID       `db:"wallet_id"`
	DestinationType             string          `db:"destination_type"`
	PSPProvider                 sql.NullString  `db:"psp_provider"`
	DestinationDetails          json.RawMessage `db:"destination_details"`
	DisplayName                 sql.NullString  `db:"display_name"`
	Currency                    string          `db:"currency"`
	Country                     sql.NullString  `db:"country"`
	OwnershipStatus             string          `db:"ownership_status"`
	OwnershipVerificationMethod sql.NullString  `db:"ownership_verification_method"`
	OwnershipVerifiedAt         sql.NullTime    `db:"ownership_verified_at"`
	OwnershipVerifiedBy         sql.NullString  `db:"ownership_verified_by"`
	OwnershipProof              json.RawMessage `db:"ownership_proof"`
	LinkedFundingSourceID       sql.NullInt64   `db:"linked_funding_source_id"`
	IsReturnToSource            bool            `db:"is_return_to_source"`
	IsActive                    bool            `db:"is_active"`
	LastUsedAt                  sql.NullTime    `db:"last_used_at"`
	TotalWithdrawn              int64           `db:"total_withdrawn"`
	CreatedAt                   time.Time       `db:"created_at"`
	UpdatedAt                   time.Time       `db:"updated_at"`
}
