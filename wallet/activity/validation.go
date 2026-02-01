package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
)

type ValidationActivities struct {
	Service *walletvalidation.Service
}

func NewValidationActivities(store *walletstore.Store) *ValidationActivities {
	return &ValidationActivities{Service: &walletvalidation.Service{Store: store}}
}

func (a *ValidationActivities) ValidateP2PTransfer(ctx context.Context, req walletvalidation.P2PValidationRequest) (*walletvalidation.P2PValidationResult, error) {
	if a == nil || a.Service == nil {
		return nil, ErrMissingStore
	}
	return a.Service.ValidateP2P(ctx, req)
}
