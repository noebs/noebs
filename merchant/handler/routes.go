package handler

import "github.com/gofiber/fiber/v2"

func RegisterRoutes(router fiber.Router, h *Handler) {
	// EBS passthrough (merchant)
	router.Post("/ebs/*", h.EBS)

	// Primary merchant endpoints
	router.Post("/workingKey", h.WorkingKey)
	router.Post("/cardTransfer", h.CardTransfer)
	router.Post("/voucher", h.GenerateVoucher)
	router.Post("/voucher/cash_in", h.VoucherCashIn)
	router.Post("/cashout", h.VoucherCashOut)
	router.Post("/voucher/cash_out", h.VoucherCashOut)
	router.Post("/purchase", h.Purchase)
	router.Post("/cashIn", h.CashIn)
	router.Post("/cashOut", h.CashOut)
	router.Post("/billInquiry", h.BillInquiry)
	router.Post("/billPayment", h.BillPayment)
	router.Post("/bills", h.TopUpPayment)
	router.Post("/changePin", h.ChangePIN)
	router.Post("/miniStatement", h.MiniStatement)
	router.Post("/isAlive", h.IsAlive)
	router.Post("/balance", h.Balance)
	router.Post("/refund", h.Refund)
	router.Post("/toAccount", h.ToAccount)
	router.Post("/statement", h.Statement)

	// Manual test route (legacy)
	router.Get("/wrk", h.IsAliveWrk)
}
