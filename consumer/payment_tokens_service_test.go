package consumer

import (
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestQuickPaymentTokenUUIDAcceptsExactlyOneReference(t *testing.T) {
	encoded := encodePaymentTokenForTest(t, "token-1")

	tests := []struct {
		name       string
		req        ebs_fields.QuickPaymentFields
		uuidQuery  string
		tokenQuery string
		want       string
	}{
		{
			name: "body token",
			req: ebs_fields.QuickPaymentFields{
				EncodedPaymentToken: encoded,
			},
			want: "token-1",
		},
		{
			name:       "query token",
			tokenQuery: encoded,
			want:       "token-1",
		},
		{
			name:      "query uuid",
			uuidQuery: "token-1",
			want:      "token-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := quickPaymentTokenUUID(tt.req, tt.uuidQuery, tt.tokenQuery)
			if err != nil {
				t.Fatalf("quickPaymentTokenUUID() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("quickPaymentTokenUUID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuickPaymentTokenUUIDRejectsMissingInvalidOrAmbiguousReferences(t *testing.T) {
	encoded := encodePaymentTokenForTest(t, "token-1")
	emptyUUIDToken := encodePaymentTokenForTest(t, "")

	tests := []struct {
		name       string
		req        ebs_fields.QuickPaymentFields
		uuidQuery  string
		tokenQuery string
		wantErr    error
	}{
		{
			name:    "missing",
			wantErr: ErrMissingUUID,
		},
		{
			name: "invalid body token",
			req: ebs_fields.QuickPaymentFields{
				EncodedPaymentToken: "not-base64",
			},
			wantErr: ErrInvalidPaymentToken,
		},
		{
			name:       "invalid query token",
			tokenQuery: "not-base64",
			wantErr:    ErrInvalidPaymentToken,
		},
		{
			name: "empty token uuid",
			req: ebs_fields.QuickPaymentFields{
				EncodedPaymentToken: emptyUUIDToken,
			},
			wantErr: ErrInvalidPaymentToken,
		},
		{
			name: "body token plus uuid",
			req: ebs_fields.QuickPaymentFields{
				EncodedPaymentToken: encoded,
			},
			uuidQuery: "token-1",
			wantErr:   ErrAmbiguousPaymentToken,
		},
		{
			name:       "query token plus uuid",
			tokenQuery: encoded,
			uuidQuery:  "token-1",
			wantErr:    ErrAmbiguousPaymentToken,
		},
		{
			name: "body token plus query token",
			req: ebs_fields.QuickPaymentFields{
				EncodedPaymentToken: encoded,
			},
			tokenQuery: encoded,
			wantErr:    ErrAmbiguousPaymentToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := quickPaymentTokenUUID(tt.req, tt.uuidQuery, tt.tokenQuery)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("quickPaymentTokenUUID() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func encodePaymentTokenForTest(t *testing.T, uuid string) string {
	t.Helper()
	encoded, err := ebs_fields.Encode(&ebs_fields.Token{UUID: uuid, Amount: 10})
	if err != nil {
		t.Fatalf("encode payment token: %v", err)
	}
	return encoded
}
