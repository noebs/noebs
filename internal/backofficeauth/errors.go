package backofficeauth

import "errors"

var (
	ErrInvalidConfiguration = errors.New("invalid back-office authentication configuration")
	ErrInvalidInput         = errors.New("invalid back-office authentication input")
	ErrEntropyUnavailable   = errors.New("back-office authentication entropy unavailable")
	ErrEncryption           = errors.New("back-office authentication encryption failed")
	ErrUnknownKey           = errors.New("unknown back-office authentication encryption key")
	ErrFlowConflict         = errors.New("back-office authentication flow already exists")
	ErrInvalidFlow          = errors.New("invalid or expired back-office authentication flow")
	ErrSessionConflict      = errors.New("back-office authentication session already exists")
	ErrSessionNotFound      = errors.New("back-office authentication session not found")
	ErrSessionExpired       = errors.New("back-office authentication session expired")
	ErrSessionRevoked       = errors.New("back-office authentication session revoked")
	ErrStoreUnavailable     = errors.New("back-office authentication store unavailable")
	ErrOAuthExchange        = errors.New("back-office OAuth exchange failed")
	ErrInvalidIDToken       = errors.New("invalid back-office ID token")
	ErrInvalidAccessToken   = errors.New("invalid back-office access token")
	ErrCSRF                 = errors.New("invalid back-office CSRF token")
	ErrOrigin               = errors.New("invalid back-office request origin")
)
