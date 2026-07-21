package fx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

const ecbHost = "data-api.ecb.europa.eu"

const ecbNormalObservationStatus = "A"

const ecbMaxConcurrentRequests = 4

type ECBProvider struct {
	client *http.Client
}

func NewECBProvider(client *http.Client) *ECBProvider {
	return &ECBProvider{client: client}
}

func (p *ECBProvider) Fetch(ctx context.Context, source walletstore.FXSource, pairs []walletstore.FXSourcePair, retrievedAt time.Time) ([]Observation, error) {
	if p == nil || p.client == nil {
		return nil, ErrMissingProvider
	}
	if err := validateFetchInput(source, pairs, retrievedAt); err != nil {
		return nil, err
	}
	baseURL, err := validateProviderURL(source.SourceURL, ecbHost, "/service/data/EXR")
	if err != nil {
		return nil, err
	}
	if source.Provider != ProviderECBSDMX || source.Purpose != walletstore.FXPurposeReference {
		return nil, ErrInvalidSource
	}
	for _, pair := range pairs {
		expectedSeries := "D." + pair.QuoteCurrencyCode + "." + pair.BaseCurrencyCode + ".SP00.A"
		if pair.ExternalSeries != expectedSeries {
			return nil, ErrInvalidPair
		}
	}

	observations := make([]Observation, len(pairs))
	fetchContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	workerCount := min(ecbMaxConcurrentRequests, len(pairs))
	var workers sync.WaitGroup
	var firstError error
	var errorOnce sync.Once
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				observation, fetchErr := p.fetchPair(fetchContext, source, pairs[index], baseURL, retrievedAt.UTC())
				if fetchErr != nil {
					errorOnce.Do(func() {
						firstError = fmt.Errorf("ECB series %s: %w", pairs[index].ExternalSeries, fetchErr)
						cancel()
					})
					continue
				}
				observations[index] = observation
			}
		}()
	}
enqueue:
	for index := range pairs {
		select {
		case jobs <- index:
		case <-fetchContext.Done():
			break enqueue
		}
	}
	close(jobs)
	workers.Wait()
	if firstError != nil {
		return nil, firstError
	}
	sortObservations(observations)
	return observations, nil
}

func validateProviderURL(rawURL, expectedHost, expectedPath string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, providerError{kind: ErrInvalidSource, err: err}
	}
	if parsed.Scheme != "https" || parsed.Host != expectedHost || parsed.Path != expectedPath || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.User != nil || parsed.Opaque != "" {
		return nil, ErrInvalidSourceHost
	}
	return parsed, nil
}

func (p *ECBProvider) fetchPair(ctx context.Context, source walletstore.FXSource, pair walletstore.FXSourcePair, baseURL *url.URL, retrievedAt time.Time) (Observation, error) {
	endpoint := *baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + url.PathEscape(pair.ExternalSeries)
	query := endpoint.Query()
	query.Set("format", "csvdata")
	query.Set("startPeriod", retrievedAt.Add(-time.Duration(source.MaxAgeSeconds)*time.Second).Format(time.DateOnly))
	query.Set("endPeriod", retrievedAt.Format(time.DateOnly))
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Observation{}, providerError{kind: ErrInvalidSource, err: err}
	}
	req.Header.Set("Accept", "text/csv, application/vnd.ecb.data+csv;version=1.0.0")
	response, err := executeProviderRequest(p.client, req, &endpoint)
	if err != nil {
		return Observation{}, err
	}
	body, err := readProviderResponse(response, "text/csv", "application/vnd.ecb.data+csv")
	if err != nil {
		return Observation{}, err
	}
	return parseECBResponse(body, source, pair, retrievedAt)
}

func parseECBResponse(body []byte, source walletstore.FXSource, pair walletstore.FXSourcePair, retrievedAt time.Time) (Observation, error) {
	reader := csv.NewReader(bytes.NewReader(body))
	records, err := reader.ReadAll()
	if err != nil || len(records) < 2 {
		return Observation{}, providerError{kind: ErrInvalidResponse, err: err}
	}
	header := make(map[string]int, len(records[0]))
	for index, name := range records[0] {
		if _, exists := header[name]; exists {
			return Observation{}, providerError{kind: ErrInvalidResponse, err: fmt.Errorf("duplicate column %s", name)}
		}
		header[name] = index
	}
	required := []string{"KEY", "CURRENCY", "CURRENCY_DENOM", "TIME_PERIOD", "OBS_VALUE", "OBS_STATUS"}
	for _, name := range required {
		if _, exists := header[name]; !exists {
			return Observation{}, providerError{kind: ErrInvalidResponse, err: fmt.Errorf("missing column %s", name)}
		}
	}

	var selectedDate time.Time
	var selectedRate decimal.Decimal
	selectedStatus := ""
	rowsByDate := make(map[time.Time]int)
	for _, record := range records[1:] {
		if len(record) != len(records[0]) {
			return Observation{}, providerError{kind: ErrInvalidResponse, err: errorsNew("inconsistent CSV record length")}
		}
		if record[header["KEY"]] != "EXR."+pair.ExternalSeries || record[header["CURRENCY"]] != pair.QuoteCurrencyCode || record[header["CURRENCY_DENOM"]] != pair.BaseCurrencyCode {
			return Observation{}, providerError{kind: ErrInvalidResponse, err: errorsNew("series orientation mismatch")}
		}
		observationDate, err := time.Parse(time.DateOnly, record[header["TIME_PERIOD"]])
		if err != nil {
			return Observation{}, providerError{kind: ErrInvalidResponse, err: err}
		}
		observationDate = observationDate.UTC()
		if observationDate.After(retrievedAt) {
			return Observation{}, providerError{kind: ErrInvalidResponse, err: errorsNew("future-dated observation")}
		}
		rowsByDate[observationDate]++
		rate, err := decimal.NewFromString(record[header["OBS_VALUE"]])
		if err != nil || rate.Cmp(decimal.Zero) <= 0 {
			return Observation{}, providerError{kind: ErrInvalidResponse, err: errorsNew("invalid observation rate")}
		}
		if selectedDate.IsZero() || observationDate.After(selectedDate) {
			selectedDate = observationDate
			selectedRate = rate
			selectedStatus = record[header["OBS_STATUS"]]
		}
	}
	if selectedDate.IsZero() {
		return Observation{}, ErrObservationNotFound
	}
	if rowsByDate[selectedDate] != 1 {
		return Observation{}, providerError{kind: ErrInvalidResponse, err: errorsNew("duplicate selected observation")}
	}
	if selectedStatus != ecbNormalObservationStatus {
		return Observation{}, providerError{kind: ErrInvalidResponse, err: fmt.Errorf("unexpected observation status %q", selectedStatus)}
	}
	hash := sha256.Sum256(body)
	return Observation{
		Pair:             pair,
		Rate:             selectedRate,
		Side:             walletstore.FXSideMid,
		Purpose:          source.Purpose,
		ObservationAt:    selectedDate,
		RetrievedAt:      retrievedAt,
		ExpiresAt:        selectedDate.Add(time.Duration(source.MaxAgeSeconds) * time.Second),
		RawPayloadSHA256: hex.EncodeToString(hash[:]),
		SourceRevision:   pair.ExternalSeries + ":" + selectedDate.Format(time.DateOnly) + ":" + selectedStatus,
	}, nil
}
