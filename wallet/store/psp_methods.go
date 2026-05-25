package store

import (
	"context"
	"strings"
)

func (s *Store) ListAvailablePSPMethods(ctx context.Context, filter PSPMethodFilter) ([]PSPPaymentMethod, error) {
	if filter.TenantID == "" {
		return nil, ErrMissingTenantID
	}
	direction := normalizeMethodDirection(filter.Direction)
	if direction == "" {
		return nil, ErrMissingDirection
	}
	if direction != "deposit" && direction != "withdrawal" {
		return nil, ErrInvalidDirection
	}
	if filter.Amount < 0 {
		return nil, ErrInvalidAmount
	}
	if filter.Limit <= 0 {
		return nil, ErrInvalidLimit
	}
	if filter.Offset < 0 {
		return nil, ErrInvalidOffset
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM psp_configs
		WHERE tenant_id = ? AND is_active = TRUE
		ORDER BY provider_name ASC, provider_code ASC
		LIMIT ? OFFSET ?`)
	var configs []PSPConfig
	if err := db.SelectContext(ctx, &configs, stmt, filter.TenantID, filter.Limit, filter.Offset); err != nil {
		return nil, err
	}

	methods := make([]PSPPaymentMethod, 0, len(configs))
	scope := PSPConfigScope{
		Region:    filter.Region,
		Currency:  filter.Currency,
		Direction: direction,
	}
	for _, base := range configs {
		cfg, _, err := s.ResolvePSPConfig(ctx, filter.TenantID, base.ProviderCode, scope)
		if err != nil {
			return nil, err
		}
		if !methodSupportsDirection(cfg, direction) {
			continue
		}
		if filter.Currency != "" && !methodSupportsCurrency(cfg, filter.Currency) {
			continue
		}
		if filter.Region != "" && !methodSupportsRegion(cfg, filter.Region) {
			continue
		}
		if filter.Amount > 0 && !methodSupportsAmount(cfg, filter.Amount) {
			continue
		}
		methods = append(methods, pspPaymentMethodFromConfig(cfg, direction, filter.Currency))
	}
	return methods, nil
}

func pspPaymentMethodFromConfig(cfg *PSPConfig, direction, currency string) PSPPaymentMethod {
	method := PSPPaymentMethod{
		TenantID:         cfg.TenantID,
		ProviderCode:     cfg.ProviderCode,
		ProviderName:     cfg.ProviderName,
		DisplayName:      "",
		MethodType:       cfg.MethodType,
		Direction:        direction,
		Currencies:       []string(cfg.EnabledCurrencies),
		Regions:          []string(cfg.SupportedRegions),
		MinAmount:        cfg.MinAmount,
		MaxAmount:        cfg.MaxAmount,
		Presentation:     cfg.PresentationSchema,
		SupportsDeposit:  cfg.SupportsDeposit,
		SupportsWithdraw: cfg.SupportsWithdrawal,
	}
	if cfg.DisplayName.Valid {
		method.DisplayName = cfg.DisplayName.String
	}
	if currency != "" {
		method.Currencies = []string{currency}
	}
	switch direction {
	case "deposit":
		method.InputSchema = cfg.DepositInputSchema
	case "withdrawal":
		method.InputSchema = cfg.WithdrawalInputSchema
	}
	return method
}

func methodSupportsDirection(cfg *PSPConfig, direction string) bool {
	if cfg == nil || !cfg.IsActive {
		return false
	}
	switch direction {
	case "deposit":
		return cfg.SupportsDeposit
	case "withdrawal":
		return cfg.SupportsWithdrawal
	default:
		return false
	}
}

func methodSupportsCurrency(cfg *PSPConfig, currency string) bool {
	if cfg == nil || currency == "" || len(cfg.EnabledCurrencies) == 0 {
		return true
	}
	for _, supported := range cfg.EnabledCurrencies {
		if strings.EqualFold(strings.TrimSpace(supported), strings.TrimSpace(currency)) {
			return true
		}
	}
	return false
}

func methodSupportsRegion(cfg *PSPConfig, region string) bool {
	if cfg == nil || region == "" || len(cfg.SupportedRegions) == 0 {
		return true
	}
	for _, supported := range cfg.SupportedRegions {
		if strings.EqualFold(strings.TrimSpace(supported), strings.TrimSpace(region)) {
			return true
		}
	}
	return false
}

func methodSupportsAmount(cfg *PSPConfig, amount int64) bool {
	if cfg == nil {
		return false
	}
	if cfg.MinAmount.Valid && amount < cfg.MinAmount.Int64 {
		return false
	}
	if cfg.MaxAmount.Valid && amount > cfg.MaxAmount.Int64 {
		return false
	}
	return true
}

func normalizeMethodDirection(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "deposit", "inbound":
		return "deposit"
	case "withdrawal", "outbound", "payout":
		return "withdrawal"
	default:
		return strings.ToLower(strings.TrimSpace(direction))
	}
}
