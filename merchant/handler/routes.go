package handler

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, h *Handler, middleware ...fiber.Handler) {
	post := func(path string, handler fiber.Handler) {
		handlers := make([]fiber.Handler, 0, len(middleware)+1)
		handlers = append(handlers, middleware...)
		handlers = append(handlers, handler)
		router.Post(path, handlers...)
	}

	// EBS passthrough (merchant)
	post("/ebs/*", h.EBS)

	// Primary merchant endpoints
	post("/workingKey", h.WorkingKey)
	post("/cardTransfer", h.CardTransfer)
	post("/voucher", h.GenerateVoucher)
	post("/voucher/cash_in", h.VoucherCashIn)
	post("/cashout", h.VoucherCashOut)
	post("/voucher/cash_out", h.VoucherCashOut)
	post("/purchase", h.Purchase)
	post("/cashIn", h.CashIn)
	post("/cashOut", h.CashOut)
	post("/billInquiry", h.BillInquiry)
	post("/billPayment", h.BillPayment)
	post("/bills", h.TopUpPayment)
	post("/changePin", h.ChangePIN)
	post("/miniStatement", h.MiniStatement)
	post("/isAlive", h.IsAlive)
	post("/balance", h.Balance)
	post("/refund", h.Refund)
	post("/toAccount", h.ToAccount)
	post("/statement", h.Statement)
}
