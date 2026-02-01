package validation

import (
	"errors"
	"fmt"
)

var (
	ErrMissingStore            = errors.New("missing validation store")
	ErrWalletInactive          = errors.New("wallet not active")
	ErrWalletOwnerMismatch     = errors.New("wallet owner mismatch")
	ErrLimitExceeded           = errors.New("limit exceeded")
	ErrPSPConfigDisabled       = errors.New("psp config disabled")
	ErrPSPDirectionInvalid     = errors.New("psp direction not supported")
	ErrPSPCurrencyInvalid      = errors.New("psp currency not supported")
	ErrFeeExceedsAmount        = errors.New("fee exceeds amount")
	ErrMissingPSPTransactionID = errors.New("missing psp transaction id")
)

type LimitExceededError struct {
	Reason string
}

func (e LimitExceededError) Error() string {
	if e.Reason == "" {
		return ErrLimitExceeded.Error()
	}
	return fmt.Sprintf("%s: %s", ErrLimitExceeded.Error(), e.Reason)
}

func (e LimitExceededError) Is(target error) bool {
	return target == ErrLimitExceeded
}
