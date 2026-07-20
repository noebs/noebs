package transactionauth

import "errors"

var (
	ErrInvalidConfiguration  = errors.New("invalid transaction authorization configuration")
	ErrInvalidInput          = errors.New("invalid transaction authorization input")
	ErrMissingTenantID       = errors.New("missing transaction authorization tenant id")
	ErrInvalidTenantID       = errors.New("invalid transaction authorization tenant id")
	ErrMissingIssuer         = errors.New("missing transaction authorization issuer")
	ErrInvalidIssuer         = errors.New("invalid transaction authorization issuer")
	ErrMissingSubject        = errors.New("missing transaction authorization subject")
	ErrInvalidSubject        = errors.New("invalid transaction authorization subject")
	ErrInvalidOperation      = errors.New("invalid transaction authorization operation")
	ErrMissingRequestDigest  = errors.New("missing transaction authorization request digest")
	ErrMissingIdempotencyKey = errors.New("missing transaction authorization idempotency key")
	ErrInvalidIdempotencyKey = errors.New("invalid transaction authorization idempotency key")
	ErrEntropyUnavailable    = errors.New("transaction authorization entropy unavailable")
	ErrEncryption            = errors.New("transaction authorization encryption failed")
	ErrUnknownKey            = errors.New("unknown transaction authorization encryption key")
	ErrIntentConflict        = errors.New("transaction authorization intent already exists")
	ErrIntentNotFound        = errors.New("transaction authorization intent not found")
	ErrInvalidBrowserStart   = errors.New("invalid or expired transaction authorization browser start")
	ErrInvalidFlow           = errors.New("invalid or expired transaction authorization flow")
	ErrAuthorizationDenied   = errors.New("transaction authorization denied")
	ErrOAuthExchange         = errors.New("transaction authorization OAuth exchange failed")
	ErrInvalidIDToken        = errors.New("invalid transaction authorization ID token")
	ErrStoreUnavailable      = errors.New("transaction authorization store unavailable")
)
