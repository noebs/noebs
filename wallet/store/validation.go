package store

import (
	"errors"

	basestore "github.com/adonese/noebs/store"
	"github.com/google/uuid"
)

func ValidateTenantID(tenantID string) (string, error) {
	tenantID, err := basestore.ValidateTenantID(tenantID)
	switch {
	case err == nil:
		return tenantID, nil
	case errors.Is(err, basestore.ErrMissingTenantID):
		return "", ErrMissingTenantID
	case errors.Is(err, basestore.ErrInvalidTenantID):
		return "", ErrInvalidTenantID
	default:
		return "", err
	}
}

// ValidateIdempotencyKey validates the shared public-command key contract.
// Keys are opaque, exact UTF-8 values; lower layers never trim or synthesize
// them.
func ValidateIdempotencyKey(idempotencyKey string) error {
	return validateBoundedIdentifier(
		idempotencyKey,
		256,
		ErrMissingIdempotencyKey,
		ErrInvalidIdempotencyKey,
	)
}

// ValidateCurrencyUnitID distinguishes an omitted zero value from an invalid
// negative identifier. Stores and services call this instead of collapsing
// both cases into a silent fallback or an ambiguous error.
func ValidateCurrencyUnitID(currencyUnitID int64) error {
	switch {
	case currencyUnitID == 0:
		return ErrMissingCurrencyUnitID
	case currencyUnitID < 0:
		return ErrInvalidCurrencyUnitID
	default:
		return nil
	}
}

func ValidateDoubleEntryParams(params DoubleEntryParams) error {
	if _, err := ValidateTenantID(params.TenantID); err != nil {
		return err
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

func ValidateHeldDoubleEntryParams(params HeldDoubleEntryParams) error {
	if err := ValidateDoubleEntryParams(params.Entry); err != nil {
		return err
	}
	return ValidateReleaseHold(params.Entry.TenantID, params.HoldID)
}

func ValidateHoldParams(params HoldParams) error {
	if _, err := ValidateTenantID(params.TenantID); err != nil {
		return err
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
	if _, err := ValidateTenantID(tenantID); err != nil {
		return err
	}
	if holdID <= 0 {
		return ErrInvalidHoldID
	}
	return nil
}
