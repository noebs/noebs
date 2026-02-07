package store

import (
	"database/sql"
	"time"
)

type PSPConfigScope struct {
	Region    string
	Currency  string
	Direction string
}

type PSPConfigOverride struct {
	ID                 int64          `db:"id"`
	TenantID           string         `db:"tenant_id"`
	ProviderCode       string         `db:"provider_code"`
	Region             sql.NullString `db:"region"`
	Currency           sql.NullString `db:"currency"`
	Direction          sql.NullString `db:"direction"`
	IsActive           bool           `db:"is_active"`
	SupportsDeposit    bool           `db:"supports_deposit"`
	SupportsWithdrawal bool           `db:"supports_withdrawal"`
	EnabledCurrencies  []string       `db:"enabled_currencies"`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
}
