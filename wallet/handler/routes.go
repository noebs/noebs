package handler

import "github.com/gofiber/fiber/v2"

func RegisterUserRoutes(router fiber.Router, handler *UserHandler) {
	if router == nil || handler == nil {
		return
	}
	router.Get("/methods", handler.ListPaymentMethods)
	router.Post("/wallets", handler.EnsureWallet)
	router.Get("/wallets/:id/transactions", handler.ListWalletTransactions)
	router.Get("/wallets/:id", handler.GetWallet)
}

func RegisterAdminRoutes(router fiber.Router, handler *AdminHandler) {
	if router == nil || handler == nil {
		return
	}
	router.Get("/", handler.Dashboard)
	router.Get("/wallets", handler.ListWallets)
	router.Get("/wallets/:id", handler.WalletDetail)
	router.Get("/transactions", handler.Transactions)
	router.Get("/transactions/:client_reference", handler.TransactionDetail)
	router.Get("/pending", handler.PendingApprovals)
	router.Get("/manual", handler.ManualTransfers)
	router.Post("/manual", handler.SubmitManualTransfer)
	router.Get("/manual/:workflow_id", handler.ManualTransferDetail)
	router.Get("/fees", handler.Fees)
	router.Post("/fees", handler.CreateFeeConfig)
	router.Get("/rates", handler.Rates)
	router.Post("/rates", handler.CreateRate)
	router.Post("/approve/:workflow_id", handler.ApproveTransfer)
	router.Post("/reject/:workflow_id", handler.RejectTransfer)
	router.Get("/audit", handler.AuditLog)
}

func RegisterWebhookRoutes(router fiber.Router, handler *PSPWebhookHandler) {
	if router == nil || handler == nil {
		return
	}
	router.Post("/psp/webhooks/:provider", handler.Handle)
}
