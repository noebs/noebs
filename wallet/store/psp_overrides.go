package store

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) GetPSPConfigOverride(ctx context.Context, tenantID, providerCode string, scope PSPConfigScope) (*PSPConfigOverride, error) {
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
	stmt := db.Rebind(`SELECT * FROM psp_config_overrides
		WHERE tenant_id = ? AND provider_code = ?
		AND (region IS NULL OR region = ?)
		AND (currency IS NULL OR currency = ?)
		AND (direction IS NULL OR direction = ?)
		ORDER BY (CASE WHEN region IS NULL THEN 0 ELSE 1 END
			+ CASE WHEN currency IS NULL THEN 0 ELSE 1 END
			+ CASE WHEN direction IS NULL THEN 0 ELSE 1 END) DESC,
			updated_at DESC
		LIMIT 1`)
	var override PSPConfigOverride
	if err := db.GetContext(ctx, &override, stmt, tenantID, providerCode, scope.Region, scope.Currency, scope.Direction); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrPSPConfigOverrideNotFound
		}
		return nil, err
	}
	return &override, nil
}

func (s *Store) ResolvePSPConfig(ctx context.Context, tenantID, providerCode string, scope PSPConfigScope) (*PSPConfig, *PSPConfigOverride, error) {
	cfg, err := s.GetPSPConfig(ctx, tenantID, providerCode)
	if err != nil {
		return nil, nil, err
	}
	override, err := s.GetPSPConfigOverride(ctx, tenantID, providerCode, scope)
	if err != nil {
		if errors.Is(err, ErrPSPConfigOverrideNotFound) {
			return cfg, nil, nil
		}
		return nil, nil, err
	}
	merged := *cfg
	merged.IsActive = override.IsActive
	merged.SupportsDeposit = override.SupportsDeposit
	merged.SupportsWithdrawal = override.SupportsWithdrawal
	if override.EnabledCurrencies != nil {
		merged.EnabledCurrencies = override.EnabledCurrencies
	}
	return &merged, override, nil
}
