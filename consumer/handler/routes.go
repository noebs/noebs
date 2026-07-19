package handler

import "github.com/gofiber/fiber/v2"

func RegisterEBSAdapterPublicRoutes(router fiber.Router, h *Handler) {
	// PAN-selected recovery/registration is terminal after the opaque cutover.
	router.Post("/otp/balance", h.LegacyCardUpgradeRequired)
	router.Post("/register_with_card", h.LegacyCardUpgradeRequired)
	router.Post("/card_info", h.LegacyCardUpgradeRequired)
	router.Post("/cards/new", h.LegacyCardUpgradeRequired)
	router.Post("/cards/complete", h.LegacyCardUpgradeRequired)
	router.Post("/key", publicEBS(h.WorkingKey))
	router.Post("/ipin_key", publicEBS(h.IPINKey))
}

func RegisterEBSAdapterAuthedRoutes(router fiber.Router, h *Handler) {
	// Opaque card enrollment is verified by the EBS adapter and persisted only
	// through authenticated Card Vault commands.
	router.Post("/cards/enrollment-intents", h.CreateOpaqueCardEnrollmentIntent)
	router.Post("/cards/enrollment-intents/:enrollment_id/confirm", h.ConfirmOpaqueCardEnrollment)

	// Card and account operations
	router.Post("/balance", authenticatedEBS(h.OpaqueBalance))
	router.Post("/status", authenticatedEBS(h.TransactionStatus))
	router.Post("/is_alive", authenticatedEBS(h.IsAlive))
	router.Post("/bill_payment", h.LegacyCardUpgradeRequired)
	router.Post("/bills", h.LegacyCardUpgradeRequired)
	router.Get("/biller", authenticatedEBS(h.GetBiller))
	router.Post("/bill_inquiry", h.LegacyCardUpgradeRequired)
	router.Post("/p2p", h.LegacyCardUpgradeRequired)
	router.Post("/cashIn", h.LegacyCardUpgradeRequired)
	router.Post("/cashOut", h.LegacyCardUpgradeRequired)
	router.Post("/account", h.LegacyCardUpgradeRequired)
	router.Post("/purchase", h.LegacyCardUpgradeRequired)
	router.Post("/n/status", authenticatedEBS(h.Status))
	router.Post("/ipin", h.LegacyCardUpgradeRequired)
	router.Get("/nec2name", authenticatedEBS(h.NecToName))

	// QR
	router.Post("/generate_qr", authenticatedEBS(h.QRMerchantRegistration))
	router.Post("/qr_payment", h.LegacyCardUpgradeRequired)
	router.Post("/qr_status", authenticatedEBS(h.QRTransactions))
	router.Post("/qr_refund", authenticatedEBS(h.QRRefund))
	router.Post("/qr_complete", authenticatedEBS(h.QRComplete))

	// IPIN
	router.Post("/generate_ipin", h.LegacyCardUpgradeRequired)
	router.Post("/complete_ipin", h.LegacyCardUpgradeRequired)

	// Vouchers
	router.Post("/vouchers/generate", h.LegacyCardUpgradeRequired)

	// Transactions / payment compatibility
	router.Get("/transaction", authenticatedEBS(h.TransactionByUUID))
	router.Get("/transactions", authenticatedEBS(h.GetTransactions))
	router.Post("/p2p_mobile", h.LegacyCardUpgradeRequired)
	router.Post("/payment_token/quick_pay", h.LegacyCardUpgradeRequired)
}

func RegisterBeneficiaryRoutes(router fiber.Router, h *Handler) {
	router.Post("/beneficiary", h.RetiredBeneficiaryContract)
	router.Get("/beneficiary", h.RetiredBeneficiaryContract)
	router.Delete("/beneficiary", h.RetiredBeneficiaryContract)
}

func RegisterCardVaultAuthedRoutes(router fiber.Router, h *Handler) {
	// Opaque card contract.
	router.Get("/cards", h.ListOpaqueCards)
	router.Patch("/cards/:card_id", h.RenameOpaqueCard)
	router.Delete("/cards/:card_id", h.RetireOpaqueCard)
	router.Put("/cards/:card_id/main", h.SetOpaqueMainCard)

	// Legacy PAN selectors are intentionally terminal. They do not bind or
	// inspect request bodies, so a new client can never fall back through them.
	router.Get("/get_cards", h.LegacyCardUpgradeRequired)
	router.Post("/add_card", h.LegacyCardUpgradeRequired)
	router.Put("/edit_card", h.LegacyCardUpgradeRequired)
	router.Delete("/delete_card", h.LegacyCardUpgradeRequired)
	router.Post("/cards/set_main", h.LegacyCardUpgradeRequired)

	// PAN-backed payment tokens are unavailable until the opaque token contract.
	router.Get("/payment_token", h.LegacyCardUpgradeRequired)
	router.Post("/payment_token", h.LegacyCardUpgradeRequired)
}

func RegisterCardVaultInternalRoutes(router fiber.Router, h *Handler) {
	router.Post("/cards/masked", h.ListMaskedCards)
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
	router.Post("/users/by-mobile", h.ResolveIdentityUserByMobile)
	router.Post("/users/resolve-batch", h.ResolveIdentityUsersBatch)
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
	router.Post("/check_user", h.CheckUser)
	router.Post("/kyc", h.KYC)
}

func RegisterNotificationRoutes(router fiber.Router, h *Handler) {
	router.Get("/notifications", h.Notifications)
}

func RegisterNotificationAdminInternalRoutes(router fiber.Router, h *Handler) {
	router.Post("/push-data", h.StoreNotificationPushData)
	router.Post("/biller-hook", h.SubmitBillerHook)
}
