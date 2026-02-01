package store

import (
	"database/sql"
	"time"

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
	ID               int64               `db:"id"`
	TenantID         string              `db:"tenant_id"`
	PSPTransactionID int64               `db:"psp_transaction_id"`
	AmountKind       PSPAmountKind       `db:"amount_kind"`
	Amount           int64               `db:"amount"`
	Currency         string              `db:"currency"`
	FxRate           decimal.NullDecimal `db:"fx_rate"`
	FxBaseCurrency   sql.NullString      `db:"fx_base_currency"`
	FxQuoteCurrency  sql.NullString      `db:"fx_quote_currency"`
	FxSource         sql.NullString      `db:"fx_source"`
	CreatedAt        time.Time           `db:"created_at"`
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
