package store

// Option configures Store behavior.
type Option func(*StoreOptions)

// StoreOptions carries optional configuration for Store.
type StoreOptions struct {
	DataKey string
}

// WithDataKey enables at-rest encryption using the provided key material.
func WithDataKey(key string) Option {
	return func(opts *StoreOptions) {
		opts.DataKey = key
	}
}
