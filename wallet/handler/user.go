package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type UserHandler struct {
	Service *wallet.Service
}

type ensureWalletRequest struct {
	TenantID *string `json:"tenant_id"`
	UserID   *int64  `json:"user_id"`
	Currency string  `json:"currency"`
}

type walletResponse struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	OwnerType        string    `json:"owner_type"`
	OwnerID          string    `json:"owner_id"`
	UserID           *int64    `json:"user_id,omitempty"`
	Currency         string    `json:"currency"`
	Balance          int64     `json:"balance"`
	AvailableBalance int64     `json:"available_balance"`
	Status           string    `json:"status"`
	KYCTier          string    `json:"kyc_tier"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type paymentMethodResponse struct {
	ProviderCode     string          `json:"provider_code"`
	ProviderName     string          `json:"provider_name"`
	DisplayName      string          `json:"display_name,omitempty"`
	MethodType       string          `json:"method_type"`
	Direction        string          `json:"direction"`
	Currencies       []string        `json:"currencies,omitempty"`
	Regions          []string        `json:"regions,omitempty"`
	MinAmount        *int64          `json:"min_amount,omitempty"`
	MaxAmount        *int64          `json:"max_amount,omitempty"`
	InputSchema      json.RawMessage `json:"input_schema,omitempty"`
	Presentation     json.RawMessage `json:"presentation,omitempty"`
	SupportsDeposit  bool            `json:"supports_deposit"`
	SupportsWithdraw bool            `json:"supports_withdrawal"`
}

type walletTransactionResponse struct {
	ID             int64           `json:"id"`
	TenantID       string          `json:"tenant_id"`
	TransactionID  int64           `json:"transaction_id"`
	WalletID       string          `json:"wallet_id"`
	EntryType      string          `json:"entry_type"`
	Amount         int64           `json:"amount"`
	Currency       string          `json:"currency"`
	BalanceAfter   int64           `json:"balance_after"`
	WalletSequence int64           `json:"wallet_sequence"`
	Status         string          `json:"status"`
	ReferenceType  string          `json:"reference_type"`
	ReferenceID    *string         `json:"reference_id,omitempty"`
	Description    *string         `json:"description,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

func NewUserHandler(service *wallet.Service) *UserHandler {
	return &UserHandler{Service: service}
}

func (h *UserHandler) EnsureWallet(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}

	var req ensureWalletRequest
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, 0, err)
	}

	userID, err := authenticatedUserID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := validateRequestedTenantID(req.TenantID, tenantID); err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := validateRequestedUserID(req.UserID, userID); err != nil {
		return jsonResponse(c, 0, err)
	}
	currency, err := resolveCurrency(h.Service.Config, req.Currency)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}

	w, err := h.Service.EnsureUserWallet(c.Context(), tenantID, userID, currency)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}

	return jsonResponse(c, http.StatusOK, walletResponseFromModel(w))
}

func (h *UserHandler) GetWallet(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}

	walletIDRaw := c.Params("id")
	if walletIDRaw == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingWalletID))
	}
	walletID, err := uuid.Parse(walletIDRaw)
	if err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid wallet id"))
	}

	userID, err := authenticatedUserID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := validateRequestedTenantID(requestedTenantIDFromQuery(c), tenantID); err != nil {
		return jsonResponse(c, 0, err)
	}

	w, err := h.Service.GetWallet(c.Context(), tenantID, walletID)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	if !walletOwnedByUser(w, tenantID, userID) {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrWalletNotFound))
	}

	return jsonResponse(c, http.StatusOK, walletResponseFromModel(w))
}

func (h *UserHandler) ListWalletTransactions(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}

	walletIDRaw := c.Params("id")
	if walletIDRaw == "" {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrMissingWalletID))
	}
	walletID, err := uuid.Parse(walletIDRaw)
	if err != nil {
		return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid wallet id"))
	}

	userID, err := authenticatedUserID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := validateRequestedTenantID(requestedTenantIDFromQuery(c), tenantID); err != nil {
		return jsonResponse(c, 0, err)
	}

	w, err := h.Service.GetWallet(c.Context(), tenantID, walletID)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	if !walletOwnedByUser(w, tenantID, userID) {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrWalletNotFound))
	}

	limit, err := optionalIntQuery(c, "limit", 100)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidLimit))
	}
	offset, err := optionalIntQuery(c, "offset", 0)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidOffset))
	}
	entries, err := h.Service.Store.ListWalletLedgerEntries(c.Context(), walletstore.WalletLedgerEntryFilter{
		TenantID:  tenantID,
		WalletID:  walletID,
		EntryType: c.Query("entry_type"),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	resp := make([]walletTransactionResponse, 0, len(entries))
	for _, entry := range entries {
		resp = append(resp, walletTransactionResponseFromModel(entry))
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"transactions": resp})
}

func (h *UserHandler) ListPaymentMethods(c *fiber.Ctx) error {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := authenticatedTenantID(c)
	if err != nil {
		return jsonResponse(c, 0, err)
	}
	if err := validateRequestedTenantID(requestedTenantIDFromQuery(c), tenantID); err != nil {
		return jsonResponse(c, 0, err)
	}
	amount, err := optionalInt64Query(c, "amount")
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidAmount))
	}
	limit, err := optionalIntQuery(c, "limit", 100)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidLimit))
	}
	offset, err := optionalIntQuery(c, "offset", 0)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(walletstore.ErrInvalidOffset))
	}
	methods, err := h.Service.Store.ListAvailablePSPMethods(c.Context(), walletstore.PSPMethodFilter{
		TenantID:  tenantID,
		Direction: c.Query("direction"),
		Currency:  c.Query("currency"),
		Region:    c.Query("region"),
		Amount:    amount,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	resp := make([]paymentMethodResponse, 0, len(methods))
	for _, method := range methods {
		resp = append(resp, paymentMethodResponseFromModel(method))
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"methods": resp})
}

func authenticatedUserID(c *fiber.Ctx) (int64, error) {
	if c == nil {
		return 0, apperr.ErrUnauthorized
	}
	raw := c.Locals("user_id")
	userID, ok := raw.(int64)
	if !ok || userID <= 0 {
		return 0, apperr.ErrUnauthorized
	}
	return userID, nil
}

func authenticatedTenantID(c *fiber.Ctx) (string, error) {
	if c == nil {
		return "", apperr.ErrUnauthorized
	}
	raw := c.Locals("tenant_id")
	tenantID, ok := raw.(string)
	if !ok {
		return "", apperr.ErrUnauthorized
	}
	tenantID, err := walletstore.ValidateTenantID(tenantID)
	if err != nil {
		return "", apperr.ErrUnauthorized
	}
	return tenantID, nil
}

func validateRequestedTenantID(requested *string, authenticated string) error {
	if requested == nil {
		return nil
	}
	tenantID := strings.TrimSpace(*requested)
	tenantID, err := walletstore.ValidateTenantID(tenantID)
	if err != nil {
		return mapWalletError(err)
	}
	if tenantID != authenticated {
		return apperr.ErrForbidden
	}
	return nil
}

func validateRequestedUserID(requested *int64, authenticated int64) error {
	if requested == nil {
		return nil
	}
	if *requested <= 0 {
		return mapWalletError(walletstore.ErrInvalidUserID)
	}
	if *requested != authenticated {
		return apperr.ErrForbidden
	}
	return nil
}

func requestedTenantIDFromQuery(c *fiber.Ctx) *string {
	if c == nil || !c.Request().URI().QueryArgs().Has("tenant_id") {
		return nil
	}
	tenantID := c.Query("tenant_id")
	return &tenantID
}

func optionalIntQuery(c *fiber.Ctx, key string, defaultValue int) (int, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func optionalInt64Query(c *fiber.Ctx, key string) (int64, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func walletOwnedByUser(w *walletstore.Wallet, tenantID string, userID int64) bool {
	if w == nil || userID <= 0 {
		return false
	}
	if w.TenantID != tenantID || w.OwnerType != walletstore.OwnerTypeUser {
		return false
	}
	if w.UserID.Valid {
		return w.UserID.Int64 == userID
	}
	return w.OwnerID == strconv.FormatInt(userID, 10)
}

func walletResponseFromModel(w *walletstore.Wallet) walletResponse {
	var userID *int64
	if w.UserID.Valid {
		userID = &w.UserID.Int64
	}
	return walletResponse{
		ID:               w.ID.String(),
		TenantID:         w.TenantID,
		OwnerType:        w.OwnerType,
		OwnerID:          w.OwnerID,
		UserID:           userID,
		Currency:         w.Currency,
		Balance:          w.Balance,
		AvailableBalance: w.AvailableBalance,
		Status:           w.Status,
		KYCTier:          w.KYCTier,
		CreatedAt:        w.CreatedAt,
		UpdatedAt:        w.UpdatedAt,
	}
}

func paymentMethodResponseFromModel(method walletstore.PSPPaymentMethod) paymentMethodResponse {
	var minAmount *int64
	if method.MinAmount.Valid {
		minAmount = &method.MinAmount.Int64
	}
	var maxAmount *int64
	if method.MaxAmount.Valid {
		maxAmount = &method.MaxAmount.Int64
	}
	return paymentMethodResponse{
		ProviderCode:     method.ProviderCode,
		ProviderName:     method.ProviderName,
		DisplayName:      method.DisplayName,
		MethodType:       method.MethodType,
		Direction:        method.Direction,
		Currencies:       method.Currencies,
		Regions:          method.Regions,
		MinAmount:        minAmount,
		MaxAmount:        maxAmount,
		InputSchema:      json.RawMessage(method.InputSchema),
		Presentation:     json.RawMessage(method.Presentation),
		SupportsDeposit:  method.SupportsDeposit,
		SupportsWithdraw: method.SupportsWithdraw,
	}
}

func walletTransactionResponseFromModel(entry walletstore.WalletLedgerEntry) walletTransactionResponse {
	var referenceID *string
	if entry.ReferenceID.Valid {
		referenceID = &entry.ReferenceID.String
	}
	var description *string
	if entry.Description.Valid {
		description = &entry.Description.String
	}
	return walletTransactionResponse{
		ID:             entry.ID,
		TenantID:       entry.TenantID,
		TransactionID:  entry.TransactionID,
		WalletID:       entry.WalletID.String(),
		EntryType:      entry.EntryType,
		Amount:         entry.Amount,
		Currency:       entry.Currency,
		BalanceAfter:   entry.BalanceAfter,
		WalletSequence: entry.WalletSequence,
		Status:         entry.Status,
		ReferenceType:  entry.ReferenceType,
		ReferenceID:    referenceID,
		Description:    description,
		Metadata:       json.RawMessage(entry.Metadata),
		CreatedAt:      entry.CreatedAt,
	}
}
