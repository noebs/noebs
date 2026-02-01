package psp

import "sync"

type ProviderFactory func(cfg *Config) (Provider, error)

type Registry struct {
	mu        sync.RWMutex
	providers map[string]ProviderFactory
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]ProviderFactory)}
}

func (r *Registry) Register(code string, factory ProviderFactory) error {
	if code == "" {
		return ErrPSPConfigInvalid
	}
	if factory == nil {
		return ErrPSPNotRegistered
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[code] = factory
	return nil
}

func (r *Registry) Resolve(cfg *Config) (Provider, error) {
	if cfg == nil || cfg.ProviderCode == "" {
		return nil, ErrPSPConfigInvalid
	}
	r.mu.RLock()
	factory := r.providers[cfg.ProviderCode]
	r.mu.RUnlock()
	if factory == nil {
		return nil, ErrPSPNotRegistered
	}
	return factory(cfg)
}
