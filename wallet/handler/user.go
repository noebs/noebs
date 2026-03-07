package handler

import (
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
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", apperr.ErrUnauthorized
	}
	return tenantID, nil
}

func validateRequestedTenantID(requested *string, authenticated string) error {
	if requested == nil {
		return nil
	}
	tenantID := strings.TrimSpace(*requested)
	if tenantID == "" {
		return mapWalletError(walletstore.ErrMissingTenantID)
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
