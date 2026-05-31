package httpjson

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adonese/noebs/internal/httpclient"
	"github.com/adonese/noebs/wallet/psp"
)

type Provider struct {
	config *psp.Config
	client *http.Client
}

func NewProvider(cfg *psp.Config) (*Provider, error) {
	if cfg == nil || cfg.APIBaseURL == "" || cfg.ProviderCode == "" {
		return nil, psp.ErrPSPConfigInvalid
	}
	if err := validateRequestRoutes(cfg); err != nil {
		return nil, err
	}
	return &Provider{
		config: cfg,
		client: httpclient.New(httpclient.WithTimeout(15 * time.Second)),
	}, nil
}

func (p *Provider) Code() string {
	if p == nil || p.config == nil {
		return ""
	}
	return p.config.ProviderCode
}

func (p *Provider) SupportedOperations() []psp.Operation {
	return []psp.Operation{psp.OperationDeposit, psp.OperationWithdrawal}
}

func (p *Provider) VerifyDeposit(ctx context.Context, txID string) (*psp.DepositVerification, error) {
	if p == nil || p.config == nil {
		return nil, psp.ErrPSPConfigInvalid
	}
	canonical := map[string]any{"transaction_id": txID}
	payload, err := psp.MapRequest(canonical, p.config.DepositRequestMapping)
	if err != nil {
		return nil, err
	}
	resp := map[string]any{}
	method := normalizeMethod(p.config.DepositRequestMethod)
	path := renderRequestPath(strings.TrimSpace(p.config.DepositRequestPath), canonical)
	path, err = appendQueryForMethod(method, path, payload)
	if err != nil {
		return nil, err
	}
	if err := p.doJSON(ctx, method, path, payloadForMethod(method, payload), "", &resp); err != nil {
		return nil, err
	}
	mapped, err := psp.MapResponse(resp, p.config.DepositResponseMapping)
	if err != nil {
		return nil, err
	}
	return &psp.DepositVerification{
		ProviderTxID: mapped.TransactionID,
		Amount:       mapped.Amount,
		Currency:     mapped.Currency,
		Status:       mapped.Status,
		Metadata:     mapped.Metadata,
	}, nil
}

func (p *Provider) SendPayout(ctx context.Context, req psp.PayoutRequest) (*psp.PayoutResult, error) {
	if p == nil || p.config == nil {
		return nil, psp.ErrPSPConfigInvalid
	}
	canonical := map[string]any{
		"client_reference": req.ClientReference,
		"amount":           req.Amount,
		"currency":         req.Currency,
		"destination":      req.Destination,
		"metadata":         req.Metadata,
	}
	payload, err := psp.MapRequest(canonical, p.config.PayoutRequestMapping)
	if err != nil {
		return nil, err
	}
	resp := map[string]any{}
	idempotencyKey := req.ClientReference
	method := normalizeMethod(p.config.PayoutRequestMethod)
	path := renderRequestPath(strings.TrimSpace(p.config.PayoutRequestPath), canonical)
	path, err = appendQueryForMethod(method, path, payload)
	if err != nil {
		return nil, err
	}
	if err := p.doJSON(ctx, method, path, payloadForMethod(method, payload), idempotencyKey, &resp); err != nil {
		return nil, err
	}
	mapped, err := psp.MapResponse(resp, p.config.PayoutResponseMapping)
	if err != nil {
		return nil, err
	}
	return &psp.PayoutResult{
		ProviderTxID: mapped.TransactionID,
		Status:       mapped.Status,
		RawResponse:  resp,
	}, nil
}

func (p *Provider) GetTransactionStatus(ctx context.Context, txID string) (*psp.TxStatus, error) {
	if p == nil || p.config == nil {
		return nil, psp.ErrPSPConfigInvalid
	}
	resp := map[string]any{}
	canonical := map[string]any{"transaction_id": txID}
	payload, err := psp.MapRequest(canonical, p.config.StatusRequestMapping)
	if err != nil {
		return nil, err
	}
	method := normalizeMethod(p.config.StatusRequestMethod)
	path := renderRequestPath(strings.TrimSpace(p.config.StatusRequestPath), canonical)
	path, err = appendQueryForMethod(method, path, payload)
	if err != nil {
		return nil, err
	}
	if err := p.doJSON(ctx, method, path, payloadForMethod(method, payload), "", &resp); err != nil {
		return nil, err
	}
	mapped, err := psp.MapResponse(resp, p.config.StatusResponseMapping)
	if err != nil {
		return nil, err
	}
	return &psp.TxStatus{
		ProviderTxID: mapped.TransactionID,
		Amount:       mapped.Amount,
		Currency:     mapped.Currency,
		Status:       mapped.Status,
		RawResponse:  resp,
	}, nil
}

func validateRequestRoutes(cfg *psp.Config) error {
	if strings.TrimSpace(cfg.DepositRequestMethod) == "" || strings.TrimSpace(cfg.DepositRequestPath) == "" {
		return psp.ErrPSPConfigInvalid
	}
	if strings.TrimSpace(cfg.PayoutRequestMethod) == "" || strings.TrimSpace(cfg.PayoutRequestPath) == "" {
		return psp.ErrPSPConfigInvalid
	}
	if strings.TrimSpace(cfg.StatusRequestMethod) == "" || strings.TrimSpace(cfg.StatusRequestPath) == "" {
		return psp.ErrPSPConfigInvalid
	}
	return nil
}

func normalizeMethod(method string) string {
	return strings.ToUpper(strings.TrimSpace(method))
}

func payloadForMethod(method string, payload map[string]any) any {
	if len(payload) == 0 {
		return nil
	}
	switch normalizeMethod(method) {
	case http.MethodGet, http.MethodHead:
		return nil
	default:
		return payload
	}
}

func appendQueryForMethod(method, path string, payload map[string]any) (string, error) {
	if len(payload) == 0 {
		return path, nil
	}
	switch normalizeMethod(method) {
	case http.MethodGet, http.MethodHead:
	default:
		return path, nil
	}
	parts := strings.SplitN(path, "?", 2)
	values := url.Values{}
	if len(parts) == 2 {
		parsed, err := url.ParseQuery(parts[1])
		if err != nil {
			return "", fmt.Errorf("%w: invalid request query", psp.ErrPSPConfigInvalid)
		}
		values = parsed
	}
	addQueryValues(values, "", payload)
	if encoded := values.Encode(); encoded != "" {
		return parts[0] + "?" + encoded, nil
	}
	return parts[0], nil
}

func addQueryValues(values url.Values, prefix string, payload map[string]any) {
	for key, value := range payload {
		queryKey := key
		if prefix != "" {
			queryKey = prefix + "." + key
		}
		if nested, ok := value.(map[string]any); ok {
			addQueryValues(values, queryKey, nested)
			continue
		}
		values.Set(queryKey, stringValue(value))
	}
}

func renderRequestPath(path string, values map[string]any) string {
	for key, value := range values {
		replacement := url.PathEscape(stringValue(value))
		path = strings.ReplaceAll(path, "{"+key+"}", replacement)
	}
	return path
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func (p *Provider) VerifyWebhook(payload []byte, signature string) bool {
	if p == nil || p.config == nil {
		return false
	}
	secret := strings.TrimSpace(p.config.WebhookSecret)
	if secret == "" || len(payload) == 0 || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	signature = strings.TrimSpace(signature)
	decoded, err := hex.DecodeString(signature)
	if err == nil {
		return hmac.Equal(expected, decoded)
	}
	if decoded, err = base64.StdEncoding.DecodeString(signature); err == nil {
		return hmac.Equal(expected, decoded)
	}
	return hmac.Equal(expected, []byte(signature))
}

func (p *Provider) doJSON(ctx context.Context, method, path string, payload any, idempotencyKey string, out *map[string]any) error {
	url := strings.TrimRight(p.config.APIBaseURL, "/") + path
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return psp.ErrPSPPermanent
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return psp.ErrPSPTemporary
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
		req.Header.Set("X-API-Key", p.config.APIKey)
	}
	if p.config.APISecret != "" {
		req.Header.Set("X-API-Secret", p.config.APISecret)
	}
	if idempotencyKey != "" && p.config.IdempotencyHeaderName != "" {
		req.Header.Set(p.config.IdempotencyHeaderName, idempotencyKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return psp.ErrPSPTemporary
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read response body: %v", psp.ErrPSPTemporary, err)
	}
	if resp.StatusCode >= 500 {
		return psp.ErrPSPTemporary
	}
	if resp.StatusCode >= 400 {
		return psp.ErrPSPPermanent
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("%w: decode response body: %v", psp.ErrPSPResponseInvalid, err)
		}
	}
	return nil
}
