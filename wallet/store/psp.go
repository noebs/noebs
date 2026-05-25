package store

import (
	"context"
	"database/sql"
	"time"
)

type PSPConfig struct {
	ID                      int64          `db:"id"`
	TenantID                string         `db:"tenant_id"`
	ProviderCode            string         `db:"provider_code"`
	ProviderName            string         `db:"provider_name"`
	APIBaseURL              string         `db:"api_base_url"`
	EnabledCurrencies       StringArray    `db:"enabled_currencies"`
	IdempotencyHeaderName   sql.NullString `db:"idempotency_header_name"`
	IsActive                bool           `db:"is_active"`
	SupportsDeposit         bool           `db:"supports_deposit"`
	SupportsWithdrawal      bool           `db:"supports_withdrawal"`
	WebhookAuthMode         string         `db:"webhook_auth_mode"`
	WebhookAllowedCIDRs     StringArray    `db:"webhook_allowed_cidrs"`
	StatusCheckWebhook      bool           `db:"status_check_unauthenticated_webhook"`
	MethodType              string         `db:"method_type"`
	DisplayName             sql.NullString `db:"display_name"`
	SupportedRegions        StringArray    `db:"supported_regions"`
	MinAmount               sql.NullInt64  `db:"min_amount"`
	MaxAmount               sql.NullInt64  `db:"max_amount"`
	DepositInputSchema      RawJSON        `db:"deposit_input_schema"`
	WithdrawalInputSchema   RawJSON        `db:"withdrawal_input_schema"`
	PresentationSchema      RawJSON        `db:"presentation_schema"`
	ResponseDefaultCurrency sql.NullString `db:"response_default_currency"`
	DepositRequestMethod    string         `db:"deposit_request_method"`
	DepositRequestPath      string         `db:"deposit_request_path"`
	DepositRequestMapping   RawJSON        `db:"deposit_request_mapping"`
	PayoutRequestMethod     string         `db:"payout_request_method"`
	PayoutRequestPath       string         `db:"payout_request_path"`
	PayoutRequestMapping    RawJSON        `db:"payout_request_mapping"`
	StatusRequestMethod     string         `db:"status_request_method"`
	StatusRequestPath       string         `db:"status_request_path"`
	StatusRequestMapping    RawJSON        `db:"status_request_mapping"`
	DepositResponseMapping  RawJSON        `db:"deposit_response_mapping"`
	PayoutResponseMapping   RawJSON        `db:"payout_response_mapping"`
	StatusResponseMapping   RawJSON        `db:"status_response_mapping"`
	WebhookResponseMapping  RawJSON        `db:"webhook_response_mapping"`
	CreatedAt               time.Time      `db:"created_at"`
}

type PSPMethodFilter struct {
	TenantID  string
	Direction string
	Currency  string
	Region    string
	Amount    int64
	Limit     int
	Offset    int
}

type PSPPaymentMethod struct {
	TenantID         string
	ProviderCode     string
	ProviderName     string
	DisplayName      string
	MethodType       string
	Direction        string
	Currencies       []string
	Regions          []string
	MinAmount        sql.NullInt64
	MaxAmount        sql.NullInt64
	InputSchema      RawJSON
	Presentation     RawJSON
	SupportsDeposit  bool
	SupportsWithdraw bool
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
