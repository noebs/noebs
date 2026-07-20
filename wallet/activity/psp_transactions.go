package activity

import (
	"context"
	"time"

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
	WorkflowSignal  *walletstore.PSPWorkflowSignal
}

type ListPSPTransactionsByStatusParams struct {
	TenantID string
	Status   string
	Start    time.Time
	End      time.Time
	Limit    int
}

type TryAcquirePSPTransactionLockParams struct {
	TenantID        string
	ClientReference string
	LockToken       string
	LockExpiresAt   time.Time
}

type AcknowledgePSPWorkflowSignalParams struct {
	TenantID        string
	ClientReference string
	DeliveredAt     time.Time
	LockToken       string
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

func (a *PSPTransactionActivities) ListPSPTransactionsByStatus(ctx context.Context, params ListPSPTransactionsByStatusParams) ([]walletstore.PSPTransaction, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.ListPSPTransactionsByStatus(ctx, params.TenantID, params.Status, params.Start, params.End, params.Limit)
}

func (a *PSPTransactionActivities) UpdatePSPTransactionStatus(ctx context.Context, params UpdatePSPTransactionStatusParams) (*walletstore.PSPTransaction, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	if params.WorkflowSignal != nil {
		return a.Store.ApplyExternalPSPStatus(ctx, params.TenantID, params.ClientReference, params.Update, params.WorkflowSignal)
	}
	if err := a.Store.UpdatePSPTransactionStatus(ctx, params.TenantID, params.ClientReference, params.Update); err != nil {
		stored, getErr := a.Store.GetPSPTransactionByReference(ctx, params.TenantID, params.ClientReference)
		if getErr == nil && walletstore.PSPTransactionStatusTerminal(stored.Status) && len(stored.WorkflowSignalPayload) > 0 {
			return stored, nil
		}
		return nil, err
	}
	return a.Store.GetPSPTransactionByReference(ctx, params.TenantID, params.ClientReference)
}

func (a *PSPTransactionActivities) TryAcquirePSPTransactionLock(ctx context.Context, params TryAcquirePSPTransactionLockParams) (bool, error) {
	if a == nil || a.Store == nil {
		return false, ErrMissingStore
	}
	return a.Store.TryAcquirePSPTransactionLock(ctx, params.TenantID, params.ClientReference, params.LockToken, params.LockExpiresAt)
}

func (a *PSPTransactionActivities) AcknowledgePSPWorkflowSignal(ctx context.Context, params AcknowledgePSPWorkflowSignalParams) (*walletstore.PSPTransaction, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.AcknowledgePSPWorkflowSignal(ctx, params.TenantID, params.ClientReference, params.DeliveredAt, params.LockToken)
}
