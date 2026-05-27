package handler

import (
	"net/http"

	"github.com/adonese/noebs/consumer"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) ResolveIdentityUserByMobile(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	var cmd consumer.IdentityUserByMobileCommand
	if err := parseJSON(c, &cmd); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "binding_error", "message": err.Error()})
	}
	result, err := h.Service.ResolveIdentityUserByMobile(c.UserContext(), tenantID, cmd)
	if err != nil {
		return jsonResponse(c, statusForError(err), fiber.Map{"code": err.Error(), "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, result)
}
