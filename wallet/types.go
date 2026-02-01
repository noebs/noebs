package wallet

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Wallet struct {
	ID               uuid.UUID      `db:"id"`
	TenantID         string         `db:"tenant_id"`
	OwnerType        string         `db:"owner_type"`
	OwnerID          string         `db:"owner_id"`
	UserID           sql.NullInt64  `db:"user_id"`
	Currency         string         `db:"currency"`
	Balance          int64          `db:"balance"`
	AvailableBalance int64          `db:"available_balance"`
	Status           string         `db:"status"`
	WalletPinHash    sql.NullString `db:"wallet_pin_hash"`
	KYCTier          string         `db:"kyc_tier"`
	Version          int64          `db:"version"`
	CreatedAt        time.Time      `db:"created_at"`
	UpdatedAt        time.Time      `db:"updated_at"`
}
