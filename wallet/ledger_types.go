package wallet

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type LedgerTransaction struct {
	ID             int64           `db:"id"`
	TenantID       string          `db:"tenant_id"`
	IdempotencyKey string          `db:"idempotency_key"`
	Currency       string          `db:"currency"`
	ReferenceType  string          `db:"reference_type"`
	ReferenceID    sql.NullString  `db:"reference_id"`
	Status         string          `db:"status"`
	Metadata       json.RawMessage `db:"metadata"`
	CreatedAt      time.Time       `db:"created_at"`
}

type LedgerEntry struct {
	ID            int64           `db:"id"`
	TenantID      string          `db:"tenant_id"`
	TransactionID int64           `db:"transaction_id"`
	WalletID      uuid.UUID       `db:"wallet_id"`
	EntryType     string          `db:"entry_type"`
	Amount        int64           `db:"amount"`
	Currency      string          `db:"currency"`
	BalanceAfter  int64           `db:"balance_after"`
	WalletSeq     int64           `db:"wallet_sequence"`
	Status        string          `db:"status"`
	CounterID     sql.NullInt64   `db:"counter_entry_id"`
	Description   sql.NullString  `db:"description"`
	Metadata      json.RawMessage `db:"metadata"`
	CreatedAt     time.Time       `db:"created_at"`
}

type BalanceHold struct {
	ID              int64           `db:"id"`
	TenantID        string          `db:"tenant_id"`
	WalletID        uuid.UUID       `db:"wallet_id"`
	Amount          int64           `db:"amount"`
	AmountRemaining int64           `db:"amount_remaining"`
	Reason          string          `db:"reason"`
	ReferenceType   string          `db:"reference_type"`
	ReferenceID     string          `db:"reference_id"`
	IdempotencyKey  string          `db:"idempotency_key"`
	Status          string          `db:"status"`
	ExpiresAt       time.Time       `db:"expires_at"`
	ReleasedAt      sql.NullTime    `db:"released_at"`
	CreatedAt       time.Time       `db:"created_at"`
	Metadata        json.RawMessage `db:"metadata"`
}
