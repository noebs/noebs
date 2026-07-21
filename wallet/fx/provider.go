package fx

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

const (
	ProviderECBSDMX  = "ecb_sdmx"
	ProviderCBOSHTML = "cbos_html"
)

type Observation struct {
	Pair             walletstore.FXSourcePair
	Rate             decimal.Decimal
	Side             string
	Purpose          string
	ObservationAt    time.Time
	PublishedAt      sql.NullTime
	RetrievedAt      time.Time
	ExpiresAt        time.Time
	RawPayloadSHA256 string
	SourceRevision   string
}

type Provider interface {
	Fetch(context.Context, walletstore.FXSource, []walletstore.FXSourcePair, time.Time) ([]Observation, error)
}

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func NewDefaultRegistry(client *http.Client) (*Registry, error) {
	if client == nil {
		return nil, ErrMissingProvider
	}
	registry := NewRegistry()
	if err := registry.Register(ProviderECBSDMX, NewECBProvider(client)); err != nil {
		return nil, err
	}
	if err := registry.Register(ProviderCBOSHTML, NewCBOSProvider(client)); err != nil {
		return nil, err
	}
	return registry, nil
}

func (r *Registry) Register(name string, provider Provider) error {
	if r == nil || name == "" || provider == nil {
		return ErrMissingProvider
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[name]; exists {
		return ErrDuplicateProvider
	}
	r.providers[name] = provider
	return nil
}

func (r *Registry) Resolve(name string) (Provider, error) {
	if r == nil || name == "" {
		return nil, ErrMissingProvider
	}
	r.mu.RLock()
	provider := r.providers[name]
	r.mu.RUnlock()
	if provider == nil {
		return nil, ErrUnknownProvider
	}
	return provider, nil
}

func validateFetchInput(source walletstore.FXSource, pairs []walletstore.FXSourcePair, retrievedAt time.Time) error {
	if source.ID <= 0 || source.Code == "" || source.Provider == "" || source.Purpose == "" || source.SourceURL == "" || source.MaxAgeSeconds <= 0 || !source.IsEnabled {
		return ErrInvalidSource
	}
	if retrievedAt.IsZero() {
		return ErrInvalidSource
	}
	if len(pairs) == 0 {
		return ErrMissingPairs
	}
	for index, pair := range pairs {
		if pair.ID <= 0 || pair.SourceID != source.ID || pair.BaseCurrencyCode == "" || pair.QuoteCurrencyCode == "" || pair.BaseCurrencyCode == pair.QuoteCurrencyCode || pair.ExternalSeries == "" || !pair.IsEnabled {
			return ErrInvalidPair
		}
		if index > 0 && comparePairs(pairs[index-1], pair) >= 0 {
			return ErrUnsortedPairs
		}
	}
	return nil
}

func comparePairs(left, right walletstore.FXSourcePair) int {
	if left.BaseCurrencyCode != right.BaseCurrencyCode {
		if left.BaseCurrencyCode < right.BaseCurrencyCode {
			return -1
		}
		return 1
	}
	if left.QuoteCurrencyCode != right.QuoteCurrencyCode {
		if left.QuoteCurrencyCode < right.QuoteCurrencyCode {
			return -1
		}
		return 1
	}
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return 0
}

func sortObservations(observations []Observation) {
	sort.Slice(observations, func(i, j int) bool {
		pairOrder := comparePairs(observations[i].Pair, observations[j].Pair)
		if pairOrder != 0 {
			return pairOrder < 0
		}
		if !observations[i].ObservationAt.Equal(observations[j].ObservationAt) {
			return observations[i].ObservationAt.Before(observations[j].ObservationAt)
		}
		return observations[i].Side < observations[j].Side
	})
}

type providerError struct {
	kind error
	err  error
}

func (e providerError) Error() string {
	if e.err == nil {
		return e.kind.Error()
	}
	return e.kind.Error() + ": " + e.err.Error()
}

func (e providerError) Unwrap() error { return e.err }

func (e providerError) Is(target error) bool {
	return target == e.kind || errors.Is(e.err, target)
}
