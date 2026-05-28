package store

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) GetPSPConfigOverride(ctx context.Context, tenantID, providerCode string, scope PSPConfigScope) (*PSPConfigOverride, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
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
	if override.WebhookAuthMode.Valid {
		merged.WebhookAuthMode = override.WebhookAuthMode.String
	}
	if override.WebhookAllowedCIDRs != nil {
		merged.WebhookAllowedCIDRs = override.WebhookAllowedCIDRs
	}
	if override.StatusCheckWebhook.Valid {
		merged.StatusCheckWebhook = override.StatusCheckWebhook.Bool
	}
	if override.MethodType.Valid {
		merged.MethodType = override.MethodType.String
	}
	if override.DisplayName.Valid {
		merged.DisplayName = override.DisplayName
	}
	if override.SupportedRegions != nil {
		merged.SupportedRegions = override.SupportedRegions
	}
	if override.MinAmount.Valid {
		merged.MinAmount = override.MinAmount
	}
	if override.MaxAmount.Valid {
		merged.MaxAmount = override.MaxAmount
	}
	if len(override.DepositInputSchema) > 0 {
		merged.DepositInputSchema = override.DepositInputSchema
	}
	if len(override.WithdrawalInputSchema) > 0 {
		merged.WithdrawalInputSchema = override.WithdrawalInputSchema
	}
	if len(override.PresentationSchema) > 0 {
		merged.PresentationSchema = override.PresentationSchema
	}
	if override.DepositRequestMethod.Valid {
		merged.DepositRequestMethod = override.DepositRequestMethod.String
	}
	if override.DepositRequestPath.Valid {
		merged.DepositRequestPath = override.DepositRequestPath.String
	}
	if len(override.DepositRequestMapping) > 0 {
		merged.DepositRequestMapping = override.DepositRequestMapping
	}
	if override.PayoutRequestMethod.Valid {
		merged.PayoutRequestMethod = override.PayoutRequestMethod.String
	}
	if override.PayoutRequestPath.Valid {
		merged.PayoutRequestPath = override.PayoutRequestPath.String
	}
	if len(override.PayoutRequestMapping) > 0 {
		merged.PayoutRequestMapping = override.PayoutRequestMapping
	}
	if override.StatusRequestMethod.Valid {
		merged.StatusRequestMethod = override.StatusRequestMethod.String
	}
	if override.StatusRequestPath.Valid {
		merged.StatusRequestPath = override.StatusRequestPath.String
	}
	if len(override.StatusRequestMapping) > 0 {
		merged.StatusRequestMapping = override.StatusRequestMapping
	}
	if len(override.DepositResponseMapping) > 0 {
		merged.DepositResponseMapping = override.DepositResponseMapping
	}
	if len(override.PayoutResponseMapping) > 0 {
		merged.PayoutResponseMapping = override.PayoutResponseMapping
	}
	if len(override.StatusResponseMapping) > 0 {
		merged.StatusResponseMapping = override.StatusResponseMapping
	}
	if len(override.WebhookResponseMapping) > 0 {
		merged.WebhookResponseMapping = override.WebhookResponseMapping
	}
	return &merged, override, nil
}
