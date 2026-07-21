package activity

import (
	"context"
	"errors"
	"strings"
	"time"

	walletfx "github.com/adonese/noebs/wallet/fx"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/jackc/pgx/v5/pgconn"
	"go.temporal.io/sdk/temporal"
)

const (
	FXProviderInvalidErrorType = "fx_provider_invalid"
	FXCatalogInvalidErrorType  = "fx_catalog_invalid"
)

type FXActivities struct {
	Store     *walletstore.Store
	Providers *walletfx.Registry
}

type FXSyncResult struct {
	SourceCode       string
	ObservationCount int
	RetrievedAt      time.Time
}

func NewFXActivities(store *walletstore.Store, providers *walletfx.Registry) *FXActivities {
	return &FXActivities{Store: store, Providers: providers}
}

func (a *FXActivities) ListEnabledFXSources(ctx context.Context) ([]string, error) {
	if a == nil || a.Store == nil {
		return nil, ErrMissingStore
	}
	sources, err := a.Store.ListEnabledFXSources(ctx)
	if err != nil {
		return nil, err
	}
	codes := make([]string, len(sources))
	for index, source := range sources {
		codes[index] = source.Code
	}
	return codes, nil
}

func (a *FXActivities) SyncFXSource(ctx context.Context, sourceCode string) (FXSyncResult, error) {
	if a == nil || a.Store == nil {
		return FXSyncResult{}, ErrMissingStore
	}
	if a.Providers == nil {
		return FXSyncResult{}, temporal.NewNonRetryableApplicationError(
			walletfx.ErrMissingProvider.Error(), FXProviderInvalidErrorType, walletfx.ErrMissingProvider,
		)
	}
	source, err := a.Store.GetFXSource(ctx, sourceCode)
	if err != nil {
		return FXSyncResult{}, classifyFXStoreError(err)
	}
	if !source.IsEnabled {
		return FXSyncResult{}, temporal.NewNonRetryableApplicationError(
			walletfx.ErrInvalidSource.Error(), FXProviderInvalidErrorType, walletfx.ErrInvalidSource,
		)
	}
	pairs, err := a.Store.ListEnabledFXSourcePairs(ctx, source.ID)
	if err != nil {
		return FXSyncResult{}, classifyFXStoreError(err)
	}
	provider, err := a.Providers.Resolve(source.Provider)
	if err != nil {
		return FXSyncResult{}, temporal.NewNonRetryableApplicationError(err.Error(), FXProviderInvalidErrorType, err)
	}
	observations, retrievedAt, err := fetchFXObservationsAtCompletion(
		ctx, provider, *source, pairs, time.Now,
	)
	if err != nil {
		if errors.Is(err, walletfx.ErrTemporary) {
			return FXSyncResult{}, err
		}
		return FXSyncResult{}, temporal.NewNonRetryableApplicationError(err.Error(), FXProviderInvalidErrorType, err)
	}

	type unitKey struct {
		currencyCode   string
		observationDay string
	}
	unitCache := make(map[unitKey]*walletstore.CurrencyUnitVersion, len(pairs)*2)
	unit := func(currencyCode string, observationAt time.Time) (*walletstore.CurrencyUnitVersion, error) {
		key := unitKey{currencyCode: currencyCode, observationDay: observationAt.UTC().Format(time.DateOnly)}
		if cached := unitCache[key]; cached != nil {
			return cached, nil
		}
		resolved, err := a.Store.GetCurrencyUnit(ctx, currencyCode, observationAt)
		if err != nil {
			return nil, classifyFXStoreError(err)
		}
		unitCache[key] = resolved
		return resolved, nil
	}
	for _, observation := range observations {
		baseUnit, err := unit(observation.Pair.BaseCurrencyCode, observation.ObservationAt)
		if err != nil {
			return FXSyncResult{}, err
		}
		quoteUnit, err := unit(observation.Pair.QuoteCurrencyCode, observation.ObservationAt)
		if err != nil {
			return FXSyncResult{}, err
		}
		_, err = a.Store.CreateFXObservation(ctx, walletstore.FXObservationInput{
			SourceID:            source.ID,
			SourcePairID:        observation.Pair.ID,
			ExternalSeries:      observation.Pair.ExternalSeries,
			BaseCurrencyCode:    observation.Pair.BaseCurrencyCode,
			QuoteCurrencyCode:   observation.Pair.QuoteCurrencyCode,
			BaseCurrencyUnitID:  baseUnit.ID,
			QuoteCurrencyUnitID: quoteUnit.ID,
			Rate:                observation.Rate,
			Side:                observation.Side,
			Purpose:             observation.Purpose,
			ObservationAt:       observation.ObservationAt,
			PublishedAt:         observation.PublishedAt,
			RetrievedAt:         observation.RetrievedAt,
			ExpiresAt:           observation.ExpiresAt,
			RawPayloadSHA256:    observation.RawPayloadSHA256,
			SourceRevision:      observation.SourceRevision,
		})
		if err != nil {
			return FXSyncResult{}, classifyFXStoreError(err)
		}
	}
	return FXSyncResult{
		SourceCode:       source.Code,
		ObservationCount: len(observations),
		RetrievedAt:      retrievedAt,
	}, nil
}

func fetchFXObservationsAtCompletion(
	ctx context.Context,
	provider walletfx.Provider,
	source walletstore.FXSource,
	pairs []walletstore.FXSourcePair,
	now func() time.Time,
) ([]walletfx.Observation, time.Time, error) {
	if provider == nil || now == nil {
		return nil, time.Time{}, walletfx.ErrMissingProvider
	}
	// The request boundary is used only to constrain the publisher query and
	// reject future-dated publisher data. retrieved_at is an availability fact,
	// so it must be captured after every provider response has completed.
	requestAt := now().UTC().Truncate(time.Microsecond)
	observations, err := provider.Fetch(ctx, source, pairs, requestAt)
	if err != nil {
		return nil, time.Time{}, err
	}
	retrievedAt := now().UTC().Truncate(time.Microsecond)
	// Keep the audit timestamp conservative if the wall clock moves backwards
	// during the fetch. Backdating availability would let a historical quote use
	// data that was not actually present at its conversion boundary.
	if retrievedAt.Before(requestAt) {
		retrievedAt = requestAt
	}
	for index := range observations {
		observations[index].RetrievedAt = retrievedAt
	}
	return observations, retrievedAt, nil
}

func classifyFXStoreError(err error) error {
	if err == nil || !permanentFXStoreError(err) {
		return err
	}
	return temporal.NewNonRetryableApplicationError(err.Error(), FXCatalogInvalidErrorType, err)
}

func permanentFXStoreError(err error) bool {
	for _, permanent := range []error{
		walletstore.ErrFXSourceNotFound,
		walletstore.ErrCurrencyNotFound,
		walletstore.ErrFXObservationConflict,
		walletstore.ErrMissingFXSource,
		walletstore.ErrInvalidFXSource,
		walletstore.ErrMissingFXSourcePair,
		walletstore.ErrMissingCurrency,
		walletstore.ErrInvalidCurrency,
		walletstore.ErrMissingCurrencyUnitID,
		walletstore.ErrInvalidCurrencyUnitID,
		walletstore.ErrIdenticalCurrencies,
		walletstore.ErrInvalidRate,
		walletstore.ErrRateNotRepresentable,
		walletstore.ErrMissingRateSide,
		walletstore.ErrInvalidRateSide,
		walletstore.ErrMissingRatePurpose,
		walletstore.ErrInvalidRatePurpose,
		walletstore.ErrMissingObservationTime,
		walletstore.ErrMissingRetrievalTime,
		walletstore.ErrMissingExpiryTime,
		walletstore.ErrInvalidTimeRange,
		walletstore.ErrMissingPayloadHash,
		walletstore.ErrInvalidPayloadHash,
		walletstore.ErrMissingSourceRevision,
	} {
		if errors.Is(err, permanent) {
			return true
		}
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		// Data exceptions and integrity-constraint violations cannot be repaired
		// by retrying the same deterministic activity input.
		return strings.HasPrefix(postgresError.Code, "22") || strings.HasPrefix(postgresError.Code, "23")
	}
	return false
}
