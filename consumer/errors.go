package consumer

import (
	"errors"
)

var (
	ErrMissingUUID = errors.New("missing uuid")

	// Account recovery / balance step
	ErrCardNotFound = errors.New("card_not_found")

	// Payments / transfers
	ErrInvalidPaymentInfo  = errors.New("invalid_payment_info")
	ErrMissingBillerID     = errors.New("missing_biller_id")
	ErrMissingHTTPClient   = errors.New("missing_http_client")
	ErrMissingCardVault    = errors.New("missing_card_vault_service_discovery")
	ErrInvalidCardVault    = errors.New("invalid_card_vault_service_discovery")
	ErrCardVaultCommand    = errors.New("card_vault_command_failed")
	ErrMissingNotification = errors.New("missing_notification_chat_service_discovery")
	ErrInvalidNotification = errors.New("invalid_notification_chat_service_discovery")
	ErrNotificationCommand = errors.New("notification_chat_command_failed")
	ErrMissingMerchantID   = errors.New("missing merchant_id")
	ErrInvalidMerchantID   = errors.New("invalid merchant_id")
	ErrTransactionNotFound = errors.New("transaction not found")

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
)
