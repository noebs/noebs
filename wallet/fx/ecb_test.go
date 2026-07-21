package fx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

func TestECBProviderFetchesConfiguredPairsWithBoundedConcurrency(t *testing.T) {
	source := testSource(ProviderECBSDMX, "https://data-api.ecb.europa.eu/service/data/EXR")
	pairs := []walletstore.FXSourcePair{
		testPair(1, "EUR", "CHF", "D.CHF.EUR.SP00.A"),
		testPair(2, "EUR", "GBP", "D.GBP.EUR.SP00.A"),
		testPair(3, "EUR", "JPY", "D.JPY.EUR.SP00.A"),
		testPair(4, "EUR", "USD", "D.USD.EUR.SP00.A"),
	}
	started := make(chan struct{}, len(pairs))
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
		path := request.URL.EscapedPath()
		series, err := url.PathUnescape(path[strings.LastIndex(path, "/")+1:])
		if err != nil {
			return nil, err
		}
		parts := strings.Split(series, ".")
		if len(parts) != 5 {
			return nil, fmt.Errorf("unexpected series %q", series)
		}
		body := fmt.Sprintf("KEY,CURRENCY,CURRENCY_DENOM,TIME_PERIOD,OBS_VALUE,OBS_STATUS\nEXR.%s,%s,%s,2026-07-20,1.1,A\n", series, parts[1], parts[2])
		return responseForRequest(request, http.StatusOK, "text/csv", body), nil
	})}

	done := make(chan error, 1)
	go func() {
		_, err := NewECBProvider(client).Fetch(
			context.Background(), source, pairs, time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC),
		)
		done <- err
	}()
	for range pairs {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			<-done
			t.Fatal("ECB pair requests did not run concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("concurrent ECB fetch: %v", err)
	}
}

const ecbCSV = `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE,OBS_STATUS
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2026-07-17,1.1435,A
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2026-07-20,1.1426,A
`

func TestParseECBResponseSelectsLatestExactObservation(t *testing.T) {
	source := testSource(ProviderECBSDMX, "https://data-api.ecb.europa.eu/service/data/EXR")
	pair := testPair(1, "EUR", "USD", "D.USD.EUR.SP00.A")
	retrieved := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)
	got, err := parseECBResponse([]byte(ecbCSV), source, pair, retrieved)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Rate.Equal(decimal.RequireFromString("1.1426")) || got.Side != walletstore.FXSideMid || got.Pair != pair {
		t.Fatalf("unexpected observation: %+v", got)
	}
	if got.ObservationAt.Format(time.DateOnly) != "2026-07-20" || got.RetrievedAt != retrieved {
		t.Fatalf("unexpected times: %+v", got)
	}
	if got.ExpiresAt != got.ObservationAt.Add(96*time.Hour) {
		t.Fatalf("expiry = %s", got.ExpiresAt)
	}
	if len(got.RawPayloadSHA256) != 64 || got.SourceRevision != "D.USD.EUR.SP00.A:2026-07-20:A" {
		t.Fatalf("provenance = %+v", got)
	}
}

func TestParseECBResponseRejectsWrongDirectionMalformedRatesAndMissingRows(t *testing.T) {
	source := testSource(ProviderECBSDMX, "https://data-api.ecb.europa.eu/service/data/EXR")
	pair := testPair(1, "EUR", "USD", "D.USD.EUR.SP00.A")
	now := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)
	wrong := `KEY,CURRENCY,CURRENCY_DENOM,TIME_PERIOD,OBS_VALUE,OBS_STATUS
EXR.D.USD.EUR.SP00.A,EUR,USD,2026-07-20,1.2,A
`
	if _, err := parseECBResponse([]byte(wrong), source, pair, now); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("direction error = %v", err)
	}
	badRate := `KEY,CURRENCY,CURRENCY_DENOM,TIME_PERIOD,OBS_VALUE,OBS_STATUS
EXR.D.USD.EUR.SP00.A,USD,EUR,2026-07-20,NaN,A
`
	if _, err := parseECBResponse([]byte(badRate), source, pair, now); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("rate error = %v", err)
	}
	futureOnly := `KEY,CURRENCY,CURRENCY_DENOM,TIME_PERIOD,OBS_VALUE,OBS_STATUS
EXR.D.USD.EUR.SP00.A,USD,EUR,2026-07-22,1.2,A
`
	if _, err := parseECBResponse([]byte(futureOnly), source, pair, now); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("future error = %v", err)
	}
	mixedFuture := `KEY,CURRENCY,CURRENCY_DENOM,TIME_PERIOD,OBS_VALUE,OBS_STATUS
EXR.D.USD.EUR.SP00.A,USD,EUR,2026-07-20,1.1,A
EXR.D.USD.EUR.SP00.A,USD,EUR,2026-07-22,1.2,A
`
	if _, err := parseECBResponse([]byte(mixedFuture), source, pair, now); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("mixed future error = %v", err)
	}
}

func TestECBProviderUsesOnlyPinnedOfficialEndpoint(t *testing.T) {
	called := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called++
		if request.URL.Scheme != "https" || request.URL.Host != ecbHost || request.URL.Path != "/service/data/EXR/D.USD.EUR.SP00.A" {
			t.Fatalf("unexpected URL %s", request.URL)
		}
		if request.URL.Query().Get("format") != "csvdata" || request.Method != http.MethodGet {
			t.Fatalf("unexpected request %s", request.URL)
		}
		if request.URL.Query().Get("startPeriod") != "2026-07-17" || request.URL.Query().Get("endPeriod") != "2026-07-21" {
			t.Fatalf("unexpected freshness window %s", request.URL.RawQuery)
		}
		return response(http.StatusOK, "text/csv; charset=utf-8", ecbCSV), nil
	})}
	provider := NewECBProvider(client)
	source := testSource(ProviderECBSDMX, "https://data-api.ecb.europa.eu/service/data/EXR")
	pair := testPair(1, "EUR", "USD", "D.USD.EUR.SP00.A")
	observations, err := provider.Fetch(context.Background(), source, []walletstore.FXSourcePair{pair}, time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC))
	if err != nil || len(observations) != 1 || called != 1 {
		t.Fatalf("Fetch = %+v, %v; calls=%d", observations, err, called)
	}

	for _, badURL := range []string{
		"http://data-api.ecb.europa.eu/service/data/EXR",
		"https://evil.example/service/data/EXR",
		"https://data-api.ecb.europa.eu/service/data/EXR?redirect=evil",
		"https://data-api.ecb.europa.eu/service/data/EXR?",
		"https://data-api.ecb.europa.eu/service/data/%45XR",
		"https://user@data-api.ecb.europa.eu/service/data/EXR",
	} {
		source.SourceURL = badURL
		if _, err := provider.Fetch(context.Background(), source, []walletstore.FXSourcePair{pair}, time.Now().UTC()); !errors.Is(err, ErrInvalidSourceHost) {
			t.Errorf("URL %q error = %v", badURL, err)
		}
	}
}

func TestECBProviderRejectsNonCanonicalConfiguredSeriesBeforeHTTP(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called = true
		return responseForRequest(request, http.StatusOK, "text/csv", ecbCSV), nil
	})}
	source := testSource(ProviderECBSDMX, "https://data-api.ecb.europa.eu/service/data/EXR")
	pair := testPair(1, "EUR", "USD", "D.EUR.USD.SP00.A")
	_, err := NewECBProvider(client).Fetch(context.Background(), source, []walletstore.FXSourcePair{pair}, time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrInvalidPair) {
		t.Fatalf("series error = %v", err)
	}
	if called {
		t.Fatal("HTTP called for noncanonical series")
	}
}

func TestParseECBResponseRejectsDuplicateSelectedDateAndNonNormalStatus(t *testing.T) {
	source := testSource(ProviderECBSDMX, "https://data-api.ecb.europa.eu/service/data/EXR")
	pair := testPair(1, "EUR", "USD", "D.USD.EUR.SP00.A")
	retrievedAt := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)

	duplicate := `KEY,CURRENCY,CURRENCY_DENOM,TIME_PERIOD,OBS_VALUE,OBS_STATUS
EXR.D.USD.EUR.SP00.A,USD,EUR,2026-07-20,1.1426,A
EXR.D.USD.EUR.SP00.A,USD,EUR,2026-07-20,1.1426,A
`
	if _, err := parseECBResponse([]byte(duplicate), source, pair, retrievedAt); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("duplicate error = %v", err)
	}

	nonNormal := `KEY,CURRENCY,CURRENCY_DENOM,TIME_PERIOD,OBS_VALUE,OBS_STATUS
EXR.D.USD.EUR.SP00.A,USD,EUR,2026-07-20,1.1426,E
`
	if _, err := parseECBResponse([]byte(nonNormal), source, pair, retrievedAt); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("status error = %v", err)
	}
}

func TestECBProviderRejectsRedirectsAndMismatchedFinalResponseURL(t *testing.T) {
	source := testSource(ProviderECBSDMX, "https://data-api.ecb.europa.eu/service/data/EXR")
	pair := testPair(1, "EUR", "USD", "D.USD.EUR.SP00.A")
	retrievedAt := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)

	t.Run("redirect", func(t *testing.T) {
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			result := responseForRequest(request, http.StatusFound, "text/plain", "redirect")
			result.Header.Set("Location", "https://evil.example/rates")
			return result, nil
		})}
		_, err := NewECBProvider(client).Fetch(context.Background(), source, []walletstore.FXSourcePair{pair}, retrievedAt)
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("redirect error = %v", err)
		}
		if calls != 1 {
			t.Fatalf("redirect target was requested; calls = %d", calls)
		}
	})

	t.Run("reported final URL", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			finalURL, err := url.Parse("https://evil.example/service/data/EXR/D.USD.EUR.SP00.A?format=csvdata")
			if err != nil {
				t.Fatal(err)
			}
			finalRequest := request.Clone(request.Context())
			finalRequest.URL = finalURL
			return responseForRequest(finalRequest, http.StatusOK, "text/csv", ecbCSV), nil
		})}
		_, err := NewECBProvider(client).Fetch(context.Background(), source, []walletstore.FXSourcePair{pair}, retrievedAt)
		if !errors.Is(err, ErrInvalidSourceHost) {
			t.Fatalf("final URL error = %v", err)
		}
	})
}
