package store

import (
	"database/sql"
	"time"
)

type UserTwoFA struct {
	TenantID   string       `db:"tenant_id"`
	UserID     int64        `db:"user_id"`
	Secret     string       `db:"secret"`
	Enabled    bool         `db:"enabled"`
	CreatedAt  time.Time    `db:"created_at"`
	UpdatedAt  time.Time    `db:"updated_at"`
	EnabledAt  sql.NullTime `db:"enabled_at"`
	DisabledAt sql.NullTime `db:"disabled_at"`
	LastUsedAt sql.NullTime `db:"last_used_at"`
}
