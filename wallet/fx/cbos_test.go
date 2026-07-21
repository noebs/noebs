package fx

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

const cbosHTML = `<!doctype html><html><body>
<span class="date-display-single">07/03/2022</span>
<table class="field-collection-table-view other">
<thead><tr><th>The Currency Arabic</th><th>The Currency</th><th>Buying </th><th>Selling </th><th>Middle</th></tr></thead>
<tbody>
<tr class="field-collection-item"><td>الدولار</td><td>USD</td><td>445.3988</td><td>448.7393</td><td>447.0690</td></tr>
<tr class="field-collection-item"><td>الدرهم</td><td>U.A.E Dirham</td><td>121.2662</td><td>122.1757</td><td>121.7209</td></tr>
</tbody></table></body></html>`

func TestParseCBOSResponseProducesExplicitBidAskMid(t *testing.T) {
	source := testSource(ProviderCBOSHTML, "https://cbos.gov.sd/en/exchange-rates")
	pairs := []walletstore.FXSourcePair{
		testPair(1, "AED", "SDG", "U.A.E Dirham"),
		testPair(2, "USD", "SDG", "USD"),
	}
	retrieved := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)
	got, err := parseCBOSResponse([]byte(cbosHTML), source, pairs, retrieved)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("observations = %d", len(got))
	}
	if got[0].Pair.ID != 1 || got[0].Side != walletstore.FXSideAsk || !got[0].Rate.Equal(decimal.RequireFromString("122.1757")) {
		t.Fatalf("first sorted observation = %+v", got[0])
	}
	for _, observation := range got {
		if observation.ObservationAt.Format(time.DateOnly) != "2022-03-07" || observation.ExpiresAt.After(retrieved) {
			t.Fatalf("stale provenance was altered: %+v", observation)
		}
	}
}

func TestParseCBOSResponseRejectsSchemaAmbiguityAndMissingConfiguredPair(t *testing.T) {
	source := testSource(ProviderCBOSHTML, "https://cbos.gov.sd/en/exchange-rates")
	retrieved := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)
	missing := []walletstore.FXSourcePair{testPair(1, "SAR", "SDG", "Saudi Riyal")}
	if _, err := parseCBOSResponse([]byte(cbosHTML), source, missing, retrieved); !errors.Is(err, ErrObservationNotFound) {
		t.Fatalf("missing pair error = %v", err)
	}
	ambiguousDate := strings.Replace(cbosHTML, "<span class=\"date-display-single\">", "<span class=\"date-display-single\">07/03/2022</span><span class=\"date-display-single\">", 1)
	if _, err := parseCBOSResponse([]byte(ambiguousDate), source, []walletstore.FXSourcePair{testPair(1, "USD", "SDG", "USD")}, retrieved); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("ambiguous date error = %v", err)
	}
}

func TestCBOSProviderRejectsRedirectsAndMismatchedFinalResponseURL(t *testing.T) {
	source := testSource(ProviderCBOSHTML, "https://cbos.gov.sd/en/exchange-rates")
	pairs := []walletstore.FXSourcePair{testPair(1, "USD", "SDG", "USD")}
	retrievedAt := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)

	t.Run("redirect", func(t *testing.T) {
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			result := responseForRequest(request, http.StatusMovedPermanently, "text/plain", "redirect")
			result.Header.Set("Location", "https://evil.example/rates")
			return result, nil
		})}
		_, err := NewCBOSProvider(client).Fetch(context.Background(), source, pairs, retrievedAt)
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("redirect error = %v", err)
		}
		if calls != 1 {
			t.Fatalf("redirect target was requested; calls = %d", calls)
		}
	})

	t.Run("reported final URL", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			finalURL, err := url.Parse("https://evil.example/en/exchange-rates")
			if err != nil {
				t.Fatal(err)
			}
			finalRequest := request.Clone(request.Context())
			finalRequest.URL = finalURL
			return responseForRequest(finalRequest, http.StatusOK, "text/html", cbosHTML), nil
		})}
		_, err := NewCBOSProvider(client).Fetch(context.Background(), source, pairs, retrievedAt)
		if !errors.Is(err, ErrInvalidSourceHost) {
			t.Fatalf("final URL error = %v", err)
		}
	})
}
