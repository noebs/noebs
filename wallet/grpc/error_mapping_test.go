package walletgrpc

import (
	"errors"
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
		{"fee-percentage-not-representable", walletstore.ErrFeePercentageNotRepresentable, codes.InvalidArgument},
		{"legacy-rate-not-representable", walletstore.ErrLegacyRateNotRepresentable, codes.InvalidArgument},
		{"spread-not-representable", walletstore.ErrSpreadNotRepresentable, codes.InvalidArgument},
		{"psp-fx-rate-not-representable", walletstore.ErrPSPFXRateNotRepresentable, codes.InvalidArgument},
		{"missing-psp-fx-rate-fraction", walletstore.ErrMissingFXRateFraction, codes.InvalidArgument},
		{"invalid-psp-fx-rate-fraction", walletstore.ErrInvalidFXRateFraction, codes.InvalidArgument},
		{"psp-fx-rate-fraction-not-representable", walletstore.ErrPSPFXRateFractionNotRepresentable, codes.InvalidArgument},
		{"missing-psp-fx-conversion-time", walletstore.ErrMissingFXConversionTime, codes.InvalidArgument},
		{"invalid-psp-fx-conversion-time", walletstore.ErrInvalidFXConversionTime, codes.InvalidArgument},
		{"psp-fx-provenance-mismatch", walletstore.ErrPSPFXProvenanceMismatch, codes.InvalidArgument},
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

func TestMapErrorProtectsInternalDetailsAndMapsQuoteAbuseControls(t *testing.T) {
	if mapped := mapError(walletstore.ErrConversionQuoteLimitExceeded); status.Code(mapped) != codes.ResourceExhausted {
		t.Fatalf("quote limit code = %v, want %v", status.Code(mapped), codes.ResourceExhausted)
	}
	if mapped := mapError(walletstore.ErrConversionQuoteIdempotencyConflict); status.Code(mapped) != codes.AlreadyExists {
		t.Fatalf("quote conflict code = %v, want %v", status.Code(mapped), codes.AlreadyExists)
	}
	internal := mapError(errors.New("secret database detail"))
	if status.Code(internal) != codes.Internal || status.Convert(internal).Message() != "internal wallet error" {
		t.Fatalf("internal error = %v", internal)
	}
}
