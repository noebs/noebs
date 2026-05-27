package consumer

import "errors"

var (
	// Auth / OTP
	ErrWrongPassword   = errors.New("wrong_password")
	ErrWrongOTP        = errors.New("wrong_otp")
	ErrPasswordInvalid = errors.New("password_invalid")
	ErrEmptyOTP        = errors.New("empty_otp")
	ErrInvalidOTP      = errors.New("invalid_otp")

	// Account recovery / balance step
	ErrCardNotMatched    = errors.New("card_not_matched")
	ErrTransactionFailed = errors.New("transaction_failed")

	// Payments / transfers
	ErrAmountMismatch        = errors.New("amount_mismatch")
	ErrReceiverHasNoCard     = errors.New("receiver has no card")
	ErrInvalidPaymentToken   = errors.New("invalid_payment_token")
	ErrAmbiguousPaymentToken = errors.New("ambiguous_payment_token")
	ErrMissingHTTPClient     = errors.New("missing_http_client")
	ErrMissingCardVault      = errors.New("missing_card_vault_service_discovery")
	ErrInvalidCardVault      = errors.New("invalid_card_vault_service_discovery")
	ErrCardVaultCommand      = errors.New("card_vault_command_failed")

	// Registration
	ErrMissingPassword = errors.New("missing password")
)
