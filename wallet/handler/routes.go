package handler

import "github.com/gofiber/fiber/v2"

func RegisterUserRoutes(router fiber.Router, handler *UserHandler) {
	router.Get("/methods", handler.ListPaymentMethods)
	router.Post("/wallets", handler.EnsureWallet)
	router.Get("/wallets/:id/transactions", handler.ListWalletTransactions)
	router.Get("/wallets/:id", handler.GetWallet)
}

func RegisterWebhookRoutes(router fiber.Router, handler *PSPWebhookHandler) {
	router.Post("/psp/webhooks/:provider", handler.Handle)
}
