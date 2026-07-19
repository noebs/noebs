package consumer

import (
	"errors"
)

var (
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
	ErrInvalidCard                 = errors.New("invalid card")
	ErrMissingIssuedPAN            = errors.New("missing_issued_pan")
	ErrMissingCardExpiry           = errors.New("missing_card_expiry")
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
