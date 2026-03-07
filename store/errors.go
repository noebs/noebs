package store

import "errors"

var (
	ErrMissingTenantID = errors.New("missing tenant_id")
	ErrMissingUser     = errors.New("missing user")
	ErrMissingToken    = errors.New("missing token")
	ErrMissingPushData = errors.New("missing push data")
	ErrMissingKYC      = errors.New("missing kyc")
	ErrMissingAccount  = errors.New("missing auth account")
	ErrMissingUUID     = errors.New("missing uuid")
	ErrInvalidUserID   = errors.New("invalid user_id")
	ErrMissingDataKey  = errors.New("missing data_key")
)
