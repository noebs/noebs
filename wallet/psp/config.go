package psp

import (
	"encoding/json"

	"github.com/adonese/noebs/wallet/store"
)

type Config struct {
	TenantID               string
	ProviderCode           string
	ProviderName           string
	APIBaseURL             string
	EnabledCurrencies      []string
	IdempotencyHeaderName  string
	IsActive               bool
	SupportsDeposit        bool
	SupportsWithdrawal     bool
	WebhookAuthMode        string
	WebhookAllowedCIDRs    []string
	StatusCheckWebhook     bool
	DepositRequestMethod   string
	DepositRequestPath     string
	DepositRequestMapping  RequestMapping
	PayoutRequestMethod    string
	PayoutRequestPath      string
	PayoutRequestMapping   RequestMapping
	StatusRequestMethod    string
	StatusRequestPath      string
	StatusRequestMapping   RequestMapping
	DepositResponseMapping ResponseMapping
	PayoutResponseMapping  ResponseMapping
	StatusResponseMapping  ResponseMapping
	WebhookResponseMapping ResponseMapping
	APIKey                 string
	APISecret              string
	WebhookSecret          string
	WebhookPublicKey       string
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
	depositMapping, err := parseResponseMapping(cfg.DepositResponseMapping)
	if err != nil {
		return nil, err
	}
	payoutMapping, err := parseResponseMapping(cfg.PayoutResponseMapping)
	if err != nil {
		return nil, err
	}
	statusMapping, err := parseResponseMapping(cfg.StatusResponseMapping)
	if err != nil {
		return nil, err
	}
	webhookMapping, err := parseResponseMapping(cfg.WebhookResponseMapping)
	if err != nil {
		return nil, err
	}
	depositRequestMapping, err := parseRequestMapping(cfg.DepositRequestMapping)
	if err != nil {
		return nil, err
	}
	payoutRequestMapping, err := parseRequestMapping(cfg.PayoutRequestMapping)
	if err != nil {
		return nil, err
	}
	statusRequestMapping, err := parseRequestMapping(cfg.StatusRequestMapping)
	if err != nil {
		return nil, err
	}
	return &Config{
		TenantID:               cfg.TenantID,
		ProviderCode:           cfg.ProviderCode,
		ProviderName:           cfg.ProviderName,
		APIBaseURL:             cfg.APIBaseURL,
		EnabledCurrencies:      []string(cfg.EnabledCurrencies),
		IdempotencyHeaderName:  idempotencyHeader,
		IsActive:               cfg.IsActive,
		SupportsDeposit:        cfg.SupportsDeposit,
		SupportsWithdrawal:     cfg.SupportsWithdrawal,
		WebhookAuthMode:        cfg.WebhookAuthMode,
		WebhookAllowedCIDRs:    []string(cfg.WebhookAllowedCIDRs),
		StatusCheckWebhook:     cfg.StatusCheckWebhook,
		DepositRequestMethod:   cfg.DepositRequestMethod,
		DepositRequestPath:     cfg.DepositRequestPath,
		DepositRequestMapping:  depositRequestMapping,
		PayoutRequestMethod:    cfg.PayoutRequestMethod,
		PayoutRequestPath:      cfg.PayoutRequestPath,
		PayoutRequestMapping:   payoutRequestMapping,
		StatusRequestMethod:    cfg.StatusRequestMethod,
		StatusRequestPath:      cfg.StatusRequestPath,
		StatusRequestMapping:   statusRequestMapping,
		DepositResponseMapping: depositMapping,
		PayoutResponseMapping:  payoutMapping,
		StatusResponseMapping:  statusMapping,
		WebhookResponseMapping: webhookMapping,
		APIKey:                 secrets.APIKey,
		APISecret:              secrets.APISecret,
		WebhookSecret:          secrets.WebhookSecret,
		WebhookPublicKey:       secrets.WebhookPublicKey,
	}, nil
}

func parseRequestMapping(raw []byte) (RequestMapping, error) {
	if len(raw) == 0 {
		return RequestMapping{}, nil
	}
	var mapping RequestMapping
	if err := json.Unmarshal(raw, &mapping); err != nil {
		return RequestMapping{}, err
	}
	return mapping, nil
}

func parseResponseMapping(raw []byte) (ResponseMapping, error) {
	if len(raw) == 0 {
		return ResponseMapping{}, nil
	}
	var mapping ResponseMapping
	if err := json.Unmarshal(raw, &mapping); err != nil {
		return ResponseMapping{}, err
	}
	return mapping, nil
}
