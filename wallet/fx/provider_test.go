package fx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type stubProvider struct{}

func (stubProvider) Fetch(context.Context, walletstore.FXSource, []walletstore.FXSourcePair, time.Time) ([]Observation, error) {
	return nil, nil
}

func testSource(provider, sourceURL string) walletstore.FXSource {
	return walletstore.FXSource{
		ID:            1,
		Code:          "official-reference",
		Provider:      provider,
		Purpose:       walletstore.FXPurposeReference,
		SourceURL:     sourceURL,
		MaxAgeSeconds: 345600,
		IsEnabled:     true,
	}
}

func testPair(id int64, base, quote, series string) walletstore.FXSourcePair {
	return walletstore.FXSourcePair{
		ID:                id,
		SourceID:          1,
		BaseCurrencyCode:  base,
		QuoteCurrencyCode: quote,
		ExternalSeries:    series,
		IsEnabled:         true,
	}
}

func response(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func responseForRequest(request *http.Request, status int, contentType, body string) *http.Response {
	result := response(status, contentType, body)
	result.Request = request
	return result
}

func TestRegistryRejectsMissingDuplicateAndUnknownProviders(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("", stubProvider{}); !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("missing name error = %v", err)
	}
	if err := registry.Register("stub", stubProvider{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("stub", stubProvider{}); !errors.Is(err, ErrDuplicateProvider) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := registry.Resolve("missing"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("unknown error = %v", err)
	}
	if _, err := (*Registry)(nil).Resolve("stub"); !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("nil registry error = %v", err)
	}
}

func TestValidateFetchInputRequiresCanonicalSortedExplicitPairs(t *testing.T) {
	source := testSource(ProviderECBSDMX, "https://data-api.ecb.europa.eu/service/data/EXR")
	now := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)
	valid := []walletstore.FXSourcePair{
		testPair(1, "EUR", "GBP", "D.GBP.EUR.SP00.A"),
		testPair(2, "EUR", "USD", "D.USD.EUR.SP00.A"),
	}
	if err := validateFetchInput(source, valid, now); err != nil {
		t.Fatal(err)
	}
	if err := validateFetchInput(source, nil, now); !errors.Is(err, ErrMissingPairs) {
		t.Fatalf("missing pair error = %v", err)
	}
	reversed := []walletstore.FXSourcePair{valid[1], valid[0]}
	if err := validateFetchInput(source, reversed, now); !errors.Is(err, ErrUnsortedPairs) {
		t.Fatalf("unsorted error = %v", err)
	}
	duplicate := []walletstore.FXSourcePair{valid[0], valid[0]}
	if err := validateFetchInput(source, duplicate, now); !errors.Is(err, ErrUnsortedPairs) {
		t.Fatalf("duplicate order error = %v", err)
	}
	disabledSource := source
	disabledSource.IsEnabled = false
	if err := validateFetchInput(disabledSource, valid, now); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("disabled source error = %v", err)
	}
	disabledPair := append([]walletstore.FXSourcePair(nil), valid...)
	disabledPair[0].IsEnabled = false
	if err := validateFetchInput(source, disabledPair, now); !errors.Is(err, ErrInvalidPair) {
		t.Fatalf("disabled pair error = %v", err)
	}
}

func TestReadProviderResponseClassifiesFailures(t *testing.T) {
	if _, err := readProviderResponse(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/csv"}}}, "text/csv"); !errors.Is(err, ErrTemporary) {
		t.Fatalf("nil body error = %v", err)
	}
	if _, err := readProviderResponse(response(http.StatusTooManyRequests, "text/csv", "later"), "text/csv"); !errors.Is(err, ErrTemporary) {
		t.Fatalf("429 error = %v", err)
	}
	if _, err := readProviderResponse(response(http.StatusBadRequest, "text/csv", "bad"), "text/csv"); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("400 error = %v", err)
	}
	if _, err := readProviderResponse(response(http.StatusOK, "application/json", "{}"), "text/csv"); !errors.Is(err, ErrUnexpectedMediaType) {
		t.Fatalf("media error = %v", err)
	}
	large := strings.Repeat("x", int(maxResponseBytes)+1)
	if _, err := readProviderResponse(response(http.StatusOK, "text/csv", large), "text/csv"); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("large response error = %v", err)
	}
}

func TestValidateProviderResponseURLPinsHostPathAndQuery(t *testing.T) {
	expected, err := url.Parse("https://data-api.ecb.europa.eu/service/data/EXR/D.USD.EUR.SP00.A?endPeriod=2026-07-21&format=csvdata")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProviderResponseURL(responseForRequest(&http.Request{URL: expected}, http.StatusOK, "text/csv", "ok"), expected); err != nil {
		t.Fatalf("matching URL error = %v", err)
	}
	for _, rawURL := range []string{
		"https://evil.example/service/data/EXR/D.USD.EUR.SP00.A?endPeriod=2026-07-21&format=csvdata",
		"https://data-api.ecb.europa.eu/service/data/EXR/D.GBP.EUR.SP00.A?endPeriod=2026-07-21&format=csvdata",
		"https://data-api.ecb.europa.eu/service/data/EXR/D.USD.EUR.SP00.A?endPeriod=2026-07-20&format=csvdata",
	} {
		actual, parseErr := url.Parse(rawURL)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		got := validateProviderResponseURL(responseForRequest(&http.Request{URL: actual}, http.StatusOK, "text/csv", "ok"), expected)
		if !errors.Is(got, ErrInvalidSourceHost) {
			t.Errorf("URL %q error = %v", rawURL, got)
		}
	}
}
