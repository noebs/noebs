package limits

import (
	"context"
	"errors"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

var ErrMissingStore = errors.New("missing limit store")

type Enforcer struct {
	Store *walletstore.Store
}

type CheckResult struct {
	Allowed          bool
	DailyUsed        int64
	DailyRemaining   int64
	MonthlyUsed      int64
	MonthlyRemaining int64
	Reason           string
}

func (e *Enforcer) Check(ctx context.Context, tenantID string, walletID uuid.UUID, txType, currency string, amount int64) (*CheckResult, error) {
	if e == nil || e.Store == nil {
		return nil, ErrMissingStore
	}
	wallet, err := e.Store.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		return nil, err
	}
	limits, err := e.Store.GetLimits(ctx, tenantID, wallet.KYCTier, txType, currency)
	if err != nil {
		return nil, err
	}
	dailyUsed, err := e.Store.GetDailyUsage(ctx, tenantID, walletID, txType)
	if err != nil {
		return nil, err
	}
	monthlyUsed, err := e.Store.GetMonthlyUsage(ctx, tenantID, walletID, txType)
	if err != nil {
		return nil, err
	}

	result := &CheckResult{
		DailyUsed:        dailyUsed,
		DailyRemaining:   limits.DailyLimit - dailyUsed,
		MonthlyUsed:      monthlyUsed,
		MonthlyRemaining: limits.MonthlyLimit - monthlyUsed,
	}

	if amount > limits.PerTransactionLimit {
		result.Allowed = false
		result.Reason = "exceeds_per_transaction_limit"
		return result, nil
	}
	if dailyUsed+amount > limits.DailyLimit {
		result.Allowed = false
		result.Reason = "exceeds_daily_limit"
		return result, nil
	}
	if monthlyUsed+amount > limits.MonthlyLimit {
		result.Allowed = false
		result.Reason = "exceeds_monthly_limit"
		return result, nil
	}

	result.Allowed = true
	return result, nil
}
