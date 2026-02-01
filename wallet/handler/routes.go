package handler

import "github.com/gofiber/fiber/v2"

func RegisterUserRoutes(router fiber.Router, handler *UserHandler) {
	if router == nil || handler == nil {
		return
	}
	router.Post("/wallets", handler.EnsureWallet)
	router.Get("/wallets/:id", handler.GetWallet)
}

func RegisterAdminRoutes(router fiber.Router, handler *AdminHandler) {
	if router == nil || handler == nil {
		return
	}
	router.Get("/", handler.Dashboard)
	router.Get("/wallets", handler.ListWallets)
}
