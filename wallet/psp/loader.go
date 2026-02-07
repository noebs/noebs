package psp

import (
	"context"
	"errors"

	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
)

type SecretResolver interface {
	Resolve(ctx context.Context, tenantID, providerCode string) (SecretBundle, error)
}

type SecretResolverFunc func(ctx context.Context, tenantID, providerCode string) (SecretBundle, error)

func (f SecretResolverFunc) Resolve(ctx context.Context, tenantID, providerCode string) (SecretBundle, error) {
	return f(ctx, tenantID, providerCode)
}

type Loader struct {
	Store   *walletstore.Store
	Secrets SecretResolver
}

func (l *Loader) Load(ctx context.Context, tenantID, providerCode string) (*Config, error) {
	return l.LoadForScope(ctx, tenantID, providerCode, Scope{})
}

func (l *Loader) LoadForScope(ctx context.Context, tenantID, providerCode string, scope Scope) (*Config, error) {
	if l == nil || l.Store == nil {
		return nil, ErrPSPConfigInvalid
	}
	if tenantID == "" || providerCode == "" {
		return nil, ErrPSPConfigInvalid
	}
	cfg, _, err := l.Store.ResolvePSPConfig(ctx, tenantID, providerCode, walletstore.PSPConfigScope{
		Region:    scope.Region,
		Currency:  scope.Currency,
		Direction: scope.Direction,
	})
	if err != nil {
		if errors.Is(err, walletstore.ErrPSPConfigNotFound) {
			return nil, ErrPSPNotRegistered
		}
		return nil, err
	}
	if err := walletvalidation.ValidatePSPConfig(cfg, scope.Currency, scope.Direction); err != nil {
		return nil, err
	}
	if l.Secrets == nil {
		return nil, ErrPSPSecretMissing
	}
	secrets, err := l.Secrets.Resolve(ctx, tenantID, providerCode)
	if err != nil {
		return nil, ErrPSPSecretMissing
	}
	return MergeConfig(cfg, secrets)
}
