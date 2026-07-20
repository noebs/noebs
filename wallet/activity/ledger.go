package activity

import (
	"context"
	"errors"

	walletstore "github.com/adonese/noebs/wallet/store"
)

var ErrMissingStore = errors.New("missing wallet store")

type LedgerActivities struct {
	Store *walletstore.Store
}

func NewLedgerActivities(store *walletstore.Store) *LedgerActivities {
	return &LedgerActivities{Store: store}
}

func (a *LedgerActivities) ExecuteDoubleEntry(ctx context.Context, params walletstore.DoubleEntryParams) (*walletstore.DoubleEntryResult, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.PostDoubleEntry(ctx, params)
}

func (a *LedgerActivities) ExecuteHeldDoubleEntry(ctx context.Context, params walletstore.HeldDoubleEntryParams) (*walletstore.DoubleEntryResult, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.PostHeldDoubleEntry(ctx, params)
}

func (a *LedgerActivities) ExecuteSystemDebitDoubleEntry(ctx context.Context, params walletstore.DoubleEntryParams) (*walletstore.DoubleEntryResult, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.PostSystemDebitDoubleEntry(ctx, params)
}

func (a *LedgerActivities) ExecuteMultiLegSettlement(ctx context.Context, params walletstore.MultiLegSettlementParams) (*walletstore.MultiLegSettlementResult, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	result, err := a.Store.PostMultiLegSettlement(ctx, params)
	return result, classifyTransactionLimitError(err)
}

func (a *LedgerActivities) ExecuteSystemFundedMultiLegSettlement(ctx context.Context, params walletstore.MultiLegSettlementParams) (*walletstore.MultiLegSettlementResult, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	result, err := a.Store.PostSystemFundedMultiLegSettlement(ctx, params)
	return result, classifyTransactionLimitError(err)
}

func (a *LedgerActivities) ExecuteHeldWithdrawalSettlement(ctx context.Context, params walletstore.HeldWithdrawalSettlementParams) (*walletstore.MultiLegSettlementResult, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	result, err := a.Store.PostHeldWithdrawalSettlement(ctx, params)
	return result, classifyTransactionLimitError(err)
}

func (a *LedgerActivities) ValidateDoubleEntry(ctx context.Context, params walletstore.DoubleEntryParams) (struct{}, error) {
	_ = ctx
	if a == nil {
		return struct{}{}, ErrMissingStore
	}
	return struct{}{}, walletstore.ValidateDoubleEntryParams(params)
}

func (a *LedgerActivities) ValidateHeldDoubleEntry(ctx context.Context, params walletstore.HeldDoubleEntryParams) (struct{}, error) {
	_ = ctx
	if a == nil {
		return struct{}{}, ErrMissingStore
	}
	return struct{}{}, walletstore.ValidateHeldDoubleEntryParams(params)
}

func (a *LedgerActivities) ValidateSystemDebitDoubleEntry(ctx context.Context, params walletstore.DoubleEntryParams) (struct{}, error) {
	_ = ctx
	if a == nil {
		return struct{}{}, ErrMissingStore
	}
	return struct{}{}, walletstore.ValidateDoubleEntryParams(params)
}

func (a *LedgerActivities) ValidateMultiLegSettlement(ctx context.Context, params walletstore.MultiLegSettlementParams) (struct{}, error) {
	_ = ctx
	if a == nil {
		return struct{}{}, ErrMissingStore
	}
	return struct{}{}, walletstore.ValidateMultiLegSettlementParams(params)
}

func (a *LedgerActivities) ValidateHeldWithdrawalSettlement(ctx context.Context, params walletstore.HeldWithdrawalSettlementParams) (struct{}, error) {
	_ = ctx
	if a == nil {
		return struct{}{}, ErrMissingStore
	}
	return struct{}{}, walletstore.ValidateHeldWithdrawalSettlementParams(params)
}

func (a *LedgerActivities) CreateHold(ctx context.Context, params walletstore.HoldParams) (*walletstore.BalanceHold, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.CreateHold(ctx, params)
}

func (a *LedgerActivities) ValidateHold(ctx context.Context, params walletstore.HoldParams) (struct{}, error) {
	_ = ctx
	if a == nil {
		return struct{}{}, ErrMissingStore
	}
	return struct{}{}, walletstore.ValidateHoldParams(params)
}

func (a *LedgerActivities) ReleaseHold(ctx context.Context, tenantID string, holdID int64) error {
	if a == nil || a.Store == nil {
		return ErrMissingStore
	}
	return a.Store.ReleaseHold(ctx, tenantID, holdID)
}

func (a *LedgerActivities) CommitHold(ctx context.Context, tenantID string, holdID int64) error {
	if a == nil || a.Store == nil {
		return ErrMissingStore
	}
	return a.Store.CommitHold(ctx, tenantID, holdID)
}

func (a *LedgerActivities) ExpireHolds(ctx context.Context, tenantID string, limit int) (int, error) {
	if a == nil || a.Store == nil {
		return 0, ErrMissingStore
	}
	return a.Store.ExpireHolds(ctx, tenantID, limit)
}

func (a *LedgerActivities) ValidateReleaseHold(ctx context.Context, tenantID string, holdID int64) (struct{}, error) {
	_ = ctx
	if a == nil {
		return struct{}{}, ErrMissingStore
	}
	return struct{}{}, walletstore.ValidateReleaseHold(tenantID, holdID)
}

func (a *LedgerActivities) LedgerTransactionExists(ctx context.Context, tenantID, idempotencyKey string) (bool, error) {
	if a == nil || a.Store == nil {
		return false, ErrMissingStore
	}
	return a.Store.LedgerTransactionExists(ctx, tenantID, idempotencyKey)
}

func (a *LedgerActivities) LedgerTransactionExistsByReference(ctx context.Context, tenantID, referenceType, referenceID string) (bool, error) {
	if a == nil || a.Store == nil {
		return false, ErrMissingStore
	}
	return a.Store.LedgerTransactionExistsByReference(ctx, tenantID, referenceType, referenceID)
}
