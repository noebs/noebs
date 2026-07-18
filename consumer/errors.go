package consumer

import (
	"errors"
	"time"
)

var (
	// Auth / OTP
	ErrWrongPassword          = errors.New("wrong_password")
	ErrWrongOTP               = errors.New("wrong_otp")
	ErrPasswordInvalid        = errors.New("password_invalid")
	ErrUserNotVerified        = errors.New("user_not_verified")
	ErrEmptyOTP               = errors.New("empty_otp")
	ErrInvalidOTP             = errors.New("invalid_otp")
	ErrMissingAuth            = errors.New("missing_auth")
	ErrInvalidSignature       = errors.New("invalid_signature")
	ErrRateLimited            = errors.New("rate_limited")
	ErrMissingRequestSource   = errors.New("missing_request_source")
	ErrInvalidRequestSource   = errors.New("invalid_request_source")
	ErrMissingOTPSecret       = errors.New("missing_otp_secret")
	ErrRefreshExpired         = errors.New("refresh_expired")
	ErrRefreshReplay          = errors.New("refresh_replay")
	ErrRefreshTenantMismatch  = errors.New("refresh_tenant_mismatch")
	ErrSessionRevoked         = errors.New("session_revoked")
	ErrCheckUserBatchTooLarge = errors.New("check_user_batch_too_large")

	// Account recovery / balance step
	ErrCardNotMatched    = errors.New("card_not_matched")
	ErrCardNotFound      = errors.New("card_not_found")
	ErrTransactionFailed = errors.New("transaction_failed")

	// Payments / transfers
	ErrAmountMismatch             = errors.New("amount_mismatch")
	ErrReceiverHasNoCard          = errors.New("receiver has no card")
	ErrInvalidPaymentToken        = errors.New("invalid_payment_token")
	ErrAmbiguousPaymentToken      = errors.New("ambiguous_payment_token")
	ErrInvalidQuickPaymentRequest = errors.New("invalid_quick_payment_request")
	ErrPaymentOutcomeUnknown      = errors.New("payment_outcome_unknown")
	ErrInvalidPaymentInfo         = errors.New("invalid_payment_info")
	ErrMissingBillerID            = errors.New("missing_biller_id")
	ErrMissingHTTPClient          = errors.New("missing_http_client")
	ErrMissingCardVault           = errors.New("missing_card_vault_service_discovery")
	ErrInvalidCardVault           = errors.New("invalid_card_vault_service_discovery")
	ErrCardVaultCommand           = errors.New("card_vault_command_failed")
	ErrMissingIdentityAuth        = errors.New("missing_identity_auth_service_discovery")
	ErrInvalidIdentityAuth        = errors.New("invalid_identity_auth_service_discovery")
	ErrIdentityAuthCommand        = errors.New("identity_auth_command_failed")
	ErrMissingNotification        = errors.New("missing_notification_chat_service_discovery")
	ErrInvalidNotification        = errors.New("invalid_notification_chat_service_discovery")
	ErrNotificationCommand        = errors.New("notification_chat_command_failed")
	ErrInvalidBillerHookEndpoint  = errors.New("invalid_biller_hook_endpoint")
	ErrBillerHookPost             = errors.New("biller_hook_post_failed")
	ErrMissingMerchantID          = errors.New("missing merchant_id")
	ErrInvalidMerchantID          = errors.New("invalid merchant_id")
	ErrTransactionNotFound        = errors.New("transaction not found")

	// Registration
	ErrMissingMobile               = errors.New("missing mobile")
	ErrMissingPublicKey            = errors.New("missing public key")
	ErrInvalidPublicKey            = errors.New("invalid public key")
	ErrInvalidCard                 = errors.New("invalid card")
	ErrMissingPassword             = errors.New("missing password")
	ErrMissingIssuedPAN            = errors.New("missing_issued_pan")
	ErrMissingCardExpiry           = errors.New("missing_card_expiry")
	ErrUserAlreadyExists           = errors.New("user_already_exists")
	ErrMissingEnrollmentPublicKey  = errors.New("missing_enrollment_public_key")
	ErrInvalidEnrollmentPublicKey  = errors.New("invalid_enrollment_public_key")
	ErrMissingIPINBlock            = errors.New("missing_ipin_block")
	ErrInvalidIPINBlock            = errors.New("invalid_ipin_block")
	ErrEnrollmentRailUUIDMismatch  = errors.New("enrollment_rail_uuid_mismatch")
	ErrFundedOperationsUnavailable = errors.New("funded_operations_unavailable")
	ErrOperationRailUUIDMismatch   = errors.New("operation_rail_uuid_mismatch")
	ErrFundedOutcomeUnknown        = errors.New("funded_operation_outcome_unknown")
	ErrFundedRailRejected          = errors.New("funded_operation_rejected")
	ErrUnsafeBalanceResponse       = errors.New("unsafe_balance_response")
	ErrEnrollmentOutcomeUnknown    = errors.New("enrollment_outcome_unknown")
	ErrUpgradeRequired             = errors.New("upgrade_required")
)

type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return ErrRateLimited.Error()
}

func (e *RateLimitError) Unwrap() error {
	return ErrRateLimited
}
