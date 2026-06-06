package walletgrpc

import (
	"errors"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestTextOrDefaultTreatsBlankAsOmitted(t *testing.T) {
	if got := textOrDefault(" \t ", "fallback"); got != "fallback" {
		t.Fatalf("textOrDefault(blank) = %q, want fallback", got)
	}
	if got := textOrDefault(" explicit ", "fallback"); got != " explicit " {
		t.Fatalf("textOrDefault(value) = %q, want explicit value unchanged", got)
	}
}

func TestResolveIdempotencyAndReference(t *testing.T) {
	cases := []struct {
		name    string
		idem    string
		ref     string
		wantID  string
		wantRef string
		wantErr error
	}{
		{
			name:    "both missing",
			wantErr: walletstore.ErrMissingIdempotencyKey,
		},
		{
			name:    "both blank",
			idem:    " \t ",
			ref:     " \t ",
			wantErr: walletstore.ErrMissingIdempotencyKey,
		},
		{
			name:    "idempotency defaults from reference",
			idem:    " \t ",
			ref:     "ref-1",
			wantID:  "ref-1",
			wantRef: "ref-1",
		},
		{
			name:    "reference defaults from idempotency",
			idem:    "idem-1",
			ref:     " \t ",
			wantID:  "idem-1",
			wantRef: "idem-1",
		},
		{
			name:    "explicit values preserved",
			idem:    " idem-1 ",
			ref:     " ref-1 ",
			wantID:  " idem-1 ",
			wantRef: " ref-1 ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotRef, err := resolveIdempotencyAndReference(tc.idem, tc.ref)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("resolveIdempotencyAndReference() error = %v, want %v", err, tc.wantErr)
			}
			if gotID != tc.wantID || gotRef != tc.wantRef {
				t.Fatalf("resolveIdempotencyAndReference() = %q/%q, want %q/%q", gotID, gotRef, tc.wantID, tc.wantRef)
			}
		})
	}
}
