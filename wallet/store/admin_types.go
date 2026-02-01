package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

type AdminRole struct {
	ID          int64           `db:"id"`
	TenantID    string          `db:"tenant_id"`
	RoleName    string          `db:"role_name"`
	RoleLevel   int             `db:"role_level"`
	Permissions json.RawMessage `db:"permissions"`
}

type AdminUser struct {
	ID           int64          `db:"id"`
	TenantID     string         `db:"tenant_id"`
	Email        string         `db:"email"`
	PasswordHash string         `db:"password_hash"`
	RoleID       sql.NullInt64  `db:"role_id"`
	IsActive     bool           `db:"is_active"`
	TOTPSecret   sql.NullString `db:"totp_secret"`
	LastLoginAt  sql.NullTime   `db:"last_login_at"`
	CreatedAt    time.Time      `db:"created_at"`
}
