package handler

import (
	"net/http"
	"strconv"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/wallet"
	"github.com/gofiber/fiber/v2"
)

type AdminHandler struct {
	Service *wallet.Service
}

func NewAdminHandler(service *wallet.Service) *AdminHandler {
	return &AdminHandler{Service: service}
}

func (h *AdminHandler) Dashboard(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	return c.Status(http.StatusNotImplemented).JSON(fiber.Map{"code": "not_implemented", "message": "wallet admin dashboard not implemented"})
}

func (h *AdminHandler) ListWallets(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	if !h.Service.Config.WalletEnabled {
		return jsonResponse(c, http.StatusServiceUnavailable, apperr.ErrUnavailable)
	}
	tenantID, err := resolveTenantID(h.Service.Config, c.Query("tenant_id"))
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid limit"))
		}
		limit = parsed
	}
	offset := 0
	if raw := c.Query("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrBadRequest, "invalid offset"))
		}
		offset = parsed
	}

	wallets, err := h.Service.Store.ListWallets(c.Context(), tenantID, limit, offset)
	if err != nil {
		return jsonResponse(c, 0, mapWalletError(err))
	}
	resp := make([]walletResponse, 0, len(wallets))
	for i := range wallets {
		wallet := wallets[i]
		resp = append(resp, walletResponseFromModel(&wallet))
	}
	return jsonResponse(c, http.StatusOK, resp)
}
