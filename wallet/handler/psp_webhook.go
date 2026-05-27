package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/adonese/noebs/apperr"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.temporal.io/api/serviceerror"
)

type TemporalSignaler interface {
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg interface{}) error
}

type PSPWebhookHandler struct {
	Store    *walletstore.Store
	Loader   *walletpsp.Loader
	Registry *walletpsp.Registry
	Temporal TemporalSignaler
}

func NewPSPWebhookHandler(store *walletstore.Store, loader *walletpsp.Loader, registry *walletpsp.Registry, temporal TemporalSignaler) *PSPWebhookHandler {
	return &PSPWebhookHandler{Store: store, Loader: loader, Registry: registry, Temporal: temporal}
}

func (h *PSPWebhookHandler) Handle(c *fiber.Ctx) error {
	if h == nil || h.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	providerCode := strings.TrimSpace(c.Params("provider"))
	if providerCode == "" {
		return jsonResponse(c, 0, apperr.Wrap(walletstore.ErrMissingProviderCode, apperr.ErrBadRequest, "missing provider"))
	}
	payload := c.Body()
	if len(payload) == 0 {
		return jsonResponse(c, 0, apperr.ErrEmptyBody)
	}

	payloadMap := map[string]any{}
	if err := json.Unmarshal(payload, &payloadMap); err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid webhook payload"))
	}

	tenantID := strings.TrimSpace(c.Query("tenant_id"))
	if tenantID == "" {
		tenantID = stringFromMap(payloadMap, "tenant_id", "tenantId")
	}
	tenantID, err := walletstore.ValidateTenantID(tenantID)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}

	clientRef := stringFromMap(payloadMap, "client_reference", "clientReference", "client_ref", "reference")
	status := strings.ToLower(stringFromMap(payloadMap, "status", "state"))
	pspTransactionID := stringFromMap(payloadMap, "psp_transaction_id", "transaction_id", "id")
	currency := stringFromMap(payloadMap, "currency")
	direction := normalizeDirection(stringFromMap(payloadMap, "direction"))
	region := stringFromMap(payloadMap, "region")

	if h.Loader == nil || h.Registry == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	cfg, err := h.Loader.LoadForScope(c.Context(), tenantID, providerCode, walletpsp.Scope{
		Region:    region,
		Currency:  currency,
		Direction: direction,
	})
	if err != nil {
		switch {
		case errors.Is(err, walletpsp.ErrPSPNotRegistered), errors.Is(err, walletpsp.ErrPSPConfigInvalid):
			return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(err, apperr.ErrBadRequest, err.Error()))
		case errors.Is(err, walletpsp.ErrPSPSecretMissing):
			return jsonResponse(c, http.StatusServiceUnavailable, apperr.Wrap(err, apperr.ErrUnavailable, err.Error()))
		default:
			return jsonResponse(c, http.StatusInternalServerError, apperr.Wrap(err, apperr.ErrInternal, err.Error()))
		}
	}
	mappedWebhook := walletpsp.MapResponse(payloadMap, cfg.WebhookResponseMapping)
	if mappedWebhook.ClientReference != "" {
		clientRef = mappedWebhook.ClientReference
	}
	if mappedWebhook.TransactionID != "" {
		pspTransactionID = mappedWebhook.TransactionID
	}
	if mappedWebhook.Status != "" {
		status = mappedWebhook.Status
	}
	if mappedWebhook.Currency != "" {
		currency = mappedWebhook.Currency
	}
	provider, err := h.Registry.Resolve(cfg)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(err, apperr.ErrBadRequest, err.Error()))
	}

	signature := strings.TrimSpace(firstHeader(c, "X-Webhook-Signature", "X-Signature", "Signature"))
	signatureValid := provider.VerifyWebhook(payload, signature)
	if !signatureValid {
		checkedMap, checkedPayload, err := h.authorizeUnsignedWebhook(c, cfg, provider, providerCode, tenantID, clientRef, pspTransactionID, direction, payloadMap, payload)
		if err != nil {
			errMsg := err.Error()
			if errors.Is(err, walletpsp.ErrPSPWebhookInvalid) {
				errMsg = "invalid webhook signature"
			}
			if logErr := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, direction, http.StatusBadRequest, fiber.Map{"error": errMsg}, errMsg, payload); logErr != nil {
				return jsonResponse(c, 0, mapWalletError(logErr))
			}
			return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(walletpsp.ErrPSPWebhookInvalid, apperr.ErrBadRequest, errMsg))
		}
		payloadMap = checkedMap
		payload = checkedPayload
		mappedWebhook = walletpsp.MapResponse(payloadMap, cfg.WebhookResponseMapping)
		if mappedWebhook.ClientReference != "" {
			clientRef = mappedWebhook.ClientReference
		}
		if mappedWebhook.TransactionID != "" {
			pspTransactionID = mappedWebhook.TransactionID
		}
		if mappedWebhook.Status != "" {
			status = mappedWebhook.Status
		}
		if mappedWebhook.Currency != "" {
			currency = mappedWebhook.Currency
		}
		if value := normalizeDirection(stringFromMap(payloadMap, "direction")); value != "" {
			direction = value
		}
	}
	if status == "" {
		status = strings.ToLower(stringFromMap(payloadMap, "status", "state"))
	}

	if clientRef == "" {
		amount := mappedWebhook.Amount
		if amount <= 0 || currency == "" {
			if err := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, direction, http.StatusBadRequest, fiber.Map{"error": walletstore.ErrMissingClientReference.Error()}, walletstore.ErrMissingClientReference.Error(), payload); err != nil {
				return jsonResponse(c, 0, mapWalletError(err))
			}
			return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(walletstore.ErrMissingClientReference, apperr.ErrBadRequest, "missing client reference"))
		}
		clientRef = "psp-webhook-" + uuid.NewString()
		txn := walletstore.PSPTransaction{
			TenantID:        tenantID,
			PSPProvider:     providerCode,
			IdempotencyKey:  clientRef,
			ClientReference: clientRef,
			Direction:       normalizePSPDirection(direction),
			Amount:          amount,
			Currency:        currency,
			Status:          "held",
			RawResponse:     walletstore.RawJSON(payload),
		}
		if pspTransactionID != "" {
			txn.PSPTransactionID = sql.NullString{String: pspTransactionID, Valid: true}
		}
		if _, err := h.Store.CreatePSPTransaction(c.Context(), txn); err != nil {
			return jsonResponse(c, 0, mapWalletError(err))
		}
		if err := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, txn.Direction, http.StatusAccepted, fiber.Map{"status": "held", "client_reference": clientRef}, "", payload); err != nil {
			return jsonResponse(c, 0, mapWalletError(err))
		}
		return jsonResponse(c, http.StatusAccepted, fiber.Map{"status": "held", "client_reference": clientRef})
	}

	stored, err := h.Store.GetPSPTransactionByReference(c.Context(), tenantID, clientRef)
	if err != nil {
		if errors.Is(err, walletstore.ErrPSPTransactionNotFound) {
			amount := mappedWebhook.Amount
			if amount <= 0 || currency == "" {
				if logErr := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, direction, http.StatusBadRequest, fiber.Map{"error": err.Error()}, err.Error(), payload); logErr != nil {
					return jsonResponse(c, 0, mapWalletError(logErr))
				}
				return jsonResponse(c, 0, mapWalletError(err))
			}
			txn := walletstore.PSPTransaction{
				TenantID:        tenantID,
				PSPProvider:     providerCode,
				IdempotencyKey:  clientRef,
				ClientReference: clientRef,
				Direction:       normalizePSPDirection(direction),
				Amount:          amount,
				Currency:        currency,
				Status:          status,
				RawResponse:     walletstore.RawJSON(payload),
			}
			if pspTransactionID != "" {
				txn.PSPTransactionID = sql.NullString{String: pspTransactionID, Valid: true}
			}
			if _, err := h.Store.CreatePSPTransaction(c.Context(), txn); err != nil {
				return jsonResponse(c, 0, mapWalletError(err))
			}
			if err := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, txn.Direction, http.StatusAccepted, fiber.Map{"status": status, "client_reference": clientRef}, "", payload); err != nil {
				return jsonResponse(c, 0, mapWalletError(err))
			}
			return jsonResponse(c, http.StatusAccepted, fiber.Map{"status": status, "client_reference": clientRef})
		}
		return jsonResponse(c, 0, mapWalletError(err))
	}

	update := walletstore.PSPStatusUpdate{Status: stored.Status}
	if status != "" {
		update.Status = status
	}
	if pspTransactionID != "" {
		update.PSPTransactionID = sql.NullString{String: pspTransactionID, Valid: true}
	}
	message := stringFromMap(payloadMap, "message", "error", "reason")
	if message != "" {
		update.ResponseMessage = sql.NullString{String: message, Valid: true}
	}
	update.RawResponse = walletstore.RawJSON(payload)
	if update.Status == "success" {
		update.ConfirmedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	}
	if err := h.Store.UpdatePSPTransactionStatus(c.Context(), tenantID, clientRef, update); err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}

	if h.Temporal != nil && stored.WorkflowID.Valid {
		signalCurrency := currency
		if signalCurrency == "" {
			signalCurrency = stored.Currency
		}
		signal := walletpsp.TxStatus{
			ProviderTxID: pspTransactionID,
			Amount:       mappedWebhook.Amount,
			Currency:     signalCurrency,
			Status:       update.Status,
			RawResponse:  payloadMap,
		}
		if err := h.Temporal.SignalWorkflow(c.Context(), stored.WorkflowID.String, "", walletworkflow.PSPStatusUpdateSignal, signal); err != nil {
			if _, ok := err.(*serviceerror.NotFound); !ok {
				return jsonResponse(c, http.StatusAccepted, fiber.Map{"status": update.Status, "client_reference": clientRef, "signal_error": err.Error()})
			}
		}
	}

	if err := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, stored.Direction, http.StatusOK, fiber.Map{"status": update.Status, "client_reference": clientRef}, "", payload); err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"status": update.Status, "client_reference": clientRef})
}

func (h *PSPWebhookHandler) recordWebhookInteraction(c *fiber.Ctx, tenantID, providerCode, clientReference, pspTransactionID, direction string, statusCode int, responseBody any, errorMessage string, payload []byte) error {
	if h == nil || h.Store == nil {
		return apperr.ErrUnavailable
	}
	requestHeaders, err := rawJSONFromAny(c.GetReqHeaders())
	if err != nil {
		return err
	}
	responseRaw, err := rawJSONFromAny(responseBody)
	if err != nil {
		return err
	}
	url := c.BaseURL() + c.OriginalURL()
	interaction := walletstore.PSPInteraction{
		TenantID:         tenantID,
		PSPProvider:      providerCode,
		PSPTransactionID: sql.NullString{String: pspTransactionID, Valid: pspTransactionID != ""},
		ClientReference:  sql.NullString{String: clientReference, Valid: clientReference != ""},
		Direction:        nullablePSPDirection(direction),
		InteractionType:  "webhook",
		Method:           sql.NullString{String: c.Method(), Valid: c.Method() != ""},
		URL:              sql.NullString{String: url, Valid: url != ""},
		RequestHeaders:   requestHeaders,
		RequestBody:      walletstore.RawJSON(payload),
		ResponseBody:     responseRaw,
		StatusCode:       sql.NullInt64{Int64: int64(statusCode), Valid: statusCode > 0},
		ErrorMessage:     sql.NullString{String: errorMessage, Valid: errorMessage != ""},
	}
	_, err = h.Store.RecordPSPInteraction(c.Context(), interaction)
	return err
}

func (h *PSPWebhookHandler) authorizeUnsignedWebhook(c *fiber.Ctx, cfg *walletpsp.Config, provider walletpsp.Provider, providerCode, tenantID, clientReference, pspTransactionID, direction string, payloadMap map[string]any, payload []byte) (map[string]any, []byte, error) {
	if cfg == nil {
		return nil, nil, walletpsp.ErrPSPWebhookInvalid
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.WebhookAuthMode))
	switch mode {
	case "ip_allowlist", "signature_or_ip_allowlist":
	default:
		return nil, nil, walletpsp.ErrPSPWebhookInvalid
	}
	allowed, err := webhookIPAllowed(c.IP(), cfg.WebhookAllowedCIDRs)
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, walletpsp.ErrPSPWebhookInvalid
	}
	if !cfg.StatusCheckWebhook {
		return payloadMap, payload, nil
	}
	transactionID := pspTransactionID
	if transactionID == "" {
		transactionID = clientReference
	}
	if transactionID == "" {
		transactionID = stringFromMap(payloadMap, "psp_transaction_id", "transaction_id", "id", "client_reference", "clientReference", "client_ref", "reference")
	}
	if transactionID == "" {
		return nil, nil, walletstore.ErrMissingPSPTransactionID
	}
	status, callErr := provider.GetTransactionStatus(c.Context(), transactionID)
	auditErr := h.recordWebhookStatusCheck(c, cfg, tenantID, providerCode, clientReference, transactionID, direction, status, callErr)
	if auditErr != nil {
		return nil, nil, auditErr
	}
	if callErr != nil {
		return nil, nil, callErr
	}
	authoritative := map[string]any{}
	if status != nil && status.RawResponse != nil {
		for key, value := range status.RawResponse {
			authoritative[key] = value
		}
	}
	if status != nil {
		if status.Status != "" {
			authoritative["status"] = status.Status
		}
		if status.ProviderTxID != "" {
			authoritative["transaction_id"] = status.ProviderTxID
			authoritative["psp_transaction_id"] = status.ProviderTxID
		}
		if status.Amount > 0 {
			authoritative["amount"] = status.Amount
		}
		if status.Currency != "" {
			authoritative["currency"] = status.Currency
		}
	}
	raw, err := json.Marshal(authoritative)
	if err != nil {
		return nil, nil, err
	}
	return authoritative, raw, nil
}

func (h *PSPWebhookHandler) recordWebhookStatusCheck(c *fiber.Ctx, cfg *walletpsp.Config, tenantID, providerCode, clientReference, pspTransactionID, direction string, status *walletpsp.TxStatus, callErr error) error {
	if h == nil || h.Store == nil {
		return apperr.ErrUnavailable
	}
	requestHeaders, err := webhookProviderHeaders(cfg)
	if err != nil {
		return err
	}
	responseBody, err := rawJSONFromAny(status)
	if err != nil {
		return err
	}
	requestBody, err := rawJSONFromAny(map[string]any{"transaction_id": pspTransactionID})
	if err != nil {
		return err
	}
	url := ""
	if cfg != nil {
		url = cfg.APIBaseURL
	}
	interaction := walletstore.PSPInteraction{
		TenantID:         tenantID,
		PSPProvider:      providerCode,
		PSPTransactionID: sql.NullString{String: pspTransactionID, Valid: pspTransactionID != ""},
		ClientReference:  sql.NullString{String: clientReference, Valid: clientReference != ""},
		Direction:        nullablePSPDirection(direction),
		InteractionType:  "webhook_status_check",
		Method:           sql.NullString{String: "GET", Valid: true},
		URL:              sql.NullString{String: url, Valid: url != ""},
		RequestHeaders:   requestHeaders,
		RequestBody:      requestBody,
		ResponseBody:     responseBody,
		ErrorMessage:     sql.NullString{String: errorString(callErr), Valid: callErr != nil},
	}
	_, err = h.Store.RecordPSPInteraction(c.Context(), interaction)
	return err
}

func webhookIPAllowed(remoteIP string, allowedCIDRs []string) (bool, error) {
	parsed := net.ParseIP(strings.TrimSpace(remoteIP))
	if parsed == nil {
		return false, walletpsp.ErrPSPWebhookInvalid
	}
	for _, allowed := range allowedCIDRs {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if strings.Contains(allowed, "/") {
			_, network, err := net.ParseCIDR(allowed)
			if err != nil {
				return false, err
			}
			if network.Contains(parsed) {
				return true, nil
			}
			continue
		}
		allowedIP := net.ParseIP(allowed)
		if allowedIP == nil {
			return false, walletpsp.ErrPSPWebhookInvalid
		}
		if allowedIP.Equal(parsed) {
			return true, nil
		}
	}
	return false, nil
}

func webhookProviderHeaders(cfg *walletpsp.Config) (walletstore.RawJSON, error) {
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
	return rawJSONFromAny(headers)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstHeader(c *fiber.Ctx, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(c.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func stringFromMap(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed != "" {
				return typed
			}
		case []byte:
			if len(typed) > 0 {
				return string(typed)
			}
		case json.Number:
			return typed.String()
		case float64:
			return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.0f", typed), "0"), ".")
		}
	}
	return ""
}

func int64FromMap(payload map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, ok := payload[key]
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

func rawJSONFromAny(value any) (walletstore.RawJSON, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return walletstore.RawJSON(data), nil
}

func nullablePSPDirection(value string) sql.NullString {
	switch normalizeDirection(value) {
	case "deposit":
		return sql.NullString{String: "inbound", Valid: true}
	case "withdrawal":
		return sql.NullString{String: "outbound", Valid: true}
	case "inbound", "outbound":
		return sql.NullString{String: strings.ToLower(strings.TrimSpace(value)), Valid: true}
	default:
		return sql.NullString{}
	}
}

func normalizeDirection(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "inbound", "deposit":
		return "deposit"
	case "outbound", "withdrawal", "payout":
		return "withdrawal"
	default:
		return normalized
	}
}

func normalizePSPDirection(value string) string {
	switch normalizeDirection(value) {
	case "withdrawal":
		return "outbound"
	case "deposit":
		return "inbound"
	default:
		return ""
	}
}
