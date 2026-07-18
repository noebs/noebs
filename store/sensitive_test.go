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

func TestSensitiveEncryptionHelpersReturnEncryptionErrors(t *testing.T) {
	withFailingRandomReader(t)
	s := New(nil, WithDataKey("test-data-key"))

	user := &ebs_fields.User{MainCard: "4242424242424242"}
	if err := s.encryptUserFields(user); err == nil {
		t.Fatal("encryptUserFields() error = nil, want encryption error")
	}
	if user.MainCard != "4242424242424242" || user.MainCardEnc != "" {
		t.Fatalf("encryptUserFields() mutated user after failure: %+v", user)
	}

}

func TestUpdateUserColumnsReturnsMainCardEncryptionErrors(t *testing.T) {
	withFailingRandomReader(t)
	s := New(nil, WithDataKey("test-data-key"))

	err := s.UpdateUserColumns(context.Background(), "tenant", 1, map[string]any{
		"main_card": "4242424242424242",
	})
	if err == nil {
		t.Fatal("UpdateUserColumns() error = nil, want encryption error")
	}
}

func TestHydrateSensitiveFieldsReturnsDecryptErrors(t *testing.T) {
	s := New(nil, WithDataKey("test-data-key"))

	user := &ebs_fields.User{MainCardEnc: "enc:invalid"}
	if err := s.hydrateUserFields(context.Background(), "tenant", user); err == nil {
		t.Fatal("hydrateUserFields() error = nil, want decrypt error")
	}

	token := &ebs_fields.Token{ToCardEnc: "enc:invalid"}
	if err := s.hydrateTokenFields(context.Background(), "tenant", token); err == nil {
		t.Fatal("hydrateTokenFields() error = nil, want decrypt error")
	}
}

func TestHydrateSensitiveFieldsReturnsBackfillEncryptionErrors(t *testing.T) {
	withFailingRandomReader(t)
	s := New(nil, WithDataKey("test-data-key"))

	user := &ebs_fields.User{Model: ebs_fields.Model{ID: 1}, MainCard: "4242424242424242"}
	if err := s.hydrateUserFields(context.Background(), "tenant", user); err == nil {
		t.Fatal("hydrateUserFields() error = nil, want backfill encryption error")
	}

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
			name: "missing tenant",
			run:  func() error { return s.updateUserMainCard(ctx, " ", 1, "hash", "enc") },
			want: ErrMissingTenantID,
		},
		{
			name: "invalid user id",
			run:  func() error { return s.updateUserMainCard(ctx, "tenant", 0, "hash", "enc") },
			want: ErrInvalidUserID,
		},
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
