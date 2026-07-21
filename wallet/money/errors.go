// Package money binds groosh's exact monetary arithmetic to the wallet's
// versioned currency and FX repositories.
package money

import (
	"errors"

	walletstore "github.com/adonese/noebs/wallet/store"
)

var (
	ErrMissingRepository        = errors.New("missing money repository")
	ErrInvalidCurrencyUnitData  = errors.New("invalid persisted currency unit")
	ErrInactiveCurrency         = walletstore.ErrInactiveCurrency
	ErrObservationPairMismatch  = errors.New("fx observation does not match requested currency pair")
	ErrQuoteIntegrity           = errors.New("persisted money conversion quote failed integrity validation")
	ErrQuoteIdempotencyConflict = walletstore.ErrConversionQuoteIdempotencyConflict
)
