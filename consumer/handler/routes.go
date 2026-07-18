package handler

import "github.com/gofiber/fiber/v2"

func RegisterEBSAdapterPublicRoutes(router fiber.Router, h *Handler) {
	// Registration, recovery, and cryptographic bootstrap are the only EBS
	// adapter operations available before a user has authenticated.
	router.Post("/otp/balance", h.BalanceStep)
	router.Post("/register_with_card", h.RegisterWithCard)
	router.Post("/card_info", h.EbsGetCardInfo)
	router.Post("/cards/new", h.RegisterCard)
	router.Post("/cards/complete", h.CompleteRegistration)
	router.Post("/key", h.WorkingKey)
	router.Post("/ipin_key", h.IPINKey)
}

func RegisterEBSAdapterAuthedRoutes(router fiber.Router, h *Handler) {
	// Card and account operations
	router.Post("/balance", h.Balance)
	router.Post("/status", h.TransactionStatus)
	router.Post("/is_alive", h.IsAlive)
	router.Post("/bill_payment", h.BillPayment)
	router.Post("/bills", h.GetBills)
	router.Get("/biller", h.GetBiller)
	router.Post("/bill_inquiry", h.BillInquiry)
	router.Post("/p2p", h.CardTransfer)
	router.Post("/cashIn", h.CashIn)
	router.Post("/cashOut", h.CashOut)
	router.Post("/account", h.AccountTransfer)
	router.Post("/purchase", h.Purchase)
	router.Post("/n/status", h.Status)
	router.Post("/ipin", h.IPinChange)
	router.Get("/nec2name", h.NecToName)

	// QR
	router.Post("/generate_qr", h.QRMerchantRegistration)
	router.Post("/qr_payment", h.QRPayment)
	router.Post("/qr_status", h.QRTransactions)
	router.Post("/qr_refund", h.QRRefund)
	router.Post("/qr_complete", h.QRComplete)

	// IPIN
	router.Post("/generate_ipin", h.GenerateIpin)
	router.Post("/complete_ipin", h.CompleteIpin)

	// Vouchers
	router.Post("/vouchers/generate", h.GenerateVoucher)

	// Transactions / payment compatibility
	router.Get("/transaction", h.TransactionByUUID)
	router.Get("/transactions", h.GetTransactions)
	router.Post("/p2p_mobile", h.MobileTransfer)
	router.Post("/payment_token/quick_pay", h.NoebsQuickPayment)
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
