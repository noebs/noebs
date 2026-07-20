package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	"go.temporal.io/sdk/temporal"
)

var ErrMissingPSPDependencies = errors.New("missing psp dependencies")

const (
	PSPDispatchRejectedErrorType     = "psp_dispatch_rejected"
	PSPDispatchNotAttemptedErrorType = "psp_dispatch_not_attempted"
)

type PSPActivities struct {
	Loader   *psp.Loader
	Registry *psp.Registry
	Store    *walletstore.Store
}

type CreateDepositParams struct {
	TenantID     string
	ProviderCode string
	Request      psp.DepositRequest
	Region       string
}

type SendPayoutParams struct {
	TenantID     string
	ProviderCode string
	Request      psp.PayoutRequest
	Region       string
}

type GetStatusParams struct {
	TenantID        string
	ProviderCode    string
	TransactionID   string
	IdempotencyKey  string
	ClientReference string
	Amount          int64
	Currency        string
	Direction       string
	Region          string
}

func NewPSPActivities(loader *psp.Loader, registry *psp.Registry) *PSPActivities {
	activities := &PSPActivities{Loader: loader, Registry: registry}
	if loader != nil {
		activities.Store = loader.Store
	}
	return activities
}

func (a *PSPActivities) CreateDeposit(ctx context.Context, params CreateDepositParams) (*psp.DepositResult, error) {
	if err := validateDispatchIdempotencyKey(params.Request.IdempotencyKey); err != nil {
		return nil, classifyPSPDispatchError(err)
	}
	provider, cfg, err := a.resolveProvider(ctx, params.TenantID, params.ProviderCode, params.Request.Currency, "deposit", params.Region)
	if err != nil {
		return nil, classifyPSPDispatchPreparationError(err)
	}
	requestHeaders, err := pspRequestHeaders(cfg, params.Request.IdempotencyKey)
	if err != nil {
		return nil, classifyPSPDispatchPreparationError(err)
	}
	requestBody, err := pspRawJSON(params.Request)
	if err != nil {
		return nil, classifyPSPDispatchPreparationError(err)
	}
	result, callErr := provider.CreateDeposit(ctx, params.Request)
	if callErr == nil {
		callErr = validateDepositResult(params.Request, result)
	}
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
		Direction:        sql.NullString{String: "inbound", Valid: true},
		InteractionType:  "deposit_create",
		IdempotencyKey:   sql.NullString{String: params.Request.IdempotencyKey, Valid: true},
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
	return result, classifyPSPDispatchError(callErr)
}

func (a *PSPActivities) SendPayout(ctx context.Context, params SendPayoutParams) (*psp.PayoutResult, error) {
	if err := validateDispatchIdempotencyKey(params.Request.IdempotencyKey); err != nil {
		return nil, classifyPSPDispatchError(err)
	}
	provider, cfg, err := a.resolveProvider(ctx, params.TenantID, params.ProviderCode, params.Request.Currency, "withdrawal", params.Region)
	if err != nil {
		return nil, classifyPSPDispatchPreparationError(err)
	}
	requestHeaders, err := pspRequestHeaders(cfg, params.Request.IdempotencyKey)
	if err != nil {
		return nil, classifyPSPDispatchPreparationError(err)
	}
	requestBody, err := pspRawJSON(params.Request)
	if err != nil {
		return nil, classifyPSPDispatchPreparationError(err)
	}
	result, callErr := provider.SendPayout(ctx, params.Request)
	if callErr == nil {
		callErr = validatePayoutResult(params.Request, result)
	}
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
		IdempotencyKey:   sql.NullString{String: params.Request.IdempotencyKey, Valid: true},
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
	return result, classifyPSPDispatchError(callErr)
}

func validateDispatchIdempotencyKey(key string) error {
	if key == "" || strings.TrimSpace(key) != key {
		return psp.ErrPSPRequestInvalid
	}
	return nil
}

func validateDepositResult(request psp.DepositRequest, result *psp.DepositResult) error {
	if result == nil || result.ClientReference != request.ClientReference ||
		strings.TrimSpace(result.ProviderTxID) == "" || result.Amount != request.Amount || result.Currency != request.Currency {
		return psp.ErrPSPResponseInvalid
	}
	return validateDispatchStatus(result.Status)
}

func validatePayoutResult(request psp.PayoutRequest, result *psp.PayoutResult) error {
	if result == nil || strings.TrimSpace(result.ProviderTxID) == "" ||
		result.Amount != request.Amount || result.Currency != request.Currency {
		return psp.ErrPSPResponseInvalid
	}
	return validateDispatchStatus(result.Status)
}

func validateDispatchStatus(status string) error {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case walletstore.PSPStatusProcessing, walletstore.PSPStatusPending, walletstore.PSPStatusHeld,
		walletstore.PSPStatusFailed, walletstore.PSPStatusCancelled, walletstore.PSPStatusSuccess:
		return nil
	default:
		return psp.ErrPSPResponseInvalid
	}
}

func classifyPSPDispatchError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, psp.ErrPSPPermanent) ||
		errors.Is(err, psp.ErrPSPRequestInvalid) ||
		errors.Is(err, psp.ErrPSPConfigInvalid) {
		return temporal.NewNonRetryableApplicationError(err.Error(), PSPDispatchRejectedErrorType, err)
	}
	return err
}

func classifyPSPDispatchPreparationError(err error) error {
	if err == nil {
		return nil
	}
	classified := classifyPSPDispatchError(err)
	var applicationError *temporal.ApplicationError
	if errors.As(classified, &applicationError) {
		return classified
	}
	return temporal.NewApplicationErrorWithCause(err.Error(), PSPDispatchNotAttemptedErrorType, err)
}

func (a *PSPActivities) GetTransactionStatus(ctx context.Context, params GetStatusParams) (*psp.TxStatus, error) {
	if err := validateDispatchIdempotencyKey(params.IdempotencyKey); err != nil {
		return nil, err
	}
	if params.ClientReference == "" || strings.TrimSpace(params.ClientReference) != params.ClientReference {
		return nil, psp.ErrPSPRequestInvalid
	}
	direction := normalizeScopeDirection(params.Direction)
	provider, cfg, err := a.resolveProvider(ctx, params.TenantID, params.ProviderCode, params.Currency, direction, params.Region)
	if err != nil {
		return nil, err
	}
	requestHeaders, err := pspRequestHeaders(cfg, "")
	if err != nil {
		return nil, err
	}
	lookup := psp.TransactionLookup{
		ProviderTxID: params.TransactionID, IdempotencyKey: params.IdempotencyKey, ClientReference: params.ClientReference,
	}
	requestBody, err := pspRawJSON(lookup)
	if err != nil {
		return nil, err
	}
	result, callErr := provider.GetTransactionStatus(ctx, lookup)
	if callErr == nil {
		callErr = validateTransactionStatusResult(params, result)
	}
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
		ClientReference:  sql.NullString{String: params.ClientReference, Valid: true},
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

func validateTransactionStatusResult(params GetStatusParams, result *psp.TxStatus) error {
	if result == nil || strings.TrimSpace(result.ProviderTxID) == "" ||
		result.Amount != params.Amount || result.Currency != params.Currency {
		return psp.ErrPSPResponseInvalid
	}
	return validateDispatchStatus(result.Status)
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
	if a.Store == nil {
		return nil, nil, ErrMissingStore
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
