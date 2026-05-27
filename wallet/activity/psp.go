package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
)

var ErrMissingPSPDependencies = errors.New("missing psp dependencies")

type PSPActivities struct {
	Loader   *psp.Loader
	Registry *psp.Registry
	Store    *walletstore.Store
}

type VerifyDepositParams struct {
	TenantID      string
	ProviderCode  string
	TransactionID string
	Currency      string
	Region        string
}

type SendPayoutParams struct {
	TenantID     string
	ProviderCode string
	Request      psp.PayoutRequest
	Region       string
}

type GetStatusParams struct {
	TenantID      string
	ProviderCode  string
	TransactionID string
	Currency      string
	Direction     string
	Region        string
}

func NewPSPActivities(loader *psp.Loader, registry *psp.Registry) *PSPActivities {
	activities := &PSPActivities{Loader: loader, Registry: registry}
	if loader != nil {
		activities.Store = loader.Store
	}
	return activities
}

func (a *PSPActivities) VerifyDeposit(ctx context.Context, params VerifyDepositParams) (*psp.DepositVerification, error) {
	provider, cfg, err := a.resolveProvider(ctx, params.TenantID, params.ProviderCode, params.Currency, "deposit", params.Region)
	if err != nil {
		return nil, err
	}
	requestHeaders, err := pspRequestHeaders(cfg, "")
	if err != nil {
		return nil, err
	}
	requestBody, err := pspRawJSON(map[string]any{"transaction_id": params.TransactionID})
	if err != nil {
		return nil, err
	}
	result, callErr := provider.VerifyDeposit(ctx, params.TransactionID)
	responseBody, err := pspRawJSON(result)
	if err != nil {
		return nil, err
	}
	auditErr := a.recordInteraction(ctx, walletstore.PSPInteraction{
		TenantID:         params.TenantID,
		PSPProvider:      cfg.ProviderCode,
		PSPTransactionID: sql.NullString{String: params.TransactionID, Valid: params.TransactionID != ""},
		Direction:        sql.NullString{String: "inbound", Valid: true},
		InteractionType:  "deposit_verify",
		Method:           sql.NullString{String: "POST", Valid: true},
		URL:              sql.NullString{String: cfg.APIBaseURL, Valid: cfg.APIBaseURL != ""},
		RequestHeaders:   requestHeaders,
		RequestBody:      requestBody,
		ResponseBody:     responseBody,
		ErrorMessage:     errorMessage(callErr),
	})
	if auditErr != nil {
		return nil, auditErr
	}
	return result, callErr
}

func (a *PSPActivities) SendPayout(ctx context.Context, params SendPayoutParams) (*psp.PayoutResult, error) {
	provider, cfg, err := a.resolveProvider(ctx, params.TenantID, params.ProviderCode, params.Request.Currency, "withdrawal", params.Region)
	if err != nil {
		return nil, err
	}
	requestHeaders, err := pspRequestHeaders(cfg, params.Request.ClientReference)
	if err != nil {
		return nil, err
	}
	requestBody, err := pspRawJSON(params.Request)
	if err != nil {
		return nil, err
	}
	result, callErr := provider.SendPayout(ctx, params.Request)
	pspTransactionID := ""
	if result != nil {
		pspTransactionID = result.ProviderTxID
	}
	responseBody, err := pspRawJSON(result)
	if err != nil {
		return nil, err
	}
	auditErr := a.recordInteraction(ctx, walletstore.PSPInteraction{
		TenantID:         params.TenantID,
		PSPProvider:      cfg.ProviderCode,
		PSPTransactionID: sql.NullString{String: pspTransactionID, Valid: pspTransactionID != ""},
		ClientReference:  sql.NullString{String: params.Request.ClientReference, Valid: params.Request.ClientReference != ""},
		Direction:        sql.NullString{String: "outbound", Valid: true},
		InteractionType:  "payout_send",
		Method:           sql.NullString{String: "POST", Valid: true},
		URL:              sql.NullString{String: cfg.APIBaseURL, Valid: cfg.APIBaseURL != ""},
		RequestHeaders:   requestHeaders,
		RequestBody:      requestBody,
		ResponseBody:     responseBody,
		ErrorMessage:     errorMessage(callErr),
	})
	if auditErr != nil {
		return nil, auditErr
	}
	return result, callErr
}

func (a *PSPActivities) GetTransactionStatus(ctx context.Context, params GetStatusParams) (*psp.TxStatus, error) {
	direction := normalizeScopeDirection(params.Direction)
	provider, cfg, err := a.resolveProvider(ctx, params.TenantID, params.ProviderCode, params.Currency, direction, params.Region)
	if err != nil {
		return nil, err
	}
	requestHeaders, err := pspRequestHeaders(cfg, "")
	if err != nil {
		return nil, err
	}
	requestBody, err := pspRawJSON(map[string]any{"transaction_id": params.TransactionID})
	if err != nil {
		return nil, err
	}
	result, callErr := provider.GetTransactionStatus(ctx, params.TransactionID)
	pspTransactionID := params.TransactionID
	if result != nil && result.ProviderTxID != "" {
		pspTransactionID = result.ProviderTxID
	}
	responseBody, err := pspRawJSON(result)
	if err != nil {
		return nil, err
	}
	auditErr := a.recordInteraction(ctx, walletstore.PSPInteraction{
		TenantID:         params.TenantID,
		PSPProvider:      cfg.ProviderCode,
		PSPTransactionID: sql.NullString{String: pspTransactionID, Valid: pspTransactionID != ""},
		Direction:        storeDirection(direction),
		InteractionType:  "status_check",
		Method:           sql.NullString{String: "GET", Valid: true},
		URL:              sql.NullString{String: cfg.APIBaseURL, Valid: cfg.APIBaseURL != ""},
		RequestHeaders:   requestHeaders,
		RequestBody:      requestBody,
		ResponseBody:     responseBody,
		ErrorMessage:     errorMessage(callErr),
	})
	if auditErr != nil {
		return nil, auditErr
	}
	return result, callErr
}

func normalizeScopeDirection(direction string) string {
	normalized := strings.ToLower(strings.TrimSpace(direction))
	switch normalized {
	case "", "deposit", "inbound":
		return "deposit"
	case "withdrawal", "outbound", "payout":
		return "withdrawal"
	default:
		return normalized
	}
}

func (a *PSPActivities) resolveProvider(ctx context.Context, tenantID, providerCode, currency, direction, region string) (psp.Provider, *psp.Config, error) {
	if a == nil || a.Loader == nil || a.Registry == nil {
		return nil, nil, ErrMissingPSPDependencies
	}
	cfg, err := a.Loader.LoadForScope(ctx, tenantID, providerCode, psp.Scope{
		Region:    region,
		Currency:  currency,
		Direction: direction,
	})
	if err != nil {
		return nil, nil, err
	}
	provider, err := a.Registry.Resolve(cfg)
	if err != nil {
		return nil, nil, err
	}
	return provider, cfg, nil
}

func (a *PSPActivities) recordInteraction(ctx context.Context, interaction walletstore.PSPInteraction) error {
	if a == nil || a.Store == nil {
		return ErrMissingStore
	}
	_, err := a.Store.RecordPSPInteraction(ctx, interaction)
	return err
}

func pspRequestHeaders(cfg *psp.Config, idempotencyKey string) (walletstore.RawJSON, error) {
	if cfg == nil {
		return nil, nil
	}
	headers := map[string]string{}
	if cfg.APIKey != "" {
		headers["Authorization"] = "REDACTED"
		headers["X-API-Key"] = "REDACTED"
	}
	if cfg.APISecret != "" {
		headers["X-API-Secret"] = "REDACTED"
	}
	if cfg.IdempotencyHeaderName != "" && idempotencyKey != "" {
		headers[cfg.IdempotencyHeaderName] = idempotencyKey
	}
	return pspRawJSON(headers)
}

func pspRawJSON(value any) (walletstore.RawJSON, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return walletstore.RawJSON(data), nil
}

func errorMessage(err error) sql.NullString {
	if err == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: err.Error(), Valid: true}
}

func storeDirection(direction string) sql.NullString {
	switch normalizeScopeDirection(direction) {
	case "withdrawal":
		return sql.NullString{String: "outbound", Valid: true}
	case "deposit":
		return sql.NullString{String: "inbound", Valid: true}
	}
	return sql.NullString{}
}
