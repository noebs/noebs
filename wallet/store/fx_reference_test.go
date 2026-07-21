package store

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestRateFitsNumeric38Scale18(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "0.000000000000000001", want: true},
		{value: "0.0000000000000000001", want: false},
		{value: "99999999999999999999.999999999999999999", want: true},
		{value: "100000000000000000000", want: false},
		{value: "1.2345678901234567890", want: true},
		{value: "1.2345678901234567891", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := rateFitsNumeric38Scale18(decimal.RequireFromString(tt.value)); got != tt.want {
				t.Fatalf("rateFitsNumeric38Scale18(%s) = %t, want %t", tt.value, got, tt.want)
			}
		})
	}
	for _, value := range []decimal.Decimal{
		decimal.New(1, 1_000_000),
		decimal.New(1, -1_000_000),
	} {
		if rateFitsNumeric38Scale18(value) {
			t.Fatalf("rateFitsNumeric38Scale18() accepted hostile exponent %d", value.Exponent())
		}
	}
}

func TestInverseFXSide(t *testing.T) {
	tests := map[string]string{
		FXSideBid:            FXSideAsk,
		FXSideAsk:            FXSideBid,
		FXSideMid:            FXSideMid,
		FXSideFixedReference: FXSideFixedReference,
	}
	for side, want := range tests {
		t.Run(side, func(t *testing.T) {
			if got := inverseFXSide(side); got != want {
				t.Fatalf("inverseFXSide(%q) = %q, want %q", side, got, want)
			}
		})
	}
}

func TestValidateFXObservationInputPublishedAtRange(t *testing.T) {
	observationAt := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	retrievedAt := observationAt.Add(time.Hour)
	valid := validFXObservationTestInput(observationAt, retrievedAt)

	tests := []struct {
		name        string
		publishedAt time.Time
		wantErr     error
	}{
		{name: "at observation", publishedAt: observationAt},
		{name: "at retrieval", publishedAt: retrievedAt},
		{name: "before observation", publishedAt: observationAt.Add(-time.Nanosecond), wantErr: ErrInvalidTimeRange},
		{name: "after retrieval", publishedAt: retrievedAt.Add(time.Nanosecond), wantErr: ErrInvalidTimeRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			input.PublishedAt.Valid = true
			input.PublishedAt.Time = tt.publishedAt
			err := validateFXObservationInput(input)
			if tt.wantErr == nil && err != nil {
				t.Fatalf("validateFXObservationInput() error = %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateFXObservationInput() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestFXPersistenceRejectsSubMicrosecondTimestampsInsteadOfSilentlyTruncating(t *testing.T) {
	observationAt := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	input := validFXObservationTestInput(observationAt, observationAt.Add(time.Minute))
	input.RetrievedAt = input.RetrievedAt.Add(time.Nanosecond)
	if err := validateFXObservationInput(input); !errors.Is(err, ErrInvalidTimeRange) {
		t.Fatalf("observation precision error = %v, want %v", err, ErrInvalidTimeRange)
	}
}

func TestValidateMoneyConversionQuoteInputBindsOrientationAndObservationExpiry(t *testing.T) {
	expiresAt := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	valid := MoneyConversionQuoteInput{
		TenantID:                       "tenant-fx",
		RequestedByUserID:              42,
		IdempotencyKey:                 "quote-request-1",
		MaxQuotesPerObservation:        100,
		ObservationID:                  9,
		ObservationBaseCurrencyUnitID:  11,
		ObservationQuoteCurrencyUnitID: 12,
		ObservationBaseCurrencyCode:    "EUR",
		ObservationQuoteCurrencyCode:   "USD",
		ObservationExpiresAt:           expiresAt,
		InputCurrencyUnitID:            11,
		OutputCurrencyUnitID:           12,
		InputCurrencyCode:              "EUR",
		OutputCurrencyCode:             "USD",
		InputMinorUnits:                100,
		OutputMinorUnits:               117,
		RoundingMode:                   "half_even",
		ConversionAt:                   expiresAt.Add(-time.Hour),
		ExpiresAt:                      expiresAt,
	}
	if err := validateMoneyConversionQuoteInput(valid); err != nil {
		t.Fatalf("validate forward quote: %v", err)
	}
	zeroOutput := valid
	zeroOutput.OutputMinorUnits = 0
	if err := validateMoneyConversionQuoteInput(zeroOutput); err != nil {
		t.Fatalf("validate rounded-to-zero preview quote: %v", err)
	}

	inverse := valid
	inverse.Inverse = true
	inverse.InputCurrencyUnitID, inverse.OutputCurrencyUnitID = valid.OutputCurrencyUnitID, valid.InputCurrencyUnitID
	inverse.InputCurrencyCode, inverse.OutputCurrencyCode = valid.OutputCurrencyCode, valid.InputCurrencyCode
	if err := validateMoneyConversionQuoteInput(inverse); err != nil {
		t.Fatalf("validate inverse quote: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*MoneyConversionQuoteInput)
		wantErr error
	}{
		{
			name: "forward orientation mismatch",
			mutate: func(input *MoneyConversionQuoteInput) {
				input.InputCurrencyCode, input.OutputCurrencyCode = input.OutputCurrencyCode, input.InputCurrencyCode
			},
			wantErr: ErrCurrencyMismatch,
		},
		{
			name: "quote expiry shortened",
			mutate: func(input *MoneyConversionQuoteInput) {
				input.ExpiresAt = input.ExpiresAt.Add(-time.Second)
			},
			wantErr: ErrInvalidTimeRange,
		},
		{
			name: "quote expiry extended",
			mutate: func(input *MoneyConversionQuoteInput) {
				input.ExpiresAt = input.ExpiresAt.Add(time.Second)
			},
			wantErr: ErrInvalidTimeRange,
		},
		{
			name: "missing observation expiry",
			mutate: func(input *MoneyConversionQuoteInput) {
				input.ObservationExpiresAt = time.Time{}
			},
			wantErr: ErrInvalidTimeRange,
		},
		{
			name: "missing conversion time",
			mutate: func(input *MoneyConversionQuoteInput) {
				input.ConversionAt = time.Time{}
			},
			wantErr: ErrMissingStartTime,
		},
		{
			name: "sub-microsecond conversion time",
			mutate: func(input *MoneyConversionQuoteInput) {
				input.ConversionAt = input.ConversionAt.Add(time.Nanosecond)
			},
			wantErr: ErrInvalidTimeRange,
		},
		{
			name: "conversion at expiry",
			mutate: func(input *MoneyConversionQuoteInput) {
				input.ConversionAt = input.ExpiresAt
			},
			wantErr: ErrInvalidTimeRange,
		},
		{
			name: "negative output",
			mutate: func(input *MoneyConversionQuoteInput) {
				input.OutputMinorUnits = -1
			},
			wantErr: ErrInvalidAmount,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.mutate(&input)
			if err := validateMoneyConversionQuoteInput(input); !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateMoneyConversionQuoteInput() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestFXObservationReplayIsExactAndInverseBidAskUsesOppositeSide(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	if _, err := walletStore.DB.ExecContext(ctx, `UPDATE fx_sources SET is_enabled = TRUE WHERE code = 'cbos-reference'`); err != nil {
		t.Fatalf("enable CBOS test source: %v", err)
	}
	source, err := walletStore.GetFXSource(ctx, "cbos-reference")
	if err != nil {
		t.Fatalf("get CBOS source: %v", err)
	}
	pairs, err := walletStore.ListEnabledFXSourcePairs(ctx, source.ID)
	if err != nil {
		t.Fatalf("list CBOS pairs: %v", err)
	}
	var pair FXSourcePair
	for _, candidate := range pairs {
		if candidate.BaseCurrencyCode == "AED" && candidate.QuoteCurrencyCode == "SDG" {
			pair = candidate
			break
		}
	}
	if pair.ID == 0 {
		t.Fatal("seeded AED/SDG CBOS source pair not found")
	}

	observationAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	retrievedAt := observationAt.Add(time.Minute)
	base := FXObservationInput{
		SourceID:            source.ID,
		SourcePairID:        pair.ID,
		ExternalSeries:      pair.ExternalSeries,
		BaseCurrencyCode:    pair.BaseCurrencyCode,
		QuoteCurrencyCode:   pair.QuoteCurrencyCode,
		BaseCurrencyUnitID:  testCurrencyUnitID(t, ctx, walletStore, pair.BaseCurrencyCode),
		QuoteCurrencyUnitID: testCurrencyUnitID(t, ctx, walletStore, pair.QuoteCurrencyCode),
		Purpose:             FXPurposeReference,
		ObservationAt:       observationAt,
		RetrievedAt:         retrievedAt,
		ExpiresAt:           observationAt.Add(time.Duration(source.MaxAgeSeconds) * time.Second),
		RawPayloadSHA256:    strings.Repeat("a", 64),
		SourceRevision:      "test-revision",
	}
	ask := base
	ask.Side = FXSideAsk
	ask.Rate = decimal.RequireFromString("610.25")
	storedAsk, err := walletStore.CreateFXObservation(ctx, ask)
	if err != nil {
		t.Fatalf("create ask observation: %v", err)
	}
	retriedAsk := ask
	retriedAsk.RetrievedAt = ask.RetrievedAt.Add(30 * time.Second)
	replayedAsk, err := walletStore.CreateFXObservation(ctx, retriedAsk)
	if err != nil {
		t.Fatalf("replay ask observation: %v", err)
	}
	if replayedAsk.ID != storedAsk.ID {
		t.Fatalf("replayed observation id = %d, want %d", replayedAsk.ID, storedAsk.ID)
	}
	if !replayedAsk.RetrievedAt.Equal(storedAsk.RetrievedAt) {
		t.Fatalf("replayed retrieval time = %s, want first persisted %s", replayedAsk.RetrievedAt, storedAsk.RetrievedAt)
	}
	if _, err := walletStore.DB.ExecContext(ctx,
		`UPDATE fx_observations SET rate = rate + 1 WHERE id = $1`, storedAsk.ID,
	); err == nil {
		t.Fatal("mutable FX observation accepted a rate rewrite")
	}
	conflict := ask
	conflict.Rate = conflict.Rate.Add(decimal.NewFromInt(1))
	if _, err := walletStore.CreateFXObservation(ctx, conflict); !errors.Is(err, ErrFXObservationConflict) {
		t.Fatalf("observation mismatch replay error = %v, want %v", err, ErrFXObservationConflict)
	}

	bid := base
	bid.Side = FXSideBid
	bid.Rate = decimal.RequireFromString("600.25")
	bid.RawPayloadSHA256 = strings.Repeat("b", 64)
	storedBid, err := walletStore.CreateFXObservation(ctx, bid)
	if err != nil {
		t.Fatalf("create bid observation: %v", err)
	}

	// created_at is the final database-availability boundary. Both observations
	// were persisted by the time the later insert completed.
	asOf := storedBid.CreatedAt
	reverseBid, err := walletStore.GetLatestFXObservation(ctx, source.Code, "SDG", "AED", FXSideBid, asOf)
	if err != nil {
		t.Fatalf("get inverse bid: %v", err)
	}
	if !reverseBid.Inverse || reverseBid.Observation.ID != storedAsk.ID || reverseBid.Observation.Side != FXSideAsk {
		t.Fatalf("inverse bid = %#v, want stored ask observation %d", reverseBid, storedAsk.ID)
	}
	reverseAsk, err := walletStore.GetLatestFXObservation(ctx, source.Code, "SDG", "AED", FXSideAsk, asOf)
	if err != nil {
		t.Fatalf("get inverse ask: %v", err)
	}
	if !reverseAsk.Inverse || reverseAsk.Observation.ID != storedBid.ID || reverseAsk.Observation.Side != FXSideBid {
		t.Fatalf("inverse ask = %#v, want stored bid observation %d", reverseAsk, storedBid.ID)
	}

	quoteInput := MoneyConversionQuoteInput{
		TenantID:                       tenantID,
		RequestedByUserID:              42,
		IdempotencyKey:                 "quote-request-1",
		MaxQuotesPerObservation:        1,
		ObservationID:                  storedAsk.ID,
		ObservationBaseCurrencyUnitID:  storedAsk.BaseCurrencyUnitID,
		ObservationQuoteCurrencyUnitID: storedAsk.QuoteCurrencyUnitID,
		ObservationBaseCurrencyCode:    storedAsk.BaseCurrencyCode,
		ObservationQuoteCurrencyCode:   storedAsk.QuoteCurrencyCode,
		ObservationExpiresAt:           storedAsk.ExpiresAt,
		InputCurrencyUnitID:            storedAsk.BaseCurrencyUnitID,
		OutputCurrencyUnitID:           storedAsk.QuoteCurrencyUnitID,
		InputCurrencyCode:              storedAsk.BaseCurrencyCode,
		OutputCurrencyCode:             storedAsk.QuoteCurrencyCode,
		InputMinorUnits:                100,
		OutputMinorUnits:               60_025,
		RoundingMode:                   "half_even",
		ConversionAt:                   asOf,
		ExpiresAt:                      storedAsk.ExpiresAt,
	}
	quote, err := walletStore.CreateMoneyConversionQuote(ctx, quoteInput)
	if err != nil {
		t.Fatalf("create conversion quote: %v", err)
	}
	if !quote.ConversionAt.Equal(asOf) {
		t.Fatalf("conversion_at = %s, want %s", quote.ConversionAt, asOf)
	}
	loadedQuote, err := walletStore.GetMoneyConversionQuote(ctx, tenantID, 42, quote.ID)
	if err != nil {
		t.Fatalf("get conversion quote: %v", err)
	}
	if !loadedQuote.ConversionAt.Equal(asOf) {
		t.Fatalf("loaded conversion_at = %s, want %s", loadedQuote.ConversionAt, asOf)
	}
	loadedByKey, err := walletStore.GetMoneyConversionQuoteByIdempotency(
		ctx, tenantID, 42, quoteInput.IdempotencyKey,
	)
	if err != nil || loadedByKey.ID != quote.ID {
		t.Fatalf("get conversion quote by idempotency = %+v, %v; want %s", loadedByKey, err, quote.ID)
	}
	replayInput := quoteInput
	replayInput.ConversionAt = replayInput.ConversionAt.Add(time.Microsecond)
	replayedQuote, err := walletStore.CreateMoneyConversionQuote(ctx, replayInput)
	if err != nil || replayedQuote.ID != quote.ID || !replayedQuote.ConversionAt.Equal(quote.ConversionAt) {
		t.Fatalf("idempotent quote replay = %+v, %v; want original %+v", replayedQuote, err, quote)
	}
	limitedInput := quoteInput
	limitedInput.IdempotencyKey = "quote-request-2"
	if _, err := walletStore.CreateMoneyConversionQuote(ctx, limitedInput); !errors.Is(err, ErrConversionQuoteLimitExceeded) {
		t.Fatalf("quote quota error = %v, want %v", err, ErrConversionQuoteLimitExceeded)
	}

	const concurrentAttempts = 8
	results := make(chan error, concurrentAttempts)
	var attempts sync.WaitGroup
	for index := range concurrentAttempts {
		attempts.Add(1)
		go func(index int) {
			defer attempts.Done()
			input := quoteInput
			input.IdempotencyKey = fmt.Sprintf("quote-concurrent-%d", index)
			input.MaxQuotesPerObservation = 3
			_, createErr := walletStore.CreateMoneyConversionQuote(ctx, input)
			results <- createErr
		}(index)
	}
	attempts.Wait()
	close(results)
	succeeded, limited := 0, 0
	for result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, ErrConversionQuoteLimitExceeded):
			limited++
		default:
			t.Fatalf("concurrent quote error = %v", result)
		}
	}
	if succeeded != 2 || limited != concurrentAttempts-2 {
		t.Fatalf("concurrent quota results = %d succeeded/%d limited, want 2/%d", succeeded, limited, concurrentAttempts-2)
	}
	if _, err := walletStore.DB.ExecContext(ctx,
		`UPDATE money_conversion_quotes SET output_minor_units = output_minor_units + 1 WHERE id = $1`, quote.ID,
	); err == nil {
		t.Fatal("mutable conversion quote accepted an output rewrite")
	}
}

func TestGetLatestFXObservationDoesNotUseFutureRetrieval(t *testing.T) {
	ctx, walletStore, _ := newWalletStoreIntegration(t)
	if _, err := walletStore.DB.ExecContext(ctx, `UPDATE fx_sources SET is_enabled = TRUE WHERE code = 'cbos-reference'`); err != nil {
		t.Fatalf("enable CBOS test source: %v", err)
	}
	source, err := walletStore.GetFXSource(ctx, "cbos-reference")
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := walletStore.ListEnabledFXSourcePairs(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	var pair FXSourcePair
	for _, candidate := range pairs {
		if candidate.BaseCurrencyCode == "AED" && candidate.QuoteCurrencyCode == "SDG" {
			pair = candidate
			break
		}
	}
	if pair.ID == 0 {
		t.Fatal("seeded AED/SDG CBOS source pair not found")
	}

	requestBoundary := time.Now().UTC().Truncate(time.Microsecond)
	base := FXObservationInput{
		SourceID:            source.ID,
		SourcePairID:        pair.ID,
		ExternalSeries:      pair.ExternalSeries,
		BaseCurrencyCode:    pair.BaseCurrencyCode,
		QuoteCurrencyCode:   pair.QuoteCurrencyCode,
		BaseCurrencyUnitID:  testCurrencyUnitID(t, ctx, walletStore, pair.BaseCurrencyCode),
		QuoteCurrencyUnitID: testCurrencyUnitID(t, ctx, walletStore, pair.QuoteCurrencyCode),
		Rate:                decimal.RequireFromString("600"),
		Side:                FXSideMid,
		Purpose:             FXPurposeReference,
		ObservationAt:       requestBoundary.Add(-2 * time.Hour),
		RetrievedAt:         requestBoundary.Add(-90 * time.Minute),
		RawPayloadSHA256:    strings.Repeat("c", 64),
		SourceRevision:      "known-at-as-of",
	}
	base.ExpiresAt = base.ObservationAt.Add(time.Duration(source.MaxAgeSeconds) * time.Second)
	known, err := walletStore.CreateFXObservation(ctx, base)
	if err != nil {
		t.Fatalf("create known observation: %v", err)
	}

	future := base
	future.ObservationAt = requestBoundary.Add(-time.Hour)
	future.RetrievedAt = requestBoundary.Add(time.Hour)
	future.ExpiresAt = future.ObservationAt.Add(time.Duration(source.MaxAgeSeconds) * time.Second)
	future.Rate = decimal.RequireFromString("999")
	future.RawPayloadSHA256 = strings.Repeat("d", 64)
	future.SourceRevision = "retrieved-after-as-of"
	storedFuture, err := walletStore.CreateFXObservation(ctx, future)
	if err != nil {
		t.Fatalf("create future-retrieved observation: %v", err)
	}
	// Choose a boundary after both database inserts, while the second row still
	// claims a later retrieval time. This isolates retrieved_at from created_at.
	asOf := storedFuture.CreatedAt

	selected, err := walletStore.GetLatestFXObservation(
		ctx, source.Code, pair.BaseCurrencyCode, pair.QuoteCurrencyCode, FXSideMid, asOf,
	)
	if err != nil {
		t.Fatalf("get observation as of historical time: %v", err)
	}
	if selected.Observation.ID != known.ID {
		t.Fatalf("selected observation %d retrieved at %s, want known observation %d", selected.Observation.ID, selected.Observation.RetrievedAt, known.ID)
	}
}

func TestFXObservationInsertedAfterAsOfIsNotHistoricallyAvailable(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	if _, err := walletStore.DB.ExecContext(ctx, `UPDATE fx_sources SET is_enabled = TRUE WHERE code = 'cbos-reference'`); err != nil {
		t.Fatalf("enable CBOS test source: %v", err)
	}
	source, err := walletStore.GetFXSource(ctx, "cbos-reference")
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := walletStore.ListEnabledFXSourcePairs(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	var pair FXSourcePair
	for _, candidate := range pairs {
		if candidate.BaseCurrencyCode == "AED" && candidate.QuoteCurrencyCode == "SDG" {
			pair = candidate
			break
		}
	}
	if pair.ID == 0 {
		t.Fatal("seeded AED/SDG CBOS source pair not found")
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	base := FXObservationInput{
		SourceID:            source.ID,
		SourcePairID:        pair.ID,
		ExternalSeries:      pair.ExternalSeries,
		BaseCurrencyCode:    pair.BaseCurrencyCode,
		QuoteCurrencyCode:   pair.QuoteCurrencyCode,
		BaseCurrencyUnitID:  testCurrencyUnitID(t, ctx, walletStore, pair.BaseCurrencyCode),
		QuoteCurrencyUnitID: testCurrencyUnitID(t, ctx, walletStore, pair.QuoteCurrencyCode),
		Rate:                decimal.RequireFromString("600"),
		Side:                FXSideMid,
		Purpose:             FXPurposeReference,
		ObservationAt:       now.Add(-2 * time.Hour),
		RetrievedAt:         now.Add(-90 * time.Minute),
		RawPayloadSHA256:    strings.Repeat("1", 64),
		SourceRevision:      "available-before-as-of",
	}
	base.ExpiresAt = base.ObservationAt.Add(time.Duration(source.MaxAgeSeconds) * time.Second)
	known, err := walletStore.CreateFXObservation(ctx, base)
	if err != nil {
		t.Fatalf("create historically available observation: %v", err)
	}

	// Put the conversion boundary strictly after the first insert and strictly
	// before the second. Both publisher/retrieval timestamps predate the boundary;
	// only database availability distinguishes the two observations.
	asOf := known.CreatedAt.Add(time.Microsecond)
	if _, err := walletStore.DB.ExecContext(ctx, `SELECT pg_sleep(0.002)`); err != nil {
		t.Fatalf("wait for a distinct database availability boundary: %v", err)
	}
	lateInput := base
	lateInput.ObservationAt = now.Add(-time.Hour)
	lateInput.RetrievedAt = now.Add(-30 * time.Minute)
	lateInput.ExpiresAt = lateInput.ObservationAt.Add(time.Duration(source.MaxAgeSeconds) * time.Second)
	lateInput.Rate = decimal.RequireFromString("999")
	lateInput.RawPayloadSHA256 = strings.Repeat("2", 64)
	lateInput.SourceRevision = "inserted-after-as-of"
	late, err := walletStore.CreateFXObservation(ctx, lateInput)
	if err != nil {
		t.Fatalf("create late-inserted observation: %v", err)
	}
	if !late.CreatedAt.After(asOf) {
		t.Fatalf("test precondition: late created_at = %s, want after as-of %s", late.CreatedAt, asOf)
	}

	selected, err := walletStore.GetLatestFXObservation(
		ctx, source.Code, pair.BaseCurrencyCode, pair.QuoteCurrencyCode, FXSideMid, asOf,
	)
	if err != nil {
		t.Fatalf("get historical observation: %v", err)
	}
	if selected.Observation.ID != known.ID {
		t.Fatalf("selected late observation %d, want historically available observation %d", selected.Observation.ID, known.ID)
	}

	_, err = walletStore.CreateMoneyConversionQuote(ctx, MoneyConversionQuoteInput{
		TenantID:                       tenantID,
		RequestedByUserID:              42,
		IdempotencyKey:                 "late-observation-quote",
		MaxQuotesPerObservation:        10,
		ObservationID:                  late.ID,
		ObservationBaseCurrencyUnitID:  late.BaseCurrencyUnitID,
		ObservationQuoteCurrencyUnitID: late.QuoteCurrencyUnitID,
		ObservationBaseCurrencyCode:    late.BaseCurrencyCode,
		ObservationQuoteCurrencyCode:   late.QuoteCurrencyCode,
		ObservationExpiresAt:           late.ExpiresAt,
		InputCurrencyUnitID:            late.BaseCurrencyUnitID,
		OutputCurrencyUnitID:           late.QuoteCurrencyUnitID,
		InputCurrencyCode:              late.BaseCurrencyCode,
		OutputCurrencyCode:             late.QuoteCurrencyCode,
		InputMinorUnits:                100,
		OutputMinorUnits:               99_900,
		RoundingMode:                   "half_even",
		ConversionAt:                   asOf,
		ExpiresAt:                      late.ExpiresAt,
	})
	if err == nil {
		t.Fatal("database accepted a quote using an observation inserted after conversion_at")
	}
}

func TestMoneyQuoteDatabasePolicyRejectsInvalidSnapshotsAndProvenanceMutation(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	source, err := walletStore.GetFXSource(ctx, "ecb-reference")
	if err != nil {
		t.Fatalf("get ECB source: %v", err)
	}
	pairs, err := walletStore.ListEnabledFXSourcePairs(ctx, source.ID)
	if err != nil {
		t.Fatalf("list ECB pairs: %v", err)
	}
	var pair FXSourcePair
	for _, candidate := range pairs {
		if candidate.BaseCurrencyCode == "EUR" && candidate.QuoteCurrencyCode == "USD" {
			pair = candidate
			break
		}
	}
	if pair.ID == 0 {
		t.Fatal("seeded EUR/USD ECB pair not found")
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	observationAt := now.Add(-2 * time.Hour)
	retrievedAt := now.Add(-time.Hour)
	observation, err := walletStore.CreateFXObservation(ctx, FXObservationInput{
		SourceID:            source.ID,
		SourcePairID:        pair.ID,
		ExternalSeries:      pair.ExternalSeries,
		BaseCurrencyCode:    pair.BaseCurrencyCode,
		QuoteCurrencyCode:   pair.QuoteCurrencyCode,
		BaseCurrencyUnitID:  testCurrencyUnitID(t, ctx, walletStore, pair.BaseCurrencyCode),
		QuoteCurrencyUnitID: testCurrencyUnitID(t, ctx, walletStore, pair.QuoteCurrencyCode),
		Rate:                decimal.RequireFromString("1.125"),
		Side:                FXSideMid,
		Purpose:             FXPurposeReference,
		ObservationAt:       observationAt,
		RetrievedAt:         retrievedAt,
		ExpiresAt:           observationAt.Add(time.Duration(source.MaxAgeSeconds) * time.Second),
		RawPayloadSHA256:    strings.Repeat("e", 64),
		SourceRevision:      "db-policy-test",
	})
	if err != nil {
		t.Fatalf("create policy-test observation: %v", err)
	}
	conversionAt := observation.CreatedAt
	valid := MoneyConversionQuoteInput{
		TenantID:                       tenantID,
		RequestedByUserID:              42,
		IdempotencyKey:                 "quote-policy-request",
		MaxQuotesPerObservation:        100,
		ObservationID:                  observation.ID,
		ObservationBaseCurrencyUnitID:  observation.BaseCurrencyUnitID,
		ObservationQuoteCurrencyUnitID: observation.QuoteCurrencyUnitID,
		ObservationBaseCurrencyCode:    observation.BaseCurrencyCode,
		ObservationQuoteCurrencyCode:   observation.QuoteCurrencyCode,
		ObservationExpiresAt:           observation.ExpiresAt,
		InputCurrencyUnitID:            observation.BaseCurrencyUnitID,
		OutputCurrencyUnitID:           observation.QuoteCurrencyUnitID,
		InputCurrencyCode:              observation.BaseCurrencyCode,
		OutputCurrencyCode:             observation.QuoteCurrencyCode,
		InputMinorUnits:                100,
		OutputMinorUnits:               113,
		RoundingMode:                   "half_even",
		ConversionAt:                   conversionAt,
		ExpiresAt:                      observation.ExpiresAt,
	}

	t.Run("future conversion", func(t *testing.T) {
		input := valid
		input.ConversionAt = conversionAt.Add(time.Minute)
		if _, err := walletStore.CreateMoneyConversionQuote(ctx, input); err == nil {
			t.Fatal("future conversion timestamp was accepted")
		}
	})

	t.Run("observation not yet retrieved", func(t *testing.T) {
		input := valid
		input.ConversionAt = retrievedAt.Add(-time.Minute)
		if _, err := walletStore.CreateMoneyConversionQuote(ctx, input); err == nil {
			t.Fatal("observation retrieved after conversion time was accepted")
		}
	})

	t.Run("stale observation", func(t *testing.T) {
		input := valid
		input.ConversionAt = observation.ExpiresAt
		if _, err := walletStore.CreateMoneyConversionQuote(ctx, input); err == nil {
			t.Fatal("stale observation was accepted")
		}
	})

	t.Run("disabled source", func(t *testing.T) {
		if _, err := walletStore.DB.ExecContext(ctx, `UPDATE fx_sources SET is_enabled = FALSE WHERE id = $1`, source.ID); err != nil {
			t.Fatalf("disable source: %v", err)
		}
		if _, err := walletStore.CreateMoneyConversionQuote(ctx, valid); err == nil {
			t.Fatal("disabled source was accepted")
		}
		if _, err := walletStore.DB.ExecContext(ctx, `UPDATE fx_sources SET is_enabled = TRUE WHERE id = $1`, source.ID); err != nil {
			t.Fatalf("re-enable source: %v", err)
		}
	})

	t.Run("disabled pair", func(t *testing.T) {
		if _, err := walletStore.DB.ExecContext(ctx, `UPDATE fx_source_pairs SET is_enabled = FALSE WHERE id = $1`, pair.ID); err != nil {
			t.Fatalf("disable pair: %v", err)
		}
		if _, err := walletStore.CreateMoneyConversionQuote(ctx, valid); err == nil {
			t.Fatal("disabled pair was accepted")
		}
		if _, err := walletStore.DB.ExecContext(ctx, `UPDATE fx_source_pairs SET is_enabled = TRUE WHERE id = $1`, pair.ID); err != nil {
			t.Fatalf("re-enable pair: %v", err)
		}
	})

	t.Run("ineffective input unit", func(t *testing.T) {
		var historicalUnitID int64
		if err := walletStore.DB.GetContext(ctx, &historicalUnitID, `INSERT INTO currency_unit_versions(
			currency_code, iso_minor_exponent, display_exponent, cash_exponent,
			cash_rounding_increment, valid_from, valid_to, source, source_revision, source_published_on
		) VALUES('EUR', 2, 2, 2, 1, '2025-01-01', '2026-01-01', 'test', 'historical-test', '2025-01-01')
		RETURNING id`); err != nil {
			t.Fatalf("insert historical unit: %v", err)
		}
		input := valid
		input.InputCurrencyUnitID = historicalUnitID
		if _, err := walletStore.CreateMoneyConversionQuote(ctx, input); err == nil {
			t.Fatal("ineffective input unit was accepted")
		}
	})

	for name, statement := range map[string]string{
		"source URL":    `UPDATE fx_sources SET source_url = 'https://example.invalid' WHERE id = $1`,
		"pair series":   `UPDATE fx_source_pairs SET external_series = 'rewritten' WHERE id = $1`,
		"source delete": `DELETE FROM fx_sources WHERE id = $1`,
		"pair delete":   `DELETE FROM fx_source_pairs WHERE id = $1`,
	} {
		t.Run("immutable "+name, func(t *testing.T) {
			id := source.ID
			if strings.HasPrefix(name, "pair") {
				id = pair.ID
			}
			if _, err := walletStore.DB.ExecContext(ctx, statement, id); err == nil {
				t.Fatalf("FX provenance mutation %q was accepted", name)
			}
		})
	}

	var quoteCount int
	if err := walletStore.DB.GetContext(ctx, &quoteCount,
		`SELECT count(*) FROM money_conversion_quotes WHERE tenant_id = $1`, tenantID,
	); err != nil {
		t.Fatalf("count rejected quotes: %v", err)
	}
	if quoteCount != 0 {
		t.Fatalf("rejected quote count = %d, want 0", quoteCount)
	}
}

func TestFXObservationTemporalRetryReplayIgnoresOnlyAttemptRetrievalTime(t *testing.T) {
	observationAt := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	input := validFXObservationTestInput(observationAt, observationAt.Add(time.Minute))
	stored := FXObservation{
		SourceID:            input.SourceID,
		SourcePairID:        input.SourcePairID,
		ExternalSeries:      input.ExternalSeries,
		BaseCurrencyCode:    input.BaseCurrencyCode,
		QuoteCurrencyCode:   input.QuoteCurrencyCode,
		BaseCurrencyUnitID:  input.BaseCurrencyUnitID,
		QuoteCurrencyUnitID: input.QuoteCurrencyUnitID,
		Rate:                input.Rate,
		Side:                input.Side,
		Purpose:             input.Purpose,
		ObservationAt:       input.ObservationAt,
		PublishedAt:         input.PublishedAt,
		RetrievedAt:         input.RetrievedAt,
		ExpiresAt:           input.ExpiresAt,
		RawPayloadSHA256:    input.RawPayloadSHA256,
		SourceRevision:      input.SourceRevision,
	}

	retry := input
	retry.RetrievedAt = input.RetrievedAt.Add(time.Hour)
	if !fxObservationMatchesInput(stored, retry) {
		t.Fatal("attempt retrieval time made an otherwise exact replay conflict")
	}

	materialConflict := retry
	materialConflict.ExpiresAt = materialConflict.ExpiresAt.Add(time.Second)
	if fxObservationMatchesInput(stored, materialConflict) {
		t.Fatal("material expiry conflict was accepted as a replay")
	}
	materialConflict = retry
	materialConflict.SourceRevision = "different-revision"
	if fxObservationMatchesInput(stored, materialConflict) {
		t.Fatal("material source revision conflict was accepted as a replay")
	}
}

func validFXObservationTestInput(observationAt, retrievedAt time.Time) FXObservationInput {
	return FXObservationInput{
		SourceID:            1,
		SourcePairID:        2,
		ExternalSeries:      "D.USD.EUR.SP00.A",
		BaseCurrencyCode:    "EUR",
		QuoteCurrencyCode:   "USD",
		BaseCurrencyUnitID:  3,
		QuoteCurrencyUnitID: 4,
		Rate:                decimal.RequireFromString("1.17"),
		Side:                FXSideMid,
		Purpose:             FXPurposeReference,
		ObservationAt:       observationAt,
		RetrievedAt:         retrievedAt,
		ExpiresAt:           observationAt.Add(24 * time.Hour),
		RawPayloadSHA256:    strings.Repeat("a", 64),
		SourceRevision:      "test-revision",
	}
}
