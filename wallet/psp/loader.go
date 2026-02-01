package psp

import (
	"context"
	"errors"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type SecretResolver interface {
	Resolve(ctx context.Context, tenantID, providerCode string) (SecretBundle, error)
}

type Loader struct {
	Store   *walletstore.Store
	Secrets SecretResolver
}

func (l *Loader) Load(ctx context.Context, tenantID, providerCode string) (*Config, error) {
	if l == nil || l.Store == nil {
		return nil, ErrPSPConfigInvalid
	}
	if tenantID == "" || providerCode == "" {
		return nil, ErrPSPConfigInvalid
	}
	cfg, err := l.Store.GetPSPConfig(ctx, tenantID, providerCode)
	if err != nil {
		if errors.Is(err, walletstore.ErrPSPConfigNotFound) {
			return nil, ErrPSPNotRegistered
		}
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
