package store

import (
	"database/sql"
	"time"

	"github.com/shopspring/decimal"
)

type FeeConfig struct {
	ID                  int64           `db:"id"`
	TenantID            string          `db:"tenant_id"`
	TransactionType     string          `db:"transaction_type"`
	Currency            string          `db:"currency"`
	TierMin             int64           `db:"tier_min"`
	TierMax             sql.NullInt64   `db:"tier_max"`
	PercentageFee       decimal.Decimal `db:"percentage_fee"`
	FlatFee             int64           `db:"flat_fee"`
	MinFee              int64           `db:"min_fee"`
	MaxFee              sql.NullInt64   `db:"max_fee"`
	FeeAccountCode      sql.NullString  `db:"fee_account_code"`
	IsActive            bool            `db:"is_active"`
	CreatedByOperatorID int64           `db:"created_by_operator_id"`
	CreatedAt           time.Time       `db:"created_at"`
}

type FeeConfigFilter struct {
	TenantID        string
	TransactionType string
	Currency        string
	ActiveOnly      bool
	Limit           int
	Offset          int
}

type ExchangeRate struct {
	ID              int64               `db:"id"`
	TenantID        string              `db:"tenant_id"`
	BaseCurrency    string              `db:"base_currency"`
	QuoteCurrency   string              `db:"quote_currency"`
	BuyRate         decimal.Decimal     `db:"buy_rate"`
	SellRate        decimal.Decimal     `db:"sell_rate"`
	Spread          decimal.NullDecimal `db:"spread"`
	SetByOperatorID int64               `db:"set_by_operator_id"`
	EffectiveFrom   time.Time           `db:"effective_from"`
	EffectiveTo     sql.NullTime        `db:"effective_to"`
	CreatedAt       time.Time           `db:"created_at"`
}

type ExchangeRateFilter struct {
	TenantID      string
	BaseCurrency  string
	QuoteCurrency string
	ActiveOnly    bool
	Limit         int
	Offset        int
}

type TransactionLimit struct {
	ID                  int64  `db:"id"`
	TenantID            string `db:"tenant_id"`
	KYCTier             string `db:"kyc_tier"`
	TransactionType     string `db:"transaction_type"`
	Currency            string `db:"currency"`
	DailyLimit          int64  `db:"daily_limit"`
	MonthlyLimit        int64  `db:"monthly_limit"`
	PerTransactionLimit int64  `db:"per_transaction_limit"`
	IsActive            bool   `db:"is_active"`
}
