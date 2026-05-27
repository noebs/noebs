package store

import "errors"

var (
	ErrMissingTenantID = errors.New("missing tenant_id")
	ErrInvalidTenantID = errors.New("invalid tenant_id")
	ErrMissingUser     = errors.New("missing user")
	ErrMissingToken    = errors.New("missing token")
	ErrMissingPushData = errors.New("missing push data")
	ErrMissingKYC      = errors.New("missing kyc")
	ErrMissingAccount  = errors.New("missing auth account")
	ErrMissingUUID     = errors.New("missing uuid")
	ErrMissingMobile   = errors.New("missing mobile")
	ErrMissingPAN      = errors.New("missing pan")
	ErrMissingData     = errors.New("missing data")
	ErrMissingBillType = errors.New("missing bill_type")
	ErrInvalidUserID   = errors.New("invalid user_id")
	ErrMissingDataKey  = errors.New("missing data_key")
)
