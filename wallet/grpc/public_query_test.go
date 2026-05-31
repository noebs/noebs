package walletgrpc

import (
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPublicQueryLimitOffsetDefaultsAtBoundary(t *testing.T) {
	limit, offset, err := publicLimitOffset(0, 0, 100)
	if err != nil {
		t.Fatalf("publicLimitOffset(defaults) error = %v", err)
	}
	if limit != 100 || offset != 0 {
		t.Fatalf("publicLimitOffset(defaults) = %d, %d; want 100, 0", limit, offset)
	}

	limit, offset, err = publicLimitOffset(25, 5, 100)
	if err != nil {
		t.Fatalf("publicLimitOffset(values) error = %v", err)
	}
	if limit != 25 || offset != 5 {
		t.Fatalf("publicLimitOffset(values) = %d, %d; want 25, 5", limit, offset)
	}
}

func TestPublicQueryRejectsInvalidBoundsAtBoundary(t *testing.T) {
	if _, _, err := publicLimitOffset(-1, 0, 100); status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != walletstore.ErrInvalidLimit.Error() {
		t.Fatalf("publicLimitOffset(invalid limit) error = %v, want %v", err, walletstore.ErrInvalidLimit)
	}
	if _, _, err := publicLimitOffset(10, -1, 100); status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != walletstore.ErrInvalidOffset.Error() {
		t.Fatalf("publicLimitOffset(invalid offset) error = %v, want %v", err, walletstore.ErrInvalidOffset)
	}
	if _, _, err := publicLimitOffset(10, 0, 0); status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != walletstore.ErrInvalidLimit.Error() {
		t.Fatalf("publicLimitOffset(invalid default) error = %v, want %v", err, walletstore.ErrInvalidLimit)
	}
	if _, err := publicNonNegativeAmount(-1); status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != walletstore.ErrInvalidAmount.Error() {
		t.Fatalf("publicNonNegativeAmount(invalid) error = %v, want %v", err, walletstore.ErrInvalidAmount)
	}
}
