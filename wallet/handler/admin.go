package handler

import (
	"net/http"

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
