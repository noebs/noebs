package store

import (
	"context"
	"database/sql"
	"time"
)

type PSPConfig struct {
	ID                    int64          `db:"id"`
	TenantID              string         `db:"tenant_id"`
	ProviderCode          string         `db:"provider_code"`
	ProviderName          string         `db:"provider_name"`
	APIBaseURL            string         `db:"api_base_url"`
	EnabledCurrencies     []string       `db:"enabled_currencies"`
	IdempotencyHeaderName sql.NullString `db:"idempotency_header_name"`
	IsActive              bool           `db:"is_active"`
	SupportsDeposit       bool           `db:"supports_deposit"`
	SupportsWithdrawal    bool           `db:"supports_withdrawal"`
	CreatedAt             time.Time      `db:"created_at"`
}

func (s *Store) GetPSPConfig(ctx context.Context, tenantID, providerCode string) (*PSPConfig, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if providerCode == "" {
		return nil, ErrMissingProviderCode
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM psp_configs WHERE tenant_id = ? AND provider_code = ?")
	var cfg PSPConfig
	if err := db.GetContext(ctx, &cfg, stmt, tenantID, providerCode); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrPSPConfigNotFound
		}
		return nil, err
	}
	return &cfg, nil
}
