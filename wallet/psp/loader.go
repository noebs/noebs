package psp

import (
	"context"
	"errors"
	"strings"

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
	return l.LoadWebhook(ctx, tenantID, providerCode, Scope{})
}

func (l *Loader) LoadForScope(ctx context.Context, tenantID, providerCode string, scope Scope) (*Config, error) {
	cfg, err := l.resolve(ctx, tenantID, providerCode, scope)
	if err != nil {
		return nil, err
	}
	if err := walletvalidation.ValidatePSPConfig(cfg, scope.Currency, scope.Direction); err != nil {
		return nil, err
	}
	return l.mergeSecrets(ctx, tenantID, providerCode, cfg)
}

// LoadWebhook resolves regional overrides without requiring request-scoped
// currency before the configured webhook mapping has been applied.
func (l *Loader) LoadWebhook(ctx context.Context, tenantID, providerCode string, scope Scope) (*Config, error) {
	cfg, err := l.resolve(ctx, tenantID, providerCode, scope)
	if err != nil {
		return nil, err
	}
	if err := walletvalidation.ValidatePSPConfigBase(cfg); err != nil {
		return nil, err
	}
	return l.mergeSecrets(ctx, tenantID, providerCode, cfg)
}

func (l *Loader) resolve(ctx context.Context, tenantID, providerCode string, scope Scope) (*walletstore.PSPConfig, error) {
	if l == nil || l.Store == nil {
		return nil, ErrPSPConfigInvalid
	}
	tenantID, err := walletstore.ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	providerCode = strings.TrimSpace(providerCode)
	if providerCode == "" {
		return nil, walletstore.ErrMissingProviderCode
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
	return cfg, nil
}

func (l *Loader) mergeSecrets(ctx context.Context, tenantID, providerCode string, cfg *walletstore.PSPConfig) (*Config, error) {
	if l.Secrets == nil {
		return nil, ErrPSPSecretMissing
	}
	secrets, err := l.Secrets.Resolve(ctx, tenantID, providerCode)
	if err != nil {
		return nil, ErrPSPSecretMissing
	}
	return MergeConfig(cfg, secrets)
}
