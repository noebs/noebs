package consumer

import "errors"

var (
	// Auth / OTP
	ErrWrongPassword      = errors.New("wrong_password")
	ErrWrongOTP           = errors.New("wrong_otp")
	ErrPasswordInvalid    = errors.New("password_invalid")
	ErrEmptyOTP           = errors.New("empty_otp")
	ErrInvalidOTP         = errors.New("invalid_otp")
	ErrMissingAuth        = errors.New("missing_auth")
	ErrInvalidRecoveryJWT = errors.New("invalid_recovery_jwt")

	// Account recovery / balance step
	ErrCardNotMatched    = errors.New("card_not_matched")
	ErrTransactionFailed = errors.New("transaction_failed")

	// Payments / transfers
	ErrAmountMismatch            = errors.New("amount_mismatch")
	ErrReceiverHasNoCard         = errors.New("receiver has no card")
	ErrInvalidPaymentToken       = errors.New("invalid_payment_token")
	ErrAmbiguousPaymentToken     = errors.New("ambiguous_payment_token")
	ErrInvalidPaymentInfo        = errors.New("invalid_payment_info")
	ErrMissingBillerID           = errors.New("missing_biller_id")
	ErrMissingHTTPClient         = errors.New("missing_http_client")
	ErrMissingCardVault          = errors.New("missing_card_vault_service_discovery")
	ErrInvalidCardVault          = errors.New("invalid_card_vault_service_discovery")
	ErrCardVaultCommand          = errors.New("card_vault_command_failed")
	ErrMissingIdentityAuth       = errors.New("missing_identity_auth_service_discovery")
	ErrInvalidIdentityAuth       = errors.New("invalid_identity_auth_service_discovery")
	ErrIdentityAuthCommand       = errors.New("identity_auth_command_failed")
	ErrMissingNotification       = errors.New("missing_notification_chat_service_discovery")
	ErrInvalidNotification       = errors.New("invalid_notification_chat_service_discovery")
	ErrNotificationCommand       = errors.New("notification_chat_command_failed")
	ErrInvalidBillerHookEndpoint = errors.New("invalid_biller_hook_endpoint")
	ErrBillerHookPost            = errors.New("biller_hook_post_failed")
	ErrMissingAdminReporting     = errors.New("missing_admin_reporting_service_discovery")
	ErrInvalidAdminReporting     = errors.New("invalid_admin_reporting_service_discovery")
	ErrAdminReportingCommand     = errors.New("admin_reporting_command_failed")

	// Registration
	ErrMissingMobile     = errors.New("missing mobile")
	ErrMissingPublicKey  = errors.New("missing public key")
	ErrInvalidCard       = errors.New("invalid card")
	ErrMissingPassword   = errors.New("missing password")
	ErrMissingIssuedPAN  = errors.New("missing_issued_pan")
	ErrMissingCardExpiry = errors.New("missing_card_expiry")
	ErrUserAlreadyExists = errors.New("user_already_exists")
)
