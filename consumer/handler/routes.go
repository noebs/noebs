package handler

import "github.com/gofiber/fiber/v2"

func RegisterEBSAdapterAuthedRoutes(router fiber.Router, h *Handler) {
	// Opaque card enrollment is verified by the EBS adapter and persisted only
	// through authenticated Card Vault commands.
	router.Post("/cards/enrollment-intents", h.CreateOpaqueCardEnrollmentIntent)
	router.Post("/cards/enrollment-intents/:enrollment_id/confirm", h.ConfirmOpaqueCardEnrollment)

	// Card and account operations
	router.Post("/balance", authenticatedEBS(h.OpaqueBalance))
	router.Post("/status", authenticatedEBS(h.TransactionStatus))
	router.Post("/is_alive", authenticatedEBS(h.IsAlive))
	router.Get("/biller", authenticatedEBS(h.GetBiller))
	router.Post("/n/status", authenticatedEBS(h.Status))
	router.Get("/nec2name", authenticatedEBS(h.NecToName))

	// QR
	router.Post("/generate_qr", authenticatedEBS(h.QRMerchantRegistration))
	router.Post("/qr_status", authenticatedEBS(h.QRTransactions))
	router.Post("/qr_refund", authenticatedEBS(h.QRRefund))
	router.Post("/qr_complete", authenticatedEBS(h.QRComplete))

	// Transactions
	router.Get("/transaction", authenticatedEBS(h.TransactionByUUID))
	router.Get("/transactions", authenticatedEBS(h.GetTransactions))
}

func RegisterCardVaultAuthedRoutes(router fiber.Router, h *Handler) {
	// Opaque card contract.
	router.Get("/cards", h.ListOpaqueCards)
	router.Patch("/cards/:card_id", h.RenameOpaqueCard)
	router.Delete("/cards/:card_id", h.RetireOpaqueCard)
	router.Put("/cards/:card_id/main", h.SetOpaqueMainCard)
}

func RegisterCardVaultInternalRoutes(router fiber.Router, h *Handler) {
	router.Post("/enrollment-intents", h.CreateCardEnrollmentIntentInternal)
	router.Post("/enrollment-intents/begin", h.BeginCardEnrollmentInternal)
	router.Post("/enrollment-intents/claim-rail", h.ClaimCardEnrollmentRailInternal)
	router.Post("/enrollment-intents/complete", h.CompleteCardEnrollmentInternal)
	router.Post("/enrollment-intents/fail", h.FailCardEnrollmentInternal)
}

func RegisterCardVaultAdminInternalRoutes(router fiber.Router, h *Handler) {
	router.Post("/funded-operations/claim", h.ClaimFundedCardOperationInternal)
}

func RegisterIdentityInternalRoutes(router fiber.Router, h *Handler) {
	router.Post("/principals/resolve", h.ResolveProfileProjection)
}

// RegisterIdentityPrincipalRoutes contains bootstrap operations that have a
// verified OIDC principal but deliberately do not yet have a numeric profile ID.
func RegisterIdentityPrincipalRoutes(router fiber.Router, h *Handler) {
	router.Post("/auth/profile", h.CreateProfileProjection)
}

func RegisterIdentityAuthedRoutes(router fiber.Router, h *Handler) {
	// User profile
	router.Get("/user", h.GetUser)
	router.Put("/user", h.UpdateUser)
	router.Get("/user/lang", h.GetUserLanguage)
	router.Put("/user/lang", h.SetUserLanguage)
	router.Post("/user/device", h.AddDeviceToken)
	router.Post("/kyc", h.KYC)
}

func RegisterNotificationAdminInternalRoutes(router fiber.Router, h *Handler) {
	router.Post("/push-data", h.StoreNotificationPushData)
}
