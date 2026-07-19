package store

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("random source failed")
}

func withFailingRandomReader(t *testing.T) {
	t.Helper()
	original := cryptorand.Reader
	cryptorand.Reader = failingReader{}
	t.Cleanup(func() {
		cryptorand.Reader = original
	})
}

func TestHydrateTokenFieldsReturnsDecryptErrors(t *testing.T) {
	s := New(nil, WithDataKey("test-data-key"))

	token := &ebs_fields.Token{ToCardEnc: "enc:invalid"}
	if err := s.hydrateTokenFields(context.Background(), "tenant", token); err == nil {
		t.Fatal("hydrateTokenFields() error = nil, want decrypt error")
	}
}

func TestHydrateTokenFieldsReturnsBackfillEncryptionErrors(t *testing.T) {
	withFailingRandomReader(t)
	s := New(nil, WithDataKey("test-data-key"))

	token := &ebs_fields.Token{Model: ebs_fields.Model{ID: 1}, ToCard: "4242424242424242"}
	if err := s.hydrateTokenFields(context.Background(), "tenant", token); err == nil {
		t.Fatal("hydrateTokenFields() error = nil, want backfill encryption error")
	}
}

func TestSensitiveBackfillUpdatesValidateTargetsBeforeDB(t *testing.T) {
	s := &Store{}
	ctx := context.Background()

	tests := []struct {
		name string
		run  func() error
		want error
	}{
		{
			name: "missing token uuid",
			run:  func() error { return s.updateTokenCard(ctx, "tenant", " ", "hash", "enc") },
			want: ErrMissingUUID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}
