package store

import (
	"database/sql"
	"time"
)

type PSPConfigScope struct {
	Region    string
	Currency  string
	Direction string
}

type PSPConfigOverride struct {
	ID                     int64          `db:"id"`
	TenantID               string         `db:"tenant_id"`
	ProviderCode           string         `db:"provider_code"`
	Region                 sql.NullString `db:"region"`
	Currency               sql.NullString `db:"currency"`
	Direction              sql.NullString `db:"direction"`
	IsActive               bool           `db:"is_active"`
	SupportsDeposit        bool           `db:"supports_deposit"`
	SupportsWithdrawal     bool           `db:"supports_withdrawal"`
	EnabledCurrencies      StringArray    `db:"enabled_currencies"`
	WebhookAuthMode        sql.NullString `db:"webhook_auth_mode"`
	WebhookAllowedCIDRs    StringArray    `db:"webhook_allowed_cidrs"`
	StatusCheckWebhook     sql.NullBool   `db:"status_check_unauthenticated_webhook"`
	MethodType             sql.NullString `db:"method_type"`
	DisplayName            sql.NullString `db:"display_name"`
	SupportedRegions       StringArray    `db:"supported_regions"`
	MinAmount              sql.NullInt64  `db:"min_amount"`
	MaxAmount              sql.NullInt64  `db:"max_amount"`
	DepositInputSchema     RawJSON        `db:"deposit_input_schema"`
	WithdrawalInputSchema  RawJSON        `db:"withdrawal_input_schema"`
	PresentationSchema     RawJSON        `db:"presentation_schema"`
	DepositRequestMethod   sql.NullString `db:"deposit_request_method"`
	DepositRequestPath     sql.NullString `db:"deposit_request_path"`
	DepositRequestMapping  RawJSON        `db:"deposit_request_mapping"`
	PayoutRequestMethod    sql.NullString `db:"payout_request_method"`
	PayoutRequestPath      sql.NullString `db:"payout_request_path"`
	PayoutRequestMapping   RawJSON        `db:"payout_request_mapping"`
	StatusRequestMethod    sql.NullString `db:"status_request_method"`
	StatusRequestPath      sql.NullString `db:"status_request_path"`
	StatusRequestMapping   RawJSON        `db:"status_request_mapping"`
	DepositResponseMapping RawJSON        `db:"deposit_response_mapping"`
	PayoutResponseMapping  RawJSON        `db:"payout_response_mapping"`
	StatusResponseMapping  RawJSON        `db:"status_response_mapping"`
	WebhookResponseMapping RawJSON        `db:"webhook_response_mapping"`
	CreatedAt              time.Time      `db:"created_at"`
	UpdatedAt              time.Time      `db:"updated_at"`
}
