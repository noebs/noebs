package store

import "errors"

var (
	ErrMissingTenantID     = errors.New("missing tenant_id")
	ErrInvalidTenantID     = errors.New("invalid tenant_id")
	ErrMissingUser         = errors.New("missing user")
	ErrMissingToken        = errors.New("missing token")
	ErrMissingPushData     = errors.New("missing push data")
	ErrMissingKYC          = errors.New("missing kyc")
	ErrMissingAccount      = errors.New("missing auth account")
	ErrMissingUUID         = errors.New("missing uuid")
	ErrMissingMobile       = errors.New("missing mobile")
	ErrMissingPAN          = errors.New("missing pan")
	ErrMissingData         = errors.New("missing data")
	ErrMissingBillType     = errors.New("missing bill_type")
	ErrMissingEventID      = errors.New("missing event id")
	ErrMissingEventKey     = errors.New("missing event key")
	ErrMissingEventTopic   = errors.New("missing event topic")
	ErrMissingEventType    = errors.New("missing event type")
	ErrMissingEventPayload = errors.New("missing event payload")
	ErrInvalidUserID       = errors.New("invalid user_id")
	ErrInvalidUserColumn   = errors.New("invalid user column")
	ErrMissingDataKey      = errors.New("missing data_key")
)
