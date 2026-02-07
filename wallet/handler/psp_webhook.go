package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/wallet"
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
	Service  *wallet.Service
	Loader   *walletpsp.Loader
	Registry *walletpsp.Registry
	Temporal TemporalSignaler
}

func NewPSPWebhookHandler(service *wallet.Service, loader *walletpsp.Loader, registry *walletpsp.Registry, temporal TemporalSignaler) *PSPWebhookHandler {
	return &PSPWebhookHandler{Service: service, Loader: loader, Registry: registry, Temporal: temporal}
}

func (h *PSPWebhookHandler) Handle(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
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
	if tenantID == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingTenantID))
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
	provider, err := h.Registry.Resolve(cfg)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(err, apperr.ErrBadRequest, err.Error()))
	}

	signature := strings.TrimSpace(firstHeader(c, "X-Webhook-Signature", "X-Signature", "Signature"))
	if !provider.VerifyWebhook(payload, signature) {
		return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(walletpsp.ErrPSPWebhookInvalid, apperr.ErrBadRequest, "invalid webhook signature"))
	}

	if clientRef == "" {
		amount := int64FromMap(payloadMap, "amount")
		if amount <= 0 || currency == "" {
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
			RawResponse:     json.RawMessage(payload),
		}
		if pspTransactionID != "" {
			txn.PSPTransactionID = sql.NullString{String: pspTransactionID, Valid: true}
		}
		if _, err := h.Service.Store.CreatePSPTransaction(c.Context(), txn); err != nil {
			return jsonResponse(c, 0, mapWalletError(err))
		}
		return jsonResponse(c, http.StatusAccepted, fiber.Map{"status": "held", "client_reference": clientRef})
	}

	stored, err := h.Service.Store.GetPSPTransactionByReference(c.Context(), tenantID, clientRef)
	if err != nil {
		if errors.Is(err, walletstore.ErrPSPTransactionNotFound) {
			amount := int64FromMap(payloadMap, "amount")
			if amount <= 0 || currency == "" {
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
				RawResponse:     json.RawMessage(payload),
			}
			if pspTransactionID != "" {
				txn.PSPTransactionID = sql.NullString{String: pspTransactionID, Valid: true}
			}
			if _, err := h.Service.Store.CreatePSPTransaction(c.Context(), txn); err != nil {
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
	update.RawResponse = json.RawMessage(payload)
	if update.Status == "success" {
		update.ConfirmedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	}
	if err := h.Service.Store.UpdatePSPTransactionStatus(c.Context(), tenantID, clientRef, update); err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}

	if h.Temporal != nil && stored.WorkflowID.Valid {
		signal := walletpsp.TxStatus{ProviderTxID: pspTransactionID, Status: update.Status, RawResponse: payloadMap}
		if err := h.Temporal.SignalWorkflow(c.Context(), stored.WorkflowID.String, "", walletworkflow.PSPStatusUpdateSignal, signal); err != nil {
			if _, ok := err.(*serviceerror.NotFound); !ok {
				return jsonResponse(c, http.StatusAccepted, fiber.Map{"status": update.Status, "client_reference": clientRef, "signal_error": err.Error()})
			}
		}
	}

	return jsonResponse(c, http.StatusOK, fiber.Map{"status": update.Status, "client_reference": clientRef})
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
		return "inbound"
	}
}
