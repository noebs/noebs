package handler

import (
	"net/http"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) RegisterWithCard(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var card ebs_fields.CacheCards
	if err := bindJSON(c, &card); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	if _, err := h.Service.RegisterWithCard(c.UserContext(), tenantID, card); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "invalid_card_or_missing_credentials"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
}

func (h *Handler) CheckUser(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	type checkUserRequest struct {
		Phones []string `json:"phones"`
	}
	var req checkUserRequest
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Bad request.", "code": "bad_request"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	out, err := h.Service.CheckUser(c.UserContext(), tenantID, req.Phones)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	return jsonResponse(c, http.StatusOK, out)
}

func (h *Handler) SetMainCard(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
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
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"error": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
}

func (h *Handler) GetTransactions(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
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
