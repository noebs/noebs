package handler

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
)

func (h *Handler) GetTransactions(c *fiber.Ctx) error {
	userID := getUserID(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	trans, err := h.Service.GetTransactionsForUserID(c.UserContext(), tenantID, userID)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	return jsonResponse(c, http.StatusOK, trans)
}
