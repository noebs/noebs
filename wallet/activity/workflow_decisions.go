package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type WorkflowDecisionActivities struct {
	Store *walletstore.Store
}

func NewWorkflowDecisionActivities(store *walletstore.Store) *WorkflowDecisionActivities {
	return &WorkflowDecisionActivities{Store: store}
}

func (a *WorkflowDecisionActivities) LookupWorkflowDecision(ctx context.Context, key walletstore.WorkflowDecisionKey) (walletstore.WorkflowDecisionLookup, error) {
	if a == nil || a.Store == nil {
		return walletstore.WorkflowDecisionLookup{}, ErrMissingStore
	}
	return a.Store.LookupWorkflowDecision(ctx, key)
}

func (a *WorkflowDecisionActivities) CloseWorkflowDecisionWindow(ctx context.Context, close walletstore.WorkflowDecisionWindowClose) (walletstore.WorkflowDecisionLookup, error) {
	if a == nil || a.Store == nil {
		return walletstore.WorkflowDecisionLookup{}, ErrMissingStore
	}
	return a.Store.CloseWorkflowDecisionWindow(ctx, close)
}
