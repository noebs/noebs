package handler

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
)

func (h *Handler) SetMainCard(c *fiber.Ctx) error {
	type cardRequest struct {
		Pan string `json:"PAN"`
	}
	var req cardRequest
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"error": "Binding error, make sure the sent json is correct"})
	}
	userID := getUserID(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	if err := h.Service.SetMainCardForUserID(c.UserContext(), tenantID, userID, req.Pan); err != nil {
		return jsonResponse(c, statusForError(err), fiber.Map{"error": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
}

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
