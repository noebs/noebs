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

	card := &ebs_fields.Card{Pan: "4242424242424242", IPIN: "1234"}
	if err := s.encryptCardFields(card); err == nil {
		t.Fatal("encryptCardFields() error = nil, want encryption error")
	}
	if card.Pan != "4242424242424242" || card.PanEnc != "" || card.IPIN != "1234" || card.IPINEnc != "" {
		t.Fatalf("encryptCardFields() mutated card after failure: %+v", card)
	}

	cacheCard := &ebs_fields.CacheCards{Pan: "4242424242424242"}
	if err := s.encryptCacheCardFields(cacheCard); err == nil {
		t.Fatal("encryptCacheCardFields() error = nil, want encryption error")
	}
	if cacheCard.Pan != "4242424242424242" || cacheCard.PanEnc != "" {
		t.Fatalf("encryptCacheCardFields() mutated cache card after failure: %+v", cacheCard)
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

	card := &ebs_fields.Card{PanEnc: "enc:invalid"}
	if err := s.hydrateCardFields(context.Background(), "tenant", card); err == nil {
		t.Fatal("hydrateCardFields() error = nil, want decrypt error")
	}

	cacheCard := &ebs_fields.CacheCards{PanEnc: "enc:invalid"}
	if err := s.hydrateCacheCardFields(context.Background(), "tenant", cacheCard); err == nil {
		t.Fatal("hydrateCacheCardFields() error = nil, want decrypt error")
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

	card := &ebs_fields.Card{Model: ebs_fields.Model{ID: 1}, Pan: "4242424242424242"}
	if err := s.hydrateCardFields(context.Background(), "tenant", card); err == nil {
		t.Fatal("hydrateCardFields() error = nil, want backfill encryption error")
	}

	cacheCard := &ebs_fields.CacheCards{Model: ebs_fields.Model{ID: 1}, Pan: "4242424242424242"}
	if err := s.hydrateCacheCardFields(context.Background(), "tenant", cacheCard); err == nil {
		t.Fatal("hydrateCacheCardFields() error = nil, want backfill encryption error")
	}

	token := &ebs_fields.Token{Model: ebs_fields.Model{ID: 1}, ToCard: "4242424242424242"}
	if err := s.hydrateTokenFields(context.Background(), "tenant", token); err == nil {
		t.Fatal("hydrateTokenFields() error = nil, want backfill encryption error")
	}
}
