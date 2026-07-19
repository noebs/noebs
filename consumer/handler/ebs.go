package handler

import (
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func (h *Handler) Purchase(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerPurchaseFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerPurchaseFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
		r.DynamicFees = h.Service.NoebsConfig.EBSDynamicFees.SpecialPaymentFees
	}, h.Service.Purchase, nil)
}

func (h *Handler) IsAlive(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerIsAliveFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerIsAliveFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.IsAlive, nil)
}

func (h *Handler) BillInquiry(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerBillInquiryFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerBillInquiryFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.BillInquiry, nil)
}

func (h *Handler) Balance(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerBalanceFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerBalanceFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.Balance, nil)
}

func (h *Handler) TransactionStatus(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerTransactionStatusFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerTransactionStatusFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.TransactionStatus, func(res ebs_fields.EBSParserFields) interface{} {
		return fiber.Map{"ebs_response": res.OriginalTransaction}
	})
}

func (h *Handler) WorkingKey(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerWorkingKeyFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerWorkingKeyFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.WorkingKey, func(res ebs_fields.EBSParserFields) interface{} {
		return fiber.Map{"ebs_response": res, "fees": h.Service.NoebsConfig.EBSDynamicFees}
	})
}

func (h *Handler) CashIn(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerCashInFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerCashInFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.CashIn, nil)
}

func (h *Handler) CashOut(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerCashoOutFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerCashoOutFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.CashOut, nil)
}

func (h *Handler) AccountTransfer(c *fiber.Ctx) error {
	var req ebs_fields.ConsumrAccountTransferFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumrAccountTransferFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.AccountTransfer, nil)
}

func (h *Handler) IPinChange(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerIPinFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerIPinFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.IPinChange, nil)
}

func (h *Handler) Status(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerStatusFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerStatusFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.Status, nil)
}

func (h *Handler) QRMerchantRegistration(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerQRRegistration
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerQRRegistration) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.QRMerchantRegistration, nil)
}

func (h *Handler) QRPayment(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerQRPaymentFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerQRPaymentFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.QRPayment, nil)
}

func (h *Handler) QRRefund(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerQRRefundFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerQRRefundFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.QRRefund, nil)
}

func (h *Handler) QRComplete(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerQRCompleteFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerQRCompleteFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.QRComplete, nil)
}

func (h *Handler) QRTransactions(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerQRStatus
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerQRStatus) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.QRTransactions, nil)
}

func (h *Handler) EbsGetCardInfo(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerCardInfoFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerCardInfoFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.EbsGetCardInfo, nil)
}

func (h *Handler) RegisterCard(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerRegistrationFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerRegistrationFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.RegisterCard, nil)
}

func (h *Handler) CardTransfer(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerCardTransferAndMobileFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerCardTransferAndMobileFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
		r.DynamicFees = h.Service.NoebsConfig.EBSDynamicFees.CardTransferfees
	}, h.Service.CardTransfer, nil)
}

func (h *Handler) MobileTransfer(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerMobileTransferFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerMobileTransferFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
		r.DynamicFees = h.Service.NoebsConfig.EBSDynamicFees.CardTransferfees
	}, h.Service.MobileTransfer, nil)
}

func (h *Handler) GenerateVoucher(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerGenerateVoucherFields
	return handleConfiguredEBS(c, &req, func(r *ebs_fields.ConsumerGenerateVoucherFields) {
		r.ApplicationId = h.Service.NoebsConfig.ConsumerID
	}, h.Service.GenerateVoucher, nil)
}

func (h *Handler) IPINKey(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerGenerateIPINFields
	return handleEBS(c, &req, h.Service.IPINKey, nil)
}

func (h *Handler) GenerateIpin(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerGenerateIPin
	return handleEBS(c, &req, h.Service.GenerateIpin, nil)
}

func (h *Handler) CompleteIpin(c *fiber.Ctx) error {
	var req ebs_fields.ConsumerGenerateIPinCompletion
	return handleEBS(c, &req, h.Service.CompleteIpin, nil)
}
