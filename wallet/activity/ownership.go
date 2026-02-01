package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type OwnershipActivities struct {
	Store *walletstore.Store
}

func NewOwnershipActivities(store *walletstore.Store) *OwnershipActivities {
	return &OwnershipActivities{Store: store}
}

func (a *OwnershipActivities) InitiateOwnershipVerification(ctx context.Context, verification walletstore.OwnershipVerification) (*walletstore.OwnershipVerification, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.CreateOwnershipVerification(ctx, verification)
}

func (a *OwnershipActivities) GetOwnershipVerification(ctx context.Context, tenantID string, verificationID int64) (*walletstore.OwnershipVerification, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.GetOwnershipVerification(ctx, tenantID, verificationID)
}
