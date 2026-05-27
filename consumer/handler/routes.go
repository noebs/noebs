package handler

import "github.com/gofiber/fiber/v2"

func RegisterEBSAdapterPublicRoutes(router fiber.Router, h *Handler) {
	// EBS operations (public, matches legacy behavior)
	router.Post("/card_info", h.EbsGetCardInfo)
	router.Post("/pan_from_mobile", h.GetMSISDNFromCard)
	router.Post("/cards/new", h.RegisterCard)
	router.Get("/nec2name", h.NecToName)
	router.Post("/balance", h.Balance)
	router.Post("/status", h.TransactionStatus)
	router.Post("/is_alive", h.IsAlive)
	router.Post("/bill_payment", h.BillPayment)
	router.Post("/bills", h.GetBills)
	router.Get("/guess_biller", h.GetBiller)
	router.Post("/bill_inquiry", h.BillInquiry)
	router.Post("/p2p", h.CardTransfer)
	router.Post("/cashIn", h.CashIn)
	router.Post("/cashOut", h.CashOut)
	router.Post("/account", h.AccountTransfer)
	router.Post("/purchase", h.Purchase)
	router.Post("/n/status", h.Status)
	router.Post("/key", h.WorkingKey)
	router.Post("/ipin", h.IPinChange)

	// QR (public)
	router.Post("/generate_qr", h.QRMerchantRegistration)
	router.Post("/qr_payment", h.QRPayment)
	router.Post("/qr_status", h.QRTransactions)
	router.Post("/qr_refund", h.QRRefund)
	router.Post("/qr_complete", h.QRComplete)

	// IPIN (public)
	router.Post("/ipin_key", h.IPINKey)
	router.Post("/generate_ipin", h.GenerateIpin)
	router.Post("/complete_ipin", h.CompleteIpin)

	// Vouchers (public)
	router.Post("/vouchers/generate", h.GenerateVoucher)
}

func RegisterCardVaultPublicRoutes(router fiber.Router, h *Handler) {
	// Registration completion persists local identity/card state; split its EBS step next.
	router.Post("/cards/complete", h.CompleteRegistration)
}

func RegisterIdentityPublicRoutes(router fiber.Router, h *Handler) {
	// Registration / auth (public)
	router.Post("/register", h.CreateUser)
	router.Post("/register_with_card", h.RegisterWithCard)
	router.Post("/login", h.LoginHandler)
	router.Post("/refresh", h.RefreshHandler)

	// OTP (public)
	router.Post("/otp/generate", h.GenerateSignInCode)
	router.Post("/otp/generate_insecure", h.GenerateSignInCodeInsecure)
	router.Post("/otp/login", h.SingleLoginHandler)
	router.Post("/otp/verify", h.VerifyOTP)
	router.Post("/otp/balance", h.BalanceStep)

	// Social auth (public)
	router.Post("/auth/google", h.GoogleAuth)

	// User identity checks
	router.Post("/check_user", h.CheckUser)
	router.Post("/kyc", h.KYC)
}

func RegisterBeneficiaryRoutes(router fiber.Router, h *Handler) {
	router.Post("/beneficiary", h.CreateBeneficiary)
	router.Get("/beneficiary", h.ListBeneficiaries)
	router.Delete("/beneficiary", h.DeleteBeneficiary)
}

func RegisterEBSAdapterAuthedRoutes(router fiber.Router, h *Handler) {
	// Transactions / payment compatibility
	router.Get("/transaction", h.TransactionByUUID)
	router.Get("/transactions", h.GetTransactions)
	router.Post("/p2p_mobile", h.MobileTransfer)
	router.Post("/payment_token/quick_pay", h.NoebsQuickPayment)
}

func RegisterCardVaultAuthedRoutes(router fiber.Router, h *Handler) {
	// Cards
	router.Get("/get_cards", h.GetCards)
	router.Post("/add_card", h.AddCards)
	router.Put("/edit_card", h.EditCard)
	router.Delete("/delete_card", h.RemoveCard)
	router.Post("/cards/set_main", h.SetMainCard)
	router.Get("/users/cards", h.CardsByMobile)
	router.Get("/mobile2pan", h.CardFromNumber)

	// Payment tokens
	router.Get("/payment_token", h.GetPaymentToken)
	router.Post("/payment_token", h.GeneratePaymentToken)
	router.Post("/payment_request", h.PaymentRequest)
}

func RegisterCardVaultInternalRoutes(router fiber.Router, h *Handler) {
	router.Post("/quick-pay/resolve", h.ResolveQuickPaymentToken)
	router.Post("/quick-pay/mark-paid", h.MarkQuickPaymentTokenPaid)
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
}

func RegisterNotificationRoutes(router fiber.Router, h *Handler) {
	router.Get("/notifications", h.Notifications)
}
