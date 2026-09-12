package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

// checkSharedOutboundLimitInTx is called only while the source wallet is locked
// by the common reservation path. Both outbound rails serialize on that same
// wallet, including a first reservation when no period rows previously exist.
//
// Existing per-operation counters are summed rather than copied/reset. Moving
// reserved usage to consumed usage preserves the sum; a concurrent release can
// only make this check conservative. No shared policy row means no new policy.
func (s *Store) checkSharedOutboundLimitInTx(ctx context.Context, tx *sqlx.Tx, params LimitUsageParams, wallet *Wallet, dailyStart, monthlyStart time.Time) error {
	if params.TransactionType != "p2p" && params.TransactionType != "interop_out" {
		return nil
	}
	var policy struct {
		Daily   int64 `db:"daily_limit"`
		Monthly int64 `db:"monthly_limit"`
	}
	err := tx.GetContext(ctx, &policy, s.DB.Rebind(`SELECT daily_limit,monthly_limit
		FROM shared_outbound_limits
		WHERE tenant_id=? AND kyc_tier=? AND currency=? AND currency_unit_version_id=? AND enabled`), params.TenantID, wallet.KYCTier, params.Currency, wallet.CurrencyUnitID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var exceeded struct {
		Daily   bool `db:"daily_exceeded"`
		Monthly bool `db:"monthly_exceeded"`
	}
	// Numeric arithmetic prevents overflow when adding multiple bigint counters.
	// Wallet currency/unit identity is immutable; scope by that exact wallet.
	err = tx.GetContext(ctx, &exceeded, s.DB.Rebind(`SELECT
		COALESCE(SUM(reserved_amount::numeric+consumed_amount::numeric)
		  FILTER(WHERE period_kind='daily' AND period_start=?),0) > ? AS daily_exceeded,
		COALESCE(SUM(reserved_amount::numeric+consumed_amount::numeric)
		  FILTER(WHERE period_kind='monthly' AND period_start=?),0) > ? AS monthly_exceeded
		FROM transaction_limit_period_usage
		WHERE tenant_id=? AND wallet_id=? AND currency=?
		  AND transaction_type IN ('p2p','interop_out')
		  AND ((period_kind='daily' AND period_start=?) OR (period_kind='monthly' AND period_start=?))`),
		dailyStart, policy.Daily-params.Amount, monthlyStart, policy.Monthly-params.Amount,
		params.TenantID, params.WalletID, params.Currency, dailyStart, monthlyStart)
	if err != nil {
		return err
	}
	if exceeded.Daily {
		return TransactionLimitExceededError{Reason: LimitExceededDaily}
	}
	if exceeded.Monthly {
		return TransactionLimitExceededError{Reason: LimitExceededMonthly}
	}
	return nil
}
