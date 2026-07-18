package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) GeneratePaymentToken(c *fiber.Ctx) error {
	userID := getUserID(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	var token ebs_fields.Token
	if err := bindJSON(c, &token); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "binding_error", "message": err.Error()})
	}

	created, encoded, paymentLink, err := h.Service.GeneratePaymentTokenForUserID(c.UserContext(), tenantID, userID, token)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": err.Error(), "message": "Unable to save payment token"})
	}
	return jsonResponse(c, http.StatusCreated, fiber.Map{"token": encoded, "result": encoded, "uuid": created.UUID, "payment_link": paymentLink})
}

func (h *Handler) GetPaymentToken(c *fiber.Ctx) error {
	userID := getUserID(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	uuid := strings.TrimSpace(c.Query("uuid"))

	tokens, token, err := h.Service.GetPaymentTokenForUserID(c.UserContext(), tenantID, userID, uuid)
	if err != nil {
		if uuid != "" {
			return jsonResponse(c, http.StatusNotFound, fiber.Map{"code": "record_not_found", "message": "token not found"})
		}
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "error_retrieving_tokens", "message": err.Error()})
	}
	if uuid == "" {
		return jsonResponse(c, http.StatusOK, fiber.Map{"token": tokens, "count": len(tokens)})
	}
	return jsonResponse(c, http.StatusOK, token)
}

func (h *Handler) NoebsQuickPayment(c *fiber.Ctx) error {
	userID := getUserID(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	uuidQuery := strings.TrimSpace(c.Query("uuid"))
	tokenQuery := strings.TrimSpace(c.Query("token"))

	var req ebs_fields.QuickPaymentFields
	if len(c.Body()) != 0 {
		if err := parseJSON(c, &req); err != nil {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "binding_error", "message": err.Error()})
		}
	}

	res, err := h.Service.NoebsQuickPayment(c.UserContext(), tenantID, userID, req, uuidQuery, tokenQuery)
	if err != nil {
		var callErr *ebs_fields.CallError
		if errors.As(err, &callErr) && callErr != nil {
			return jsonResponse(c, statusForError(err), ebsErrorDetails(callErr.Response))
		}
		return jsonResponse(c, statusForError(err), fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"ebs_response": res})
}
