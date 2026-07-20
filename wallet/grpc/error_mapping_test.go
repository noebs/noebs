package walletgrpc

import (
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMapErrorMapsPSPValidationFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{"unsupported-currency", walletvalidation.ErrPSPCurrencyInvalid, codes.InvalidArgument},
		{"disabled-config", walletvalidation.ErrPSPConfigDisabled, codes.FailedPrecondition},
		{"missing-currencies", walletvalidation.ErrPSPConfigMissingCurrencies, codes.FailedPrecondition},
		{"unsupported-direction", walletvalidation.ErrPSPDirectionInvalid, codes.FailedPrecondition},
		{"missing-verification-time", walletstore.ErrMissingVerificationTime, codes.InvalidArgument},
		{"invalid-verification-time", walletstore.ErrInvalidVerificationTime, codes.InvalidArgument},
		{"missing-status-timeout", walletstore.ErrMissingStatusTimeout, codes.InvalidArgument},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := mapError(tc.err)
			if status.Code(mapped) != tc.code {
				t.Fatalf("status.Code(mapError(%v)) = %v, want %v", tc.err, status.Code(mapped), tc.code)
			}
		})
	}
}
