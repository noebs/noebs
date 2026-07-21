package store

import (
	"context"
	"strings"
)

func (s *Store) ListAvailablePSPMethods(ctx context.Context, filter PSPMethodFilter) ([]PSPPaymentMethod, error) {
	tenantID, err := ValidateTenantID(filter.TenantID)
	if err != nil {
		return nil, err
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
	if filter.Currency == "" {
		if filter.CurrencyUnitID != 0 {
			return nil, ErrMissingCurrency
		}
		if filter.Amount > 0 {
			return nil, ErrMissingCurrency
		}
	} else {
		filter.Currency, err = ValidateCurrencyCode(filter.Currency)
		if err != nil {
			return nil, err
		}
		if err := ValidateCurrencyUnitID(filter.CurrencyUnitID); err != nil {
			return nil, err
		}
		if err := s.validateCurrencyUnitIdentity(ctx, filter.Currency, filter.CurrencyUnitID); err != nil {
			return nil, err
		}
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
		WHERE tenant_id = ?
		ORDER BY provider_name ASC, provider_code ASC`)
	var configs []PSPConfig
	if err := db.SelectContext(ctx, &configs, stmt, tenantID); err != nil {
		return nil, err
	}

	resolved := make([]*PSPConfig, 0, len(configs))
	scope := PSPConfigScope{
		Region:         filter.Region,
		Currency:       filter.Currency,
		CurrencyUnitID: filter.CurrencyUnitID,
		Direction:      direction,
	}
	for i := range configs {
		cfg, _, err := s.resolvePSPConfigFromBase(ctx, &configs[i], scope)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, cfg)
	}
	return availablePSPMethodsFromConfigs(resolved, filter, direction), nil
}

func availablePSPMethodsFromConfigs(configs []*PSPConfig, filter PSPMethodFilter, direction string) []PSPPaymentMethod {
	methods := make([]PSPPaymentMethod, 0, len(configs))
	for _, cfg := range configs {
		if !methodSupportsDirection(cfg, direction) {
			continue
		}
		if len(cfg.EnabledCurrencies) == 0 {
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
	return paginatePSPMethods(methods, filter.Limit, filter.Offset)
}

func paginatePSPMethods(methods []PSPPaymentMethod, limit, offset int) []PSPPaymentMethod {
	if offset >= len(methods) {
		return []PSPPaymentMethod{}
	}
	end := offset + limit
	if end < offset || end > len(methods) {
		end = len(methods)
	}
	return methods[offset:end]
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
		CurrencyUnitID:   cfg.AmountCurrencyUnitID,
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
	if cfg == nil {
		return false
	}
	if currency == "" {
		return true
	}
	if len(cfg.EnabledCurrencies) == 0 {
		return false
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
