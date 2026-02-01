package psp

import "github.com/adonese/noebs/wallet/store"

type Config struct {
	TenantID              string
	ProviderCode          string
	ProviderName          string
	APIBaseURL            string
	EnabledCurrencies     []string
	IdempotencyHeaderName string
	IsActive              bool
	SupportsDeposit       bool
	SupportsWithdrawal    bool
	APIKey                string
	APISecret             string
	WebhookSecret         string
	WebhookPublicKey      string
}

type SecretBundle struct {
	APIKey           string
	APISecret        string
	WebhookSecret    string
	WebhookPublicKey string
}

func MergeConfig(cfg *store.PSPConfig, secrets SecretBundle) (*Config, error) {
	if cfg == nil {
		return nil, ErrPSPConfigInvalid
	}
	if cfg.ProviderCode == "" || cfg.APIBaseURL == "" {
		return nil, ErrPSPConfigInvalid
	}
	idempotencyHeader := ""
	if cfg.IdempotencyHeaderName.Valid {
		idempotencyHeader = cfg.IdempotencyHeaderName.String
	}
	return &Config{
		TenantID:              cfg.TenantID,
		ProviderCode:          cfg.ProviderCode,
		ProviderName:          cfg.ProviderName,
		APIBaseURL:            cfg.APIBaseURL,
		EnabledCurrencies:     cfg.EnabledCurrencies,
		IdempotencyHeaderName: idempotencyHeader,
		IsActive:              cfg.IsActive,
		SupportsDeposit:       cfg.SupportsDeposit,
		SupportsWithdrawal:    cfg.SupportsWithdrawal,
		APIKey:                secrets.APIKey,
		APISecret:             secrets.APISecret,
		WebhookSecret:         secrets.WebhookSecret,
		WebhookPublicKey:      secrets.WebhookPublicKey,
	}, nil
}
