package handler

import "github.com/gofiber/fiber/v2"

func RegisterPublicRoutes(router fiber.Router, h *Handler) {
	if router == nil || h == nil {
		return
	}

	// EBS operations (public, matches legacy behavior)
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

	// Cards / vouchers (public)
	router.Post("/card_info", h.EbsGetCardInfo)
	router.Post("/pan_from_mobile", h.GetMSISDNFromCard)
	router.Post("/vouchers/generate", h.GenerateVoucher)
	router.Post("/cards/new", h.RegisterCard)
	router.Post("/cards/complete", h.CompleteRegistration)
	router.Get("/nec2name", h.NecToName)
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

func RegisterAuthedRoutes(router fiber.Router, h *Handler) {
	if router == nil || h == nil {
		return
	}

	// Cards
	router.Get("/get_cards", h.GetCards)
	router.Post("/add_card", h.AddCards)
	router.Put("/edit_card", h.EditCard)
	router.Delete("/delete_card", h.RemoveCard)
	router.Post("/cards/set_main", h.SetMainCard)
	router.Get("/users/cards", h.CardsByMobile)
	router.Get("/mobile2pan", h.CardFromNumber)

	// Transactions / payment compatibility
	router.Get("/transaction", h.TransactionByUUID)
	router.Get("/transactions", h.GetTransactions)
	router.Post("/p2p_mobile", h.MobileTransfer)

	// Beneficiaries
	router.Post("/beneficiary", h.CreateBeneficiary)
	router.Get("/beneficiary", h.ListBeneficiaries)
	router.Delete("/beneficiary", h.DeleteBeneficiary)

	// Payment tokens
	router.Get("/payment_token", h.GetPaymentToken)
	router.Post("/payment_token", h.GeneratePaymentToken)
	router.Post("/payment_request", h.PaymentRequest)
	router.Post("/payment_token/quick_pay", h.NoebsQuickPayment)
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
