package handler

import (
	"net/http"

	"github.com/adonese/noebs/consumer"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) ResolveQuickPaymentToken(c *fiber.Ctx) error {
	userID := getUserID(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	var cmd consumer.QuickPaymentTokenResolveCommand
	if err := bindJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "binding_error", "message": err.Error()})
	}
	resolution, err := h.Service.ResolveQuickPaymentTokenForUserID(c.UserContext(), tenantID, userID, cmd)
	if err != nil {
		return jsonResponse(c, statusForError(err), fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, resolution)
}

func (h *Handler) MarkQuickPaymentTokenPaid(c *fiber.Ctx) error {
	userID := getUserID(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	var cmd consumer.QuickPaymentTokenPaidCommand
	if err := bindJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "binding_error", "message": err.Error()})
	}
	if err := h.Service.MarkQuickPaymentTokenPaidForUserID(c.UserContext(), tenantID, userID, cmd); err != nil {
		return jsonResponse(c, statusForError(err), fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	return c.SendStatus(http.StatusNoContent)
}
