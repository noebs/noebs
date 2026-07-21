package store

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

const (
	HoldStatusActive    = "active"
	HoldStatusCommitted = "committed"
	HoldStatusReleased  = "released"
	HoldStatusExpired   = "expired"
	HoldStatusCaptured  = "captured"
)

const (
	WalletStatusActive = "active"
	WalletStatusFrozen = "frozen"
	WalletStatusClosed = "closed"
)

type Wallet struct {
	ID               uuid.UUID     `db:"id"`
	TenantID         string        `db:"tenant_id"`
	OwnerType        string        `db:"owner_type"`
	OwnerID          string        `db:"owner_id"`
	UserID           sql.NullInt64 `db:"user_id"`
	Currency         string        `db:"currency"`
	CurrencyUnitID   int64         `db:"currency_unit_version_id"`
	Balance          int64         `db:"balance"`
	AvailableBalance int64         `db:"available_balance"`
	Status           string        `db:"status"`
	KYCTier          string        `db:"kyc_tier"`
	Version          int64         `db:"version"`
	CreatedAt        time.Time     `db:"created_at"`
	UpdatedAt        time.Time     `db:"updated_at"`
}

type LedgerTransaction struct {
	ID             int64          `db:"id"`
	TenantID       string         `db:"tenant_id"`
	IdempotencyKey string         `db:"idempotency_key"`
	Currency       string         `db:"currency"`
	CurrencyUnitID int64          `db:"currency_unit_version_id"`
	ReferenceType  string         `db:"reference_type"`
	ReferenceID    sql.NullString `db:"reference_id"`
	Status         string         `db:"status"`
	Metadata       RawJSON        `db:"metadata"`
	CreatedAt      time.Time      `db:"created_at"`
}

type LedgerEntry struct {
	ID             int64          `db:"id"`
	TenantID       string         `db:"tenant_id"`
	TransactionID  int64          `db:"transaction_id"`
	WalletID       uuid.UUID      `db:"wallet_id"`
	EntryType      string         `db:"entry_type"`
	Amount         int64          `db:"amount"`
	Currency       string         `db:"currency"`
	CurrencyUnitID int64          `db:"currency_unit_version_id"`
	BalanceAfter   int64          `db:"balance_after"`
	WalletSeq      int64          `db:"wallet_sequence"`
	Status         string         `db:"status"`
	CounterID      sql.NullInt64  `db:"counter_entry_id"`
	Description    sql.NullString `db:"description"`
	Metadata       RawJSON        `db:"metadata"`
	CreatedAt      time.Time      `db:"created_at"`
}

type BalanceHold struct {
	ID              int64        `db:"id"`
	TenantID        string       `db:"tenant_id"`
	WalletID        uuid.UUID    `db:"wallet_id"`
	Amount          int64        `db:"amount"`
	AmountRemaining int64        `db:"amount_remaining"`
	Reason          string       `db:"reason"`
	ReferenceType   string       `db:"reference_type"`
	ReferenceID     string       `db:"reference_id"`
	IdempotencyKey  string       `db:"idempotency_key"`
	Status          string       `db:"status"`
	ExpiresAt       time.Time    `db:"expires_at"`
	ReleasedAt      sql.NullTime `db:"released_at"`
	CommittedAt     sql.NullTime `db:"committed_at"`
	ExpiredAt       sql.NullTime `db:"expired_at"`
	CapturedAt      sql.NullTime `db:"captured_at"`
	CreatedAt       time.Time    `db:"created_at"`
	Metadata        RawJSON      `db:"metadata"`
}
