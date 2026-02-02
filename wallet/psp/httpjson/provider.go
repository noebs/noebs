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
	"strings"
	"time"

	"github.com/adonese/noebs/wallet/psp"
)

const (
	verifyDepositPath = "/deposits/verify"
	payoutPath        = "/payouts"
	statusPath        = "/transactions/"
)

type Provider struct {
	config *psp.Config
	client *http.Client
}

func NewProvider(cfg *psp.Config) (*Provider, error) {
	if cfg == nil || cfg.APIBaseURL == "" || cfg.ProviderCode == "" {
		return nil, psp.ErrPSPConfigInvalid
	}
	return &Provider{
		config: cfg,
		client: &http.Client{Timeout: 15 * time.Second},
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
	payload := map[string]any{"transaction_id": txID}
	resp := map[string]any{}
	if err := p.doJSON(ctx, http.MethodPost, verifyDepositPath, payload, "", &resp); err != nil {
		return nil, err
	}
	return &psp.DepositVerification{
		ProviderTxID: stringFromMap(resp, "transaction_id", "psp_transaction_id", "id"),
		Amount:       int64FromMap(resp, "amount"),
		Currency:     stringFromMap(resp, "currency"),
		Status:       strings.ToLower(stringFromMap(resp, "status")),
		Metadata:     mapFrom(resp, "metadata", "meta"),
	}, nil
}

func (p *Provider) SendPayout(ctx context.Context, req psp.PayoutRequest) (*psp.PayoutResult, error) {
	if p == nil || p.config == nil {
		return nil, psp.ErrPSPConfigInvalid
	}
	payload := map[string]any{
		"client_reference": req.ClientReference,
		"amount":           req.Amount,
		"currency":         req.Currency,
		"destination":      req.Destination,
		"metadata":         req.Metadata,
	}
	resp := map[string]any{}
	idempotencyKey := req.ClientReference
	if err := p.doJSON(ctx, http.MethodPost, payoutPath, payload, idempotencyKey, &resp); err != nil {
		return nil, err
	}
	return &psp.PayoutResult{
		ProviderTxID: stringFromMap(resp, "transaction_id", "psp_transaction_id", "id"),
		Status:       strings.ToLower(stringFromMap(resp, "status")),
		RawResponse:  resp,
	}, nil
}

func (p *Provider) GetTransactionStatus(ctx context.Context, txID string) (*psp.TxStatus, error) {
	if p == nil || p.config == nil {
		return nil, psp.ErrPSPConfigInvalid
	}
	resp := map[string]any{}
	path := statusPath + txID
	if err := p.doJSON(ctx, http.MethodGet, path, nil, "", &resp); err != nil {
		return nil, err
	}
	return &psp.TxStatus{
		ProviderTxID: stringFromMap(resp, "transaction_id", "psp_transaction_id", "id"),
		Status:       strings.ToLower(stringFromMap(resp, "status")),
		RawResponse:  resp,
	}, nil
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
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 500 {
		return psp.ErrPSPTemporary
	}
	if resp.StatusCode >= 400 {
		return psp.ErrPSPPermanent
	}
	if out != nil && len(respBody) > 0 {
		_ = json.Unmarshal(respBody, out)
	}
	return nil
}

func stringFromMap(m map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed != "" {
				return typed
			}
		case json.Number:
			return typed.String()
		case float64:
			return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.0f", typed), "0"), ".")
		}
	}
	return ""
}

func int64FromMap(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int64:
			return typed
		case int:
			return int64(typed)
		case float64:
			return int64(typed)
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func mapFrom(m map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			if cast, ok := value.(map[string]any); ok {
				return cast
			}
		}
	}
	return nil
}
