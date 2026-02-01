package store

import "github.com/google/uuid"

func ValidateDoubleEntryParams(params DoubleEntryParams) error {
	if params.TenantID == "" {
		return ErrMissingTenantID
	}
	if params.Currency == "" {
		return ErrMissingCurrency
	}
	if params.IdempotencyKey == "" {
		return ErrMissingIdempotencyKey
	}
	if params.ReferenceType == "" {
		return ErrMissingReferenceType
	}
	if params.DebitWalletID == uuid.Nil || params.CreditWalletID == uuid.Nil {
		return ErrMissingWalletID
	}
	if params.DebitWalletID == params.CreditWalletID {
		return ErrInvalidWalletPair
	}
	if params.Amount <= 0 {
		return ErrInvalidAmount
	}
	return nil
}

func ValidateHoldParams(params HoldParams) error {
	if params.TenantID == "" {
		return ErrMissingTenantID
	}
	if params.ExpiresAt.IsZero() {
		return ErrMissingHoldExpiry
	}
	if params.WalletID == uuid.Nil {
		return ErrMissingWalletID
	}
	if params.ReferenceType == "" {
		return ErrMissingReferenceType
	}
	if params.ReferenceID == "" {
		return ErrMissingReferenceID
	}
	if params.IdempotencyKey == "" {
		return ErrMissingIdempotencyKey
	}
	if params.Reason == "" {
		return ErrMissingHoldReason
	}
	if params.Amount <= 0 {
		return ErrInvalidAmount
	}
	return nil
}

func ValidateReleaseHold(tenantID string, holdID int64) error {
	if tenantID == "" {
		return ErrMissingTenantID
	}
	if holdID <= 0 {
		return ErrInvalidHoldID
	}
	return nil
}
