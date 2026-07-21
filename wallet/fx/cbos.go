package fx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
	"golang.org/x/net/html"
)

const cbosHost = "cbos.gov.sd"

type CBOSProvider struct {
	client *http.Client
}

func NewCBOSProvider(client *http.Client) *CBOSProvider {
	return &CBOSProvider{client: client}
}

func (p *CBOSProvider) Fetch(ctx context.Context, source walletstore.FXSource, pairs []walletstore.FXSourcePair, retrievedAt time.Time) ([]Observation, error) {
	if p == nil || p.client == nil {
		return nil, ErrMissingProvider
	}
	if err := validateFetchInput(source, pairs, retrievedAt); err != nil {
		return nil, err
	}
	endpoint, err := validateProviderURL(source.SourceURL, cbosHost, "/en/exchange-rates")
	if err != nil {
		return nil, err
	}
	if source.Provider != ProviderCBOSHTML || source.Purpose != walletstore.FXPurposeReference {
		return nil, ErrInvalidSource
	}
	for _, pair := range pairs {
		if pair.QuoteCurrencyCode != "SDG" {
			return nil, ErrInvalidPair
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, providerError{kind: ErrInvalidSource, err: err}
	}
	req.Header.Set("Accept", "text/html")
	response, err := executeProviderRequest(p.client, req, endpoint)
	if err != nil {
		return nil, err
	}
	body, err := readProviderResponse(response, "text/html", "application/xhtml+xml")
	if err != nil {
		return nil, err
	}
	return parseCBOSResponse(body, source, pairs, retrievedAt.UTC())
}

type cbosRates struct {
	buy  decimal.Decimal
	sell decimal.Decimal
	mid  decimal.Decimal
}

func parseCBOSResponse(body []byte, source walletstore.FXSource, pairs []walletstore.FXSourcePair, retrievedAt time.Time) ([]Observation, error) {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, providerError{kind: ErrInvalidResponse, err: err}
	}
	dates := findNodes(document, func(node *html.Node) bool {
		return node.Type == html.ElementNode && hasClass(node, "date-display-single")
	})
	if len(dates) != 1 {
		return nil, providerError{kind: ErrInvalidResponse, err: fmt.Errorf("exchange date count %d", len(dates))}
	}
	observationAt, err := time.Parse("02/01/2006", nodeText(dates[0]))
	if err != nil {
		return nil, providerError{kind: ErrInvalidResponse, err: err}
	}
	observationAt = observationAt.UTC()
	if observationAt.After(retrievedAt) {
		return nil, providerError{kind: ErrInvalidResponse, err: errorsNew("future exchange date")}
	}
	tables := findNodes(document, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "table" && hasClass(node, "field-collection-table-view")
	})
	if len(tables) != 1 {
		return nil, providerError{kind: ErrInvalidResponse, err: fmt.Errorf("exchange table count %d", len(tables))}
	}
	headers := directDescendantTexts(tables[0], "th")
	expectedHeaders := []string{"The Currency Arabic", "The Currency", "Buying", "Selling", "Middle"}
	if len(headers) != len(expectedHeaders) {
		return nil, providerError{kind: ErrInvalidResponse, err: errorsNew("unexpected exchange table header count")}
	}
	for index := range headers {
		if headers[index] != expectedHeaders[index] {
			return nil, providerError{kind: ErrInvalidResponse, err: errorsNew("unexpected exchange table headers")}
		}
	}

	ratesByName := make(map[string]cbosRates)
	rows := findNodes(tables[0], func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "tr" && hasClass(node, "field-collection-item")
	})
	for _, row := range rows {
		cells := directChildTexts(row, "td")
		if len(cells) != 5 || cells[1] == "" {
			return nil, providerError{kind: ErrInvalidResponse, err: errorsNew("malformed exchange row")}
		}
		if _, duplicate := ratesByName[cells[1]]; duplicate {
			return nil, providerError{kind: ErrInvalidResponse, err: errorsNew("duplicate exchange currency")}
		}
		buy, buyErr := decimal.NewFromString(cells[2])
		sell, sellErr := decimal.NewFromString(cells[3])
		mid, midErr := decimal.NewFromString(cells[4])
		if buyErr != nil || sellErr != nil || midErr != nil || buy.Cmp(decimal.Zero) <= 0 || sell.Cmp(decimal.Zero) <= 0 || mid.Cmp(decimal.Zero) <= 0 || buy.Cmp(mid) > 0 || mid.Cmp(sell) > 0 {
			return nil, providerError{kind: ErrInvalidResponse, err: errorsNew("invalid exchange row rates")}
		}
		ratesByName[cells[1]] = cbosRates{buy: buy, sell: sell, mid: mid}
	}

	hash := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(hash[:])
	observations := make([]Observation, 0, len(pairs)*3)
	for _, pair := range pairs {
		rates, ok := ratesByName[pair.ExternalSeries]
		if !ok {
			return nil, fmt.Errorf("%w: CBOS series %s", ErrObservationNotFound, pair.ExternalSeries)
		}
		for _, value := range []struct {
			side string
			rate decimal.Decimal
		}{{walletstore.FXSideBid, rates.buy}, {walletstore.FXSideAsk, rates.sell}, {walletstore.FXSideMid, rates.mid}} {
			observations = append(observations, Observation{
				Pair:             pair,
				Rate:             value.rate,
				Side:             value.side,
				Purpose:          source.Purpose,
				ObservationAt:    observationAt,
				RetrievedAt:      retrievedAt,
				ExpiresAt:        observationAt.Add(time.Duration(source.MaxAgeSeconds) * time.Second),
				RawPayloadSHA256: payloadHash,
				SourceRevision:   "CBOS:" + observationAt.Format(time.DateOnly),
			})
		}
	}
	sortObservations(observations)
	return observations, nil
}

func findNodes(root *html.Node, predicate func(*html.Node) bool) []*html.Node {
	var result []*html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if predicate(node) {
			result = append(result, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return result
}

func hasClass(node *html.Node, wanted string) bool {
	for _, attribute := range node.Attr {
		if attribute.Key != "class" {
			continue
		}
		for _, className := range strings.Fields(attribute.Val) {
			if className == wanted {
				return true
			}
		}
	}
	return false
}

func nodeText(node *html.Node) string {
	var text strings.Builder
	var visit func(*html.Node)
	visit = func(current *html.Node) {
		if current.Type == html.TextNode {
			text.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return strings.Join(strings.Fields(text.String()), " ")
}

func directChildTexts(node *html.Node, element string) []string {
	var result []string
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == element {
			result = append(result, nodeText(child))
		}
	}
	return result
}

func directDescendantTexts(node *html.Node, element string) []string {
	nodes := findNodes(node, func(candidate *html.Node) bool {
		return candidate.Type == html.ElementNode && candidate.Data == element
	})
	result := make([]string, len(nodes))
	for index, candidate := range nodes {
		result[index] = nodeText(candidate)
	}
	return result
}
