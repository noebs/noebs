package activity

import (
	"context"
	"errors"
	"testing"
	"time"

	walletfx "github.com/adonese/noebs/wallet/fx"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/jackc/pgx/v5/pgconn"
	"go.temporal.io/sdk/temporal"
)

type completionAwareFXProvider struct {
	requestAt time.Time
	completed bool
}

func (p *completionAwareFXProvider) Fetch(
	_ context.Context,
	_ walletstore.FXSource,
	_ []walletstore.FXSourcePair,
	requestAt time.Time,
) ([]walletfx.Observation, error) {
	p.requestAt = requestAt
	p.completed = true
	return []walletfx.Observation{{RetrievedAt: requestAt}}, nil
}

func TestFetchFXObservationsStampsAvailabilityAfterProviderCompletion(t *testing.T) {
	requestAt := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	completionAt := requestAt.Add(37 * time.Second)
	provider := &completionAwareFXProvider{}
	clockCalls := 0
	clock := func() time.Time {
		clockCalls++
		if clockCalls == 2 && !provider.completed {
			t.Fatal("completion timestamp was captured before the provider returned")
		}
		if clockCalls == 1 {
			return requestAt
		}
		return completionAt
	}

	observations, retrievedAt, err := fetchFXObservationsAtCompletion(
		t.Context(), provider, walletstore.FXSource{}, nil, clock,
	)
	if err != nil {
		t.Fatalf("fetchFXObservationsAtCompletion() error = %v", err)
	}
	if !provider.requestAt.Equal(requestAt) {
		t.Fatalf("provider request boundary = %s, want %s", provider.requestAt, requestAt)
	}
	if !retrievedAt.Equal(completionAt) || len(observations) != 1 ||
		!observations[0].RetrievedAt.Equal(completionAt) {
		t.Fatalf("retrieval availability = %s/%v, want completion %s", retrievedAt, observations, completionAt)
	}
}

func TestClassifyFXStoreErrorStopsPermanentRetries(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "typed catalog failure", err: walletstore.ErrCurrencyNotFound},
		{name: "data exception", err: &pgconn.PgError{Code: "22007", Message: "bad timestamp"}},
		{name: "constraint failure", err: &pgconn.PgError{Code: "23514", Message: "check violation"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			classified := classifyFXStoreError(test.err)
			var applicationError *temporal.ApplicationError
			if !errors.As(classified, &applicationError) || !applicationError.NonRetryable() {
				t.Fatalf("classified error = %v, want non-retryable Temporal application error", classified)
			}
			if applicationError.Type() != FXCatalogInvalidErrorType {
				t.Fatalf("error type = %q, want %q", applicationError.Type(), FXCatalogInvalidErrorType)
			}
		})
	}
}

func TestClassifyFXStoreErrorPreservesTransientFailures(t *testing.T) {
	transient := errors.New("connection reset")
	if got := classifyFXStoreError(transient); got != transient {
		t.Fatalf("transient error = %v, want original %v", got, transient)
	}
	if got := classifyFXStoreError(nil); got != nil {
		t.Fatalf("nil error = %v", got)
	}
}
