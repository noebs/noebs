package psp

import "sync"

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func (r *Registry) Register(provider Provider) error {
	if provider == nil {
		return ErrPSPNotRegistered
	}
	code := provider.Code()
	if code == "" {
		return ErrPSPConfigInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[code] = provider
	return nil
}

func (r *Registry) Get(code string) (Provider, error) {
	if code == "" {
		return nil, ErrPSPConfigInvalid
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider := r.providers[code]
	if provider == nil {
		return nil, ErrPSPNotRegistered
	}
	return provider, nil
}
