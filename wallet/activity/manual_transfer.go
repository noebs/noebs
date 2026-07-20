package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type ManualTransferActivities struct {
	Store *walletstore.Store
}

func NewManualTransferActivities(store *walletstore.Store) *ManualTransferActivities {
	return &ManualTransferActivities{Store: store}
}

func (a *ManualTransferActivities) AddManualTransferApproval(ctx context.Context, approval walletstore.ManualTransferApproval) (*walletstore.ManualTransferApproval, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.AddManualTransferApproval(ctx, approval)
}

func (a *ManualTransferActivities) GetManualTransferByWorkflow(ctx context.Context, tenantID, workflowID string) (*walletstore.ManualTransfer, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.GetManualTransferByWorkflow(ctx, tenantID, workflowID)
}

func (a *ManualTransferActivities) UpdateManualTransferStatus(ctx context.Context, tenantID, workflowID string, update walletstore.ManualTransferStatusUpdate) error {
	if a == nil || a.Store == nil {
		return ErrMissingStore
	}
	return a.Store.UpdateManualTransferStatus(ctx, tenantID, workflowID, update)
}
