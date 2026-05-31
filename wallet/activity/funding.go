package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

type FundingActivities struct {
	Store *walletstore.Store
}

func NewFundingActivities(store *walletstore.Store) *FundingActivities {
	return &FundingActivities{Store: store}
}

func (a *FundingActivities) RecordFundingSource(ctx context.Context, source walletstore.FundingSource) (*walletstore.FundingSource, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.UpsertFundingSource(ctx, source)
}

func (a *FundingActivities) LinkLedgerToFundingSource(ctx context.Context, link walletstore.LedgerFundingLink) (*walletstore.LedgerFundingLink, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.CreateFundingLink(ctx, link)
}

func (a *FundingActivities) LinkLedgerToWithdrawalDestination(ctx context.Context, link walletstore.LedgerWithdrawalDestinationLink) (*walletstore.LedgerWithdrawalDestinationLink, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.CreateWithdrawalDestinationLink(ctx, link)
}

func (a *FundingActivities) ResolveWithdrawalDestination(ctx context.Context, tenantID string, destinationID int64) (*walletstore.WithdrawalDestination, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.GetWithdrawalDestination(ctx, tenantID, destinationID)
}

func (a *FundingActivities) ResolveFundingSource(ctx context.Context, tenantID string, sourceID int64) (*walletstore.FundingSource, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	return a.Store.GetFundingSourceByID(ctx, tenantID, sourceID)
}

func (a *FundingActivities) GetReturnToSourceOptions(ctx context.Context, tenantID string, walletID uuid.UUID) ([]walletstore.FundingSource, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	sources, err := a.Store.ListFundingSources(ctx, tenantID, walletID)
	if err != nil {
		return nil, err
	}
	options := make([]walletstore.FundingSource, 0, len(sources))
	for _, source := range sources {
		if source.SupportsWithdrawal && source.VerificationStatus == "verified" && len(source.WithdrawalMethod) > 0 {
			options = append(options, source)
		}
	}
	return options, nil
}
