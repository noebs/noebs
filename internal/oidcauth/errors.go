package oidcauth

import "errors"

var (
	ErrInvalidConfiguration = errors.New("invalid OIDC authentication configuration")
	ErrMissingAuthorization = errors.New("authorization header is required")
	ErrInvalidAuthorization = errors.New("authorization header must contain exactly one Bearer token")
	ErrInvalidToken         = errors.New("invalid OIDC access token")
	ErrUnknownKey           = errors.New("unknown OIDC signing key")
	ErrKeySetUnavailable    = errors.New("OIDC key set unavailable")
	ErrInvalidJWKS          = errors.New("invalid OIDC JSON web key set")
)
