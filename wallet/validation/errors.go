package validation

import (
	"errors"
)

var (
	ErrMissingStore                = errors.New("missing validation store")
	ErrWalletInactive              = errors.New("wallet not active")
	ErrWalletOwnerMismatch         = errors.New("wallet owner mismatch")
	ErrPSPConfigDisabled           = errors.New("psp config disabled")
	ErrPSPConfigMissingCurrencies  = errors.New("psp config missing enabled currencies")
	ErrPSPConfigMissingIdempotency = errors.New("psp config missing idempotency header")
	ErrPSPDirectionInvalid         = errors.New("psp direction not supported")
	ErrPSPCurrencyInvalid          = errors.New("psp currency not supported")
	ErrFeeExceedsAmount            = errors.New("fee exceeds amount")
	ErrMissingPSPTransactionID     = errors.New("missing psp transaction id")
	ErrMissingRequestedCurrency    = errors.New("missing requested currency")
	ErrMissingSettlementCurrency   = errors.New("missing settlement currency")
	ErrMissingWalletCurrency       = errors.New("missing wallet currency")
	ErrFXCurrencyMismatch          = errors.New("fx currency mismatch")
)
