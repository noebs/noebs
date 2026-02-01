package activity

import (
	"context"
	"errors"

	"github.com/adonese/noebs/wallet/psp"
)

var ErrMissingPSPDependencies = errors.New("missing psp dependencies")

type PSPActivities struct {
	Loader   *psp.Loader
	Registry *psp.Registry
}

type VerifyDepositParams struct {
	TenantID      string
	ProviderCode  string
	TransactionID string
}

type SendPayoutParams struct {
	TenantID     string
	ProviderCode string
	Request      psp.PayoutRequest
}

type GetStatusParams struct {
	TenantID      string
	ProviderCode  string
	TransactionID string
}

func NewPSPActivities(loader *psp.Loader, registry *psp.Registry) *PSPActivities {
	return &PSPActivities{Loader: loader, Registry: registry}
}

func (a *PSPActivities) VerifyDeposit(ctx context.Context, params VerifyDepositParams) (*psp.DepositVerification, error) {
	provider, _, err := a.resolveProvider(ctx, params.TenantID, params.ProviderCode)
	if err != nil {
		return nil, err
	}
	return provider.VerifyDeposit(ctx, params.TransactionID)
}

func (a *PSPActivities) SendPayout(ctx context.Context, params SendPayoutParams) (*psp.PayoutResult, error) {
	provider, _, err := a.resolveProvider(ctx, params.TenantID, params.ProviderCode)
	if err != nil {
		return nil, err
	}
	return provider.SendPayout(ctx, params.Request)
}

func (a *PSPActivities) GetTransactionStatus(ctx context.Context, params GetStatusParams) (*psp.TxStatus, error) {
	provider, _, err := a.resolveProvider(ctx, params.TenantID, params.ProviderCode)
	if err != nil {
		return nil, err
	}
	return provider.GetTransactionStatus(ctx, params.TransactionID)
}

func (a *PSPActivities) resolveProvider(ctx context.Context, tenantID, providerCode string) (psp.Provider, *psp.Config, error) {
	if a == nil || a.Loader == nil || a.Registry == nil {
		return nil, nil, ErrMissingPSPDependencies
	}
	cfg, err := a.Loader.Load(ctx, tenantID, providerCode)
	if err != nil {
		return nil, nil, err
	}
	provider, err := a.Registry.Resolve(cfg)
	if err != nil {
		return nil, nil, err
	}
	return provider, cfg, nil
}
