package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) BillPayment(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerBillPaymentFields
	return handleConfiguredEBS(h, c, &req, func(r *ebs_fields.ConsumerBillPaymentFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.BillPayment, nil)
}

func (h *Handler) GetBills(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req consumer.Bills
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	res, due, err := h.Service.GetBills(c.UserContext(), tenantID, req)
	if err != nil {
		var callErr *ebs_fields.CallError
		if errors.As(err, &callErr) && callErr != nil {
			return jsonResponse(c, statusForError(err), ebsErrorDetails(callErr.Response))
		}
		return jsonResponse(c, statusForError(err), fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"ebs_response": res, "due_amount": due})
}

func (h *Handler) GetBiller(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	mobile := strings.TrimSpace(c.Query("mobile"))
	if mobile == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "empty_mobile", "code": "empty_mobile"})
	}

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	billerID, err := h.Service.GetBiller(c.UserContext(), tenantID, mobile)
	if err != nil {
		return jsonResponse(c, statusForError(err), fiber.Map{"message": err.Error(), "code": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"biller_id": billerID})
}
