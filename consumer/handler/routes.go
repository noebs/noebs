package handler

import "github.com/gofiber/fiber/v2"

func RegisterEBSAdapterPublicRoutes(router fiber.Router, h *Handler) {
	// Registration, recovery, and cryptographic bootstrap are the only EBS
	// adapter operations available before a user has authenticated.
	router.Post("/otp/balance", publicEBS(h.BalanceStep))
	router.Post("/register_with_card", publicEBS(h.RegisterWithCard))
	router.Post("/card_info", publicEBS(h.EbsGetCardInfo))
	router.Post("/cards/new", publicEBS(h.RegisterCard))
	router.Post("/cards/complete", publicEBS(h.CompleteRegistration))
	router.Post("/key", publicEBS(h.WorkingKey))
	router.Post("/ipin_key", publicEBS(h.IPINKey))
}

func RegisterEBSAdapterAuthedRoutes(router fiber.Router, h *Handler) {
	// Card and account operations
	router.Post("/balance", authenticatedEBS(h.Balance))
	router.Post("/status", authenticatedEBS(h.TransactionStatus))
	router.Post("/is_alive", authenticatedEBS(h.IsAlive))
	router.Post("/bill_payment", authenticatedEBS(h.BillPayment))
	router.Post("/bills", authenticatedEBS(h.GetBills))
	router.Get("/biller", authenticatedEBS(h.GetBiller))
	router.Post("/bill_inquiry", authenticatedEBS(h.BillInquiry))
	router.Post("/p2p", authenticatedEBS(h.CardTransfer))
	router.Post("/cashIn", authenticatedEBS(h.CashIn))
	router.Post("/cashOut", authenticatedEBS(h.CashOut))
	router.Post("/account", authenticatedEBS(h.AccountTransfer))
	router.Post("/purchase", authenticatedEBS(h.Purchase))
	router.Post("/n/status", authenticatedEBS(h.Status))
	router.Post("/ipin", authenticatedEBS(h.IPinChange))
	router.Get("/nec2name", authenticatedEBS(h.NecToName))

	// QR
	router.Post("/generate_qr", authenticatedEBS(h.QRMerchantRegistration))
	router.Post("/qr_payment", authenticatedEBS(h.QRPayment))
	router.Post("/qr_status", authenticatedEBS(h.QRTransactions))
	router.Post("/qr_refund", authenticatedEBS(h.QRRefund))
	router.Post("/qr_complete", authenticatedEBS(h.QRComplete))

	// IPIN
	router.Post("/generate_ipin", authenticatedEBS(h.GenerateIpin))
	router.Post("/complete_ipin", authenticatedEBS(h.CompleteIpin))

	// Vouchers
	router.Post("/vouchers/generate", authenticatedEBS(h.GenerateVoucher))

	// Transactions / payment compatibility
	router.Get("/transaction", authenticatedEBS(h.TransactionByUUID))
	router.Get("/transactions", authenticatedEBS(h.GetTransactions))
	router.Post("/p2p_mobile", authenticatedEBS(h.MobileTransfer))
	router.Post("/payment_token/quick_pay", authenticatedEBS(h.NoebsQuickPayment))
}

func RegisterIdentityPublicRoutes(router fiber.Router, h *Handler) {
	// Registration / auth (public)
	router.Post("/register", h.CreateUser)
	router.Post("/login", h.LoginHandler)
	router.Post("/refresh", h.RefreshHandler)

	// OTP (public)
	router.Post("/otp/generate", h.GenerateSignInCode)
	router.Post("/otp/login", h.SingleLoginHandler)
	router.Post("/otp/verify", h.VerifyOTP)
	router.Post("/recovery/request", h.RequestPasswordRecovery)
	router.Post("/recovery/verify", h.VerifyPasswordRecovery)
	router.Post("/recovery/reset", h.ResetPasswordWithRecovery)

	// Social auth (public)
	router.Post("/auth/google", h.GoogleAuth)

}

func RegisterBeneficiaryRoutes(router fiber.Router, h *Handler) {
	router.Post("/beneficiary", h.CreateBeneficiary)
	router.Get("/beneficiary", h.ListBeneficiaries)
	router.Delete("/beneficiary", h.DeleteBeneficiary)
}

func RegisterCardVaultAuthedRoutes(router fiber.Router, h *Handler) {
	// Cards
	router.Get("/get_cards", h.GetCards)
	router.Post("/add_card", h.AddCards)
	router.Put("/edit_card", h.EditCard)
	router.Delete("/delete_card", h.RemoveCard)
	router.Post("/cards/set_main", h.SetMainCard)

	// Payment tokens
	router.Get("/payment_token", h.GetPaymentToken)
	router.Post("/payment_token", h.GeneratePaymentToken)
}

func RegisterCardVaultInternalRoutes(router fiber.Router, h *Handler) {
	router.Post("/quick-pay/resolve", h.ResolveQuickPaymentToken)
	router.Post("/quick-pay/finalize", h.FinalizeQuickPaymentToken)
	router.Post("/cards/masked", h.ListMaskedCards)
}

func RegisterCardVaultAdminInternalRoutes(router fiber.Router, h *Handler) {
	router.Post("/card-registration/cards", h.StoreCompletedRegistrationCard)
	router.Post("/cards/by-mobile", h.ResolveCardByMobile)
	router.Post("/cards/by-mobile-pan", h.ResolveCardByMobilePAN)
	router.Post("/cards/masked-by-mobile", h.ResolveMaskedCardByMobile)
}

func RegisterIdentityInternalRoutes(router fiber.Router, h *Handler) {
	router.Post("/card-registration/users", h.CreateCompletedRegistrationIdentity)
	router.Post("/register-with-card/users", h.RegisterWithCardIdentity)
	router.Post("/recovery-credential", h.IssueRecoveryCredential)
	router.Post("/sessions/validate", h.ValidateSession)
	router.Post("/users/by-mobile", h.ResolveIdentityUserByMobile)
}

func RegisterIdentityAuthedRoutes(router fiber.Router, h *Handler) {
	// Authenticated auth/profile endpoints
	router.Post("/auth/complete_profile", h.CompleteProfile)
	router.Get("/auth/me", h.AuthMe)

	// User profile
	router.Get("/user", h.GetUser)
	router.Put("/user", h.UpdateUser)
	router.Get("/user/lang", h.GetUserLanguage)
	router.Put("/user/lang", h.SetUserLanguage)
	router.Post("/user/device", h.AddDeviceToken)
	router.Post("/change_password", h.ChangePassword)
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
