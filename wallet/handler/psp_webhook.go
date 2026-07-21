package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/internal/workloadauth"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/gofiber/fiber/v2"
)

var (
	ErrPSPWebhookProviderMismatch  = errors.New("psp webhook provider mismatch")
	ErrPSPWebhookDirectionMismatch = errors.New("psp webhook direction mismatch")
)

type PSPWebhookHandler struct {
	Store    *walletstore.Store
	Loader   *walletpsp.Loader
	Registry *walletpsp.Registry
}

func NewPSPWebhookHandler(store *walletstore.Store, loader *walletpsp.Loader, registry *walletpsp.Registry) *PSPWebhookHandler {
	return &PSPWebhookHandler{Store: store, Loader: loader, Registry: registry}
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
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&payloadMap); err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid webhook payload"))
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return jsonResponse(c, 0, apperr.Wrap(walletpsp.ErrPSPResponseInvalid, apperr.ErrBadRequest, "invalid webhook payload"))
	}

	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}

	if h.Loader == nil || h.Registry == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	cfg, err := h.Loader.LoadWebhook(c.Context(), tenantID, providerCode, walletpsp.Scope{})
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
	fields, err := mappedPSPWebhookFields(payloadMap, cfg.WebhookResponseMapping)
	if err != nil {
		errMsg := err.Error()
		if logErr := h.recordWebhookInteraction(c, tenantID, providerCode, "", "", "", http.StatusBadRequest, fiber.Map{"error": errMsg}, errMsg, payload); logErr != nil {
			return jsonResponse(c, 0, mapWalletError(logErr))
		}
		return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(err, apperr.ErrBadRequest, errMsg))
	}
	provider, err := h.Registry.Resolve(cfg)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(err, apperr.ErrBadRequest, err.Error()))
	}

	signature := webhookSignature(c)
	signatureValid := provider.VerifyWebhook(payload, signature)
	checkedMap, checkedPayload, authErr := h.authorizeWebhook(c, cfg, provider, providerCode, tenantID, fields, signatureValid, payloadMap, payload)
	if authErr != nil {
		return h.rejectWebhookAuthentication(c, tenantID, providerCode, fields, payload, authErr)
	}
	payloadMap, payload = checkedMap, checkedPayload
	fields, err = mappedPSPWebhookFields(payloadMap, cfg.WebhookResponseMapping)
	if err != nil {
		errMsg := err.Error()
		if logErr := h.recordWebhookInteraction(c, tenantID, providerCode, "", "", "", http.StatusBadRequest, fiber.Map{"error": errMsg}, errMsg, payload); logErr != nil {
			return jsonResponse(c, 0, mapWalletError(logErr))
		}
		return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(err, apperr.ErrBadRequest, errMsg))
	}

	if fields.ClientReference == "" {
		if err := h.recordWebhookInteraction(c, tenantID, providerCode, fields.ClientReference, fields.PSPTransactionID, fields.Direction, http.StatusBadRequest, fiber.Map{"error": walletstore.ErrMissingClientReference.Error()}, walletstore.ErrMissingClientReference.Error(), payload); err != nil {
			return jsonResponse(c, 0, mapWalletError(err))
		}
		return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(walletstore.ErrMissingClientReference, apperr.ErrBadRequest, "missing client reference"))
	}
	if fields.Status == "" {
		if err := h.recordWebhookInteraction(c, tenantID, providerCode, fields.ClientReference, fields.PSPTransactionID, fields.Direction, http.StatusBadRequest, fiber.Map{"error": walletstore.ErrMissingStatus.Error()}, walletstore.ErrMissingStatus.Error(), payload); err != nil {
			return jsonResponse(c, 0, mapWalletError(err))
		}
		return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(walletstore.ErrMissingStatus, apperr.ErrBadRequest, "missing status"))
	}

	clientRef := fields.ClientReference
	pspTransactionID := fields.PSPTransactionID

	stored, err := h.Store.GetPSPTransactionByReference(c.Context(), tenantID, clientRef)
	if err != nil {
		if errors.Is(err, walletstore.ErrPSPTransactionNotFound) {
			if err := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, fields.Direction, http.StatusNotFound, fiber.Map{"error": err.Error(), "client_reference": clientRef}, err.Error(), payload); err != nil {
				return jsonResponse(c, 0, mapWalletError(err))
			}
			return jsonResponse(c, 0, mapWalletError(err))
		}
		return jsonResponse(c, 0, mapWalletError(err))
	}
	if err := validatePSPWebhookTransaction(stored, providerCode, fields.Direction); err != nil {
		statusCode := http.StatusBadRequest
		publicErr := apperr.Wrap(err, apperr.ErrBadRequest, err.Error())
		if errors.Is(err, ErrPSPWebhookProviderMismatch) {
			statusCode = http.StatusNotFound
			publicErr = apperr.Wrap(walletstore.ErrPSPTransactionNotFound, apperr.ErrNotFound, walletstore.ErrPSPTransactionNotFound.Error())
		}
		if logErr := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, fields.Direction, statusCode, fiber.Map{"error": publicErr.Error(), "client_reference": clientRef}, err.Error(), payload); logErr != nil {
			return jsonResponse(c, 0, mapWalletError(logErr))
		}
		return jsonResponse(c, statusCode, publicErr)
	}
	if err := validateTerminalPSPWebhook(stored, fields); err != nil {
		if logErr := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, fields.Direction, http.StatusBadRequest, fiber.Map{"error": err.Error(), "client_reference": clientRef}, err.Error(), payload); logErr != nil {
			return jsonResponse(c, 0, mapWalletError(logErr))
		}
		return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(err, apperr.ErrBadRequest, err.Error()))
	}

	signalCurrency := strings.TrimSpace(fields.Currency)
	if stored.WorkflowID.Valid && walletstore.PSPTransactionStatusTerminal(fields.Status) {
		if signalCurrency == "" {
			if err := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, stored.Direction, http.StatusBadRequest, fiber.Map{"error": walletstore.ErrMissingCurrency.Error()}, walletstore.ErrMissingCurrency.Error(), payload); err != nil {
				return jsonResponse(c, 0, mapWalletError(err))
			}
			return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(walletstore.ErrMissingCurrency, apperr.ErrBadRequest, "missing currency"))
		}
	}

	update := pspWebhookStatusUpdate(stored, fields, payload, time.Now().UTC())
	var workflowSignal *walletstore.PSPWorkflowSignal
	if stored.WorkflowID.Valid && walletstore.PSPTransactionStatusTerminal(update.Status) {
		workflowSignal = &walletstore.PSPWorkflowSignal{
			ProviderTxID: pspTransactionID,
			Amount:       fields.Amount,
			Currency:     signalCurrency,
			Status:       update.Status,
			RawResponse:  walletstore.RawJSON(payload),
		}
	}
	stored, err = h.Store.ApplyExternalPSPStatus(c.Context(), tenantID, clientRef, update, workflowSignal)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}

	if err := h.recordWebhookInteraction(c, tenantID, providerCode, clientRef, pspTransactionID, stored.Direction, http.StatusOK, fiber.Map{"status": stored.Status, "client_reference": clientRef}, "", payload); err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"status": stored.Status, "client_reference": clientRef})
}

func (h *PSPWebhookHandler) rejectWebhookAuthentication(c *fiber.Ctx, tenantID, providerCode string, fields pspWebhookFields, payload []byte, authErr error) error {
	errMsg := authErr.Error()
	if errors.Is(authErr, walletpsp.ErrPSPWebhookInvalid) {
		errMsg = "webhook authentication failed"
	}
	if err := h.recordWebhookInteraction(c, tenantID, providerCode, fields.ClientReference, fields.PSPTransactionID, fields.Direction, http.StatusBadRequest, fiber.Map{"error": errMsg}, errMsg, payload); err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	return jsonResponse(c, http.StatusBadRequest, apperr.Wrap(walletpsp.ErrPSPWebhookInvalid, apperr.ErrBadRequest, errMsg))
}

func validatePSPWebhookTransaction(stored *walletstore.PSPTransaction, providerCode, callbackDirection string) error {
	if stored == nil {
		return walletstore.ErrPSPTransactionNotFound
	}
	if stored.PSPProvider != providerCode {
		return ErrPSPWebhookProviderMismatch
	}
	if callbackDirection == "" {
		return nil
	}
	expectedDirection := normalizeDirection(stored.Direction)
	if expectedDirection != "deposit" && expectedDirection != "withdrawal" {
		return walletstore.ErrInvalidDirection
	}
	if normalizeDirection(callbackDirection) != expectedDirection {
		return ErrPSPWebhookDirectionMismatch
	}
	return nil
}

func validateTerminalPSPWebhook(stored *walletstore.PSPTransaction, fields pspWebhookFields) error {
	if stored == nil || !walletstore.PSPTransactionStatusTerminal(fields.Status) {
		return nil
	}
	if stored.Direction == "outbound" && fields.Status != walletstore.PSPStatusSuccess {
		return nil
	}
	if fields.PSPTransactionID == "" {
		return walletstore.ErrMissingPSPTransactionID
	}
	if stored.PSPTransactionID.Valid && stored.PSPTransactionID.String != fields.PSPTransactionID {
		return walletstore.ErrDuplicateTransaction
	}
	if fields.Amount != stored.Amount {
		return walletstore.ErrInvalidAmount
	}
	if strings.TrimSpace(fields.Currency) != stored.Currency {
		return walletstore.ErrCurrencyMismatch
	}
	return nil
}

type pspWebhookFields struct {
	ClientReference  string
	PSPTransactionID string
	Status           string
	Amount           int64
	Currency         string
	Direction        string
	Message          string
}

func pspWebhookStatusUpdate(stored *walletstore.PSPTransaction, fields pspWebhookFields, payload []byte, now time.Time) walletstore.PSPStatusUpdate {
	update := walletstore.PSPStatusUpdate{
		Status:      fields.Status,
		RawResponse: walletstore.RawJSON(payload),
	}
	if fields.PSPTransactionID != "" {
		update.PSPTransactionID = sql.NullString{String: fields.PSPTransactionID, Valid: true}
	}
	if fields.Message != "" {
		update.ResponseMessage = sql.NullString{String: fields.Message, Valid: true}
	}
	if update.Status == walletstore.PSPStatusSuccess && (stored == nil || !stored.ConfirmedAt.Valid) {
		update.ConfirmedAt = sql.NullTime{Time: now, Valid: true}
	}
	return update
}

func mappedPSPWebhookFields(payload map[string]any, mapping walletpsp.ResponseMapping) (pspWebhookFields, error) {
	mapped, err := walletpsp.MapResponse(payload, mapping)
	if err != nil {
		return pspWebhookFields{}, err
	}
	return pspWebhookFields{
		ClientReference:  mapped.ClientReference,
		PSPTransactionID: mapped.TransactionID,
		Status:           mapped.Status,
		Amount:           mapped.Amount,
		Currency:         mapped.Currency,
		Direction:        normalizeDirection(mapped.Direction),
		Message:          mapped.Message,
	}, nil
}

func (h *PSPWebhookHandler) recordWebhookInteraction(c *fiber.Ctx, tenantID, providerCode, clientReference, pspTransactionID, direction string, statusCode int, responseBody any, errorMessage string, payload []byte) error {
	if h == nil || h.Store == nil {
		return apperr.ErrUnavailable
	}
	requestHeaders, err := webhookAuditHeaders(c)
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

func (h *PSPWebhookHandler) authorizeIPAllowedWebhook(c *fiber.Ctx, cfg *walletpsp.Config, provider walletpsp.Provider, providerCode, tenantID, clientReference, pspTransactionID, direction string, payloadMap map[string]any, payload []byte) (map[string]any, []byte, error) {
	if cfg == nil {
		return nil, nil, walletpsp.ErrPSPWebhookInvalid
	}
	sourceIP, ok := c.Locals("request_source").(string)
	if !ok {
		return nil, nil, walletpsp.ErrPSPWebhookInvalid
	}
	allowed, err := webhookIPAllowed(sourceIP, cfg.WebhookAllowedCIDRs)
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, walletpsp.ErrPSPWebhookInvalid
	}
	if !cfg.StatusCheckWebhook {
		return payloadMap, payload, nil
	}
	if pspTransactionID == "" {
		return nil, nil, walletstore.ErrMissingPSPTransactionID
	}
	if clientReference == "" {
		return nil, nil, walletstore.ErrMissingClientReference
	}
	stored, err := h.Store.GetPSPTransactionByReference(c.Context(), tenantID, clientReference)
	if err != nil {
		return nil, nil, err
	}
	if err := validatePSPWebhookTransaction(stored, providerCode, direction); err != nil {
		return nil, nil, err
	}
	if stored.PSPTransactionID.Valid && stored.PSPTransactionID.String != pspTransactionID {
		return nil, nil, walletstore.ErrDuplicateTransaction
	}
	status, callErr := provider.GetTransactionStatus(c.Context(), walletpsp.TransactionLookup{
		ProviderTxID: pspTransactionID, IdempotencyKey: stored.IdempotencyKey, ClientReference: clientReference,
	})
	auditErr := h.recordWebhookStatusCheck(c, cfg, tenantID, providerCode, clientReference, pspTransactionID, direction, status, callErr)
	if auditErr != nil {
		return nil, nil, auditErr
	}
	if callErr != nil {
		return nil, nil, callErr
	}
	authoritative := authoritativeWebhookPayload(payloadMap, cfg.WebhookResponseMapping, clientReference, pspTransactionID, direction, status)
	raw, err := json.Marshal(authoritative)
	if err != nil {
		return nil, nil, err
	}
	return authoritative, raw, nil
}

func (h *PSPWebhookHandler) authorizeWebhook(c *fiber.Ctx, cfg *walletpsp.Config, provider walletpsp.Provider, providerCode, tenantID string, fields pspWebhookFields, signatureValid bool, payloadMap map[string]any, payload []byte) (map[string]any, []byte, error) {
	if cfg == nil {
		return nil, nil, walletpsp.ErrPSPWebhookInvalid
	}
	switch strings.ToLower(strings.TrimSpace(cfg.WebhookAuthMode)) {
	case "signature":
		if !signatureValid {
			return nil, nil, walletpsp.ErrPSPWebhookInvalid
		}
		return payloadMap, payload, nil
	case "ip_allowlist":
		return h.authorizeIPAllowedWebhook(c, cfg, provider, providerCode, tenantID, fields.ClientReference, fields.PSPTransactionID, fields.Direction, payloadMap, payload)
	default:
		return nil, nil, walletpsp.ErrPSPWebhookInvalid
	}
}

func authoritativeWebhookPayload(original map[string]any, mapping walletpsp.ResponseMapping, clientReference, pspTransactionID, direction string, status *walletpsp.TxStatus) map[string]any {
	authoritative := cloneWebhookPayload(original)
	if status != nil && status.RawResponse != nil {
		for key, value := range status.RawResponse {
			authoritative[key] = value
		}
	}

	setWebhookMappedValue(authoritative, mapping.ClientReference, clientReference)
	if clientReference != "" {
		authoritative["client_reference"] = clientReference
	}

	transactionID := pspTransactionID
	if status != nil && status.ProviderTxID != "" {
		transactionID = status.ProviderTxID
	}
	setWebhookMappedValue(authoritative, mapping.TransactionID, transactionID)
	if transactionID != "" {
		authoritative["transaction_id"] = transactionID
		authoritative["psp_transaction_id"] = transactionID
	}

	if direction != "" {
		setWebhookMappedValue(authoritative, mapping.Direction, direction)
		authoritative["direction"] = direction
	}

	if status == nil {
		return authoritative
	}
	if status.Status != "" {
		setWebhookMappedValue(authoritative, mapping.Status, status.Status)
		authoritative["status"] = status.Status
	}
	if status.Amount > 0 {
		setWebhookMappedValue(authoritative, mapping.Amount, status.Amount)
		authoritative["amount"] = status.Amount
	}
	if status.Currency != "" {
		setWebhookMappedValue(authoritative, mapping.Currency, status.Currency)
		authoritative["currency"] = status.Currency
	}
	return authoritative
}

func cloneWebhookPayload(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		if nested, ok := value.(map[string]any); ok {
			out[key] = cloneWebhookPayload(nested)
			continue
		}
		out[key] = value
	}
	return out
}

func setWebhookMappedValue(payload map[string]any, paths []string, value any) {
	if payload == nil || value == nil {
		return
	}
	for _, path := range paths {
		setWebhookValueAtPath(payload, path, value)
	}
}

func setWebhookValueAtPath(payload map[string]any, path string, value any) {
	parts := splitWebhookPath(path)
	if len(parts) == 0 {
		return
	}
	current := payload
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func splitWebhookPath(path string) []string {
	raw := strings.Split(path, ".")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
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

func webhookAuditHeaders(c *fiber.Ctx) (walletstore.RawJSON, error) {
	headers := c.GetReqHeaders()
	redacted := map[string]struct{}{
		"authorization":       {},
		"cookie":              {},
		"proxy-authorization": {},
		"signature":           {},
		"x-signature":         {},
		"x-webhook-signature": {},
	}
	for _, name := range workloadauth.IdentityHeaderNames() {
		redacted[strings.ToLower(name)] = struct{}{}
	}
	for _, name := range workloadauth.WorkloadHeaderNames() {
		redacted[strings.ToLower(name)] = struct{}{}
	}
	for name := range headers {
		if _, sensitive := redacted[strings.ToLower(name)]; sensitive {
			headers[name] = []string{"REDACTED"}
		}
	}
	return rawJSONFromAny(headers)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func webhookSignature(c *fiber.Ctx) string {
	values := c.Request().Header.PeekAll("X-Webhook-Signature")
	if len(values) != 1 {
		return ""
	}
	return strings.TrimSpace(string(values[0]))
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
