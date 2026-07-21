package store

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type PSPAmountKind string

const (
	PSPAmountRequested    PSPAmountKind = "requested"
	PSPAmountReported     PSPAmountKind = "reported"
	PSPAmountSettlement   PSPAmountKind = "settlement"
	PSPAmountFee          PSPAmountKind = "fee"
	PSPAmountNet          PSPAmountKind = "net"
	PSPAmountWalletCredit PSPAmountKind = "wallet_credit"
	PSPAmountWalletDebit  PSPAmountKind = "wallet_debit"
	PSPAmountOverpayment  PSPAmountKind = "overpayment"
	PSPAmountUnderpayment PSPAmountKind = "underpayment"
)

type PSPTransactionAmount struct {
	ID                               int64               `db:"id"`
	TenantID                         string              `db:"tenant_id"`
	PSPTransactionID                 int64               `db:"psp_transaction_id"`
	AmountKind                       PSPAmountKind       `db:"amount_kind"`
	Amount                           int64               `db:"amount"`
	Currency                         string              `db:"currency"`
	CurrencyUnitID                   int64               `db:"currency_unit_version_id"`
	FxRate                           decimal.NullDecimal `db:"fx_rate"`
	FxRateNumerator                  decimal.NullDecimal `db:"fx_rate_numerator"`
	FxRateDenominator                decimal.NullDecimal `db:"fx_rate_denominator"`
	FxBaseCurrency                   sql.NullString      `db:"fx_base_currency"`
	FxQuoteCurrency                  sql.NullString      `db:"fx_quote_currency"`
	FxBaseCurrencyUnitID             sql.NullInt64       `db:"fx_base_currency_unit_version_id"`
	FxQuoteCurrencyUnitID            sql.NullInt64       `db:"fx_quote_currency_unit_version_id"`
	FxSource                         sql.NullString      `db:"fx_source"`
	FxObservationID                  sql.NullInt64       `db:"fx_observation_id"`
	FxQuoteID                        uuid.NullUUID       `db:"fx_quote_id"`
	FxConversionAt                   sql.NullTime        `db:"fx_conversion_at"`
	FxObservationBaseCurrency        sql.NullString      `db:"fx_observation_base_currency"`
	FxObservationQuoteCurrency       sql.NullString      `db:"fx_observation_quote_currency"`
	FxObservationBaseCurrencyUnitID  sql.NullInt64       `db:"fx_observation_base_currency_unit_version_id"`
	FxObservationQuoteCurrencyUnitID sql.NullInt64       `db:"fx_observation_quote_currency_unit_version_id"`
	CreatedAt                        time.Time           `db:"created_at"`
}

type PSPTransactionAmountInput struct {
	AmountKind                       PSPAmountKind
	Amount                           int64
	Currency                         string
	CurrencyUnitID                   int64
	FxRate                           decimal.NullDecimal
	FxRateNumerator                  decimal.NullDecimal
	FxRateDenominator                decimal.NullDecimal
	FxBaseCurrency                   string
	FxQuoteCurrency                  string
	FxBaseCurrencyUnitID             int64
	FxQuoteCurrencyUnitID            int64
	FxSource                         string
	FxObservationID                  int64
	FxQuoteID                        uuid.UUID
	FxConversionAt                   time.Time
	FxObservationBaseCurrency        string
	FxObservationQuoteCurrency       string
	FxObservationBaseCurrencyUnitID  int64
	FxObservationQuoteCurrencyUnitID int64
}

func (k PSPAmountKind) Valid() bool {
	switch k {
	case PSPAmountRequested,
		PSPAmountReported,
		PSPAmountSettlement,
		PSPAmountFee,
		PSPAmountNet,
		PSPAmountWalletCredit,
		PSPAmountWalletDebit,
		PSPAmountOverpayment,
		PSPAmountUnderpayment:
		return true
	default:
		return false
	}
}
