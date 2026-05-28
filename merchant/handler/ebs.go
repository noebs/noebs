package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) IsAlive(c *fiber.Ctx) error {
	var req ebs_fields.IsAliveFields
	return handleEBS(h, c, &req, nil, h.Service.IsAlive)
}

func (h *Handler) WorkingKey(c *fiber.Ctx) error {
	var req ebs_fields.WorkingKeyFields
	return handleEBS(h, c, &req, nil, h.Service.WorkingKey)
}

func (h *Handler) Purchase(c *fiber.Ctx) error {
	var req ebs_fields.PurchaseFields
	return handleEBS(h, c, &req, nil, h.Service.Purchase)
}

func (h *Handler) Balance(c *fiber.Ctx) error {
	var req ebs_fields.BalanceFields
	return handleEBS(h, c, &req, nil, h.Service.Balance)
}

func (h *Handler) CardTransfer(c *fiber.Ctx) error {
	var req ebs_fields.CardTransferFields
	return handleEBS(h, c, &req, nil, h.Service.CardTransfer)
}

func (h *Handler) BillInquiry(c *fiber.Ctx) error {
	var req ebs_fields.BillInquiryFields
	return handleEBS(h, c, &req, nil, h.Service.BillInquiry)
}

func (h *Handler) BillPayment(c *fiber.Ctx) error {
	var req ebs_fields.BillPaymentFields
	return handleEBS(h, c, &req, nil, h.Service.BillPayment)
}

func (h *Handler) TopUpPayment(c *fiber.Ctx) error {
	var req ebs_fields.BillPaymentFields
	return handleEBS(h, c, &req, nil, h.Service.TopUpPayment)
}

func (h *Handler) ChangePIN(c *fiber.Ctx) error {
	var req ebs_fields.ChangePINFields
	return handleEBS(h, c, &req, nil, h.Service.ChangePIN)
}

func (h *Handler) CashOut(c *fiber.Ctx) error {
	var req ebs_fields.CashOutFields
	return handleEBS(h, c, &req, nil, h.Service.CashOut)
}

func (h *Handler) VoucherCashOut(c *fiber.Ctx) error {
	var req ebs_fields.VoucherCashOutFields
	return handleEBS(h, c, &req, nil, h.Service.VoucherCashOut)
}

func (h *Handler) VoucherCashIn(c *fiber.Ctx) error {
	var req ebs_fields.VoucherCashInFields
	return handleEBS(h, c, &req, nil, h.Service.VoucherCashIn)
}

// Statement maps to EBS mini-statement.
func (h *Handler) Statement(c *fiber.Ctx) error {
	var req ebs_fields.MiniStatementFields
	return handleEBS(h, c, &req, nil, h.Service.Statement)
}

// GenerateVoucher requests a voucher from EBS.
func (h *Handler) GenerateVoucher(c *fiber.Ctx) error {
	var req ebs_fields.GenerateVoucherFields
	return handleEBS(h, c, &req, nil, h.Service.GenerateVoucher)
}

func (h *Handler) CashIn(c *fiber.Ctx) error {
	var req ebs_fields.CashInFields
	return handleEBS(h, c, &req, nil, h.Service.CashIn)
}

func (h *Handler) ToAccount(c *fiber.Ctx) error {
	var req ebs_fields.AccountTransferFields
	return handleEBS(h, c, &req, nil, h.Service.ToAccount)
}

func (h *Handler) MiniStatement(c *fiber.Ctx) error {
	var req ebs_fields.MiniStatementFields
	return handleEBS(h, c, &req, nil, h.Service.MiniStatement)
}

// Refund requests a refund for supported refund services in EBS merchant.
func (h *Handler) Refund(c *fiber.Ctx) error {
	var req ebs_fields.RefundFields
	return handleEBS(h, c, &req, nil, h.Service.Refund)
}

// EBS is an EBS compatible endpoint that proxies /ebs/* to the upstream EBS merchant endpoint.
func (h *Handler) EBS(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	path := strings.TrimPrefix(c.Path(), "/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 || parts[0] != "ebs" || strings.TrimSpace(parts[1]) == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": "missing ebs endpoint"})
	}
	endpoint := parts[1]

	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	res, callErr := h.Service.Proxy(c.UserContext(), tenantID, endpoint, c.Body())
	if callErr != nil {
		var ebsCallErr *ebs_fields.CallError
		if errors.As(callErr, &ebsCallErr) && ebsCallErr != nil {
			return jsonResponse(c, statusForError(callErr), ebsErrorDetails(ebsCallErr.Response))
		}
		return jsonResponse(c, statusForError(callErr), fiber.Map{"code": "bad_request", "message": callErr.Error()})
	}
	return jsonResponse(c, http.StatusOK, res)
}
