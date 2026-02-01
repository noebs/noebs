package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type PSPTransactionActivities struct {
	Store *walletstore.Store
}

type AddPSPTransactionAmountsParams struct {
	TenantID         string
	PSPTransactionID int64
	Amounts          []walletstore.PSPTransactionAmountInput
}

type UpdatePSPTransactionStatusParams struct {
	TenantID        string
	ClientReference string
	Update          walletstore.PSPStatusUpdate
}

func NewPSPTransactionActivities(store *walletstore.Store) *PSPTransactionActivities {
	return &PSPTransactionActivities{Store: store}
}

func (a *PSPTransactionActivities) GetPSPTransactionByReference(ctx context.Context, tenantID, clientReference string) (*walletstore.PSPTransaction, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.GetPSPTransactionByReference(ctx, tenantID, clientReference)
}

func (a *PSPTransactionActivities) AddPSPTransactionAmounts(ctx context.Context, params AddPSPTransactionAmountsParams) ([]walletstore.PSPTransactionAmount, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.AddPSPTransactionAmounts(ctx, params.TenantID, params.PSPTransactionID, params.Amounts)
}

func (a *PSPTransactionActivities) ListPSPTransactionsForPolling(ctx context.Context, tenantID string, limit int) ([]walletstore.PSPTransaction, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.ListPSPTransactionsForPolling(ctx, tenantID, limit)
}

func (a *PSPTransactionActivities) UpdatePSPTransactionStatus(ctx context.Context, params UpdatePSPTransactionStatusParams) error {
	if a == nil || a.Store == nil {
		return ErrMissingStore
	}
	return a.Store.UpdatePSPTransactionStatus(ctx, params.TenantID, params.ClientReference, params.Update)
}
