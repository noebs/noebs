package workloadauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

// Config separates the caller's active private key from the receiver's public
// registry. A key ID may remain in TrustedKeys while callers rotate to a new
// active key.
type Config struct {
	SigningKeyID      string                      `json:"signing_key_id"`
	SigningPrivateKey string                      `json:"signing_private_key"`
	TrustedKeys       map[string]TrustedKeyConfig `json:"trusted_keys"`
	NonceDatabaseURL  string                      `json:"nonce_db_url"`
}

type TrustedKeyConfig struct {
	Caller    string `json:"caller"`
	PublicKey string `json:"public_key"`
}

// SystemClock is used by production signers and verifiers.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

// SigningKey decodes the optional active signing key. Supplying only half of
// the pair is always a startup error.
func (c Config) SigningKey() (string, ed25519.PrivateKey, bool, error) {
	if c.SigningKeyID == "" && c.SigningPrivateKey == "" {
		return "", nil, false, nil
	}
	if c.SigningKeyID == "" || c.SigningPrivateKey == "" {
		return "", nil, false, ErrInvalidConfiguration
	}
	decoded, err := decodeCanonicalBase64(c.SigningPrivateKey, ed25519.PrivateKeySize)
	if err != nil {
		return "", nil, false, err
	}
	key := ed25519.PrivateKey(decoded)
	// NewSigner also proves that the public half is consistent with the seed.
	if _, err := NewSigner(c.SigningKeyID, "configuration-check", key, SystemClock{}, rand.Reader); err != nil {
		return "", nil, false, err
	}
	return c.SigningKeyID, append(ed25519.PrivateKey(nil), key...), true, nil
}

// Registry decodes a receiver's rotation-friendly public key registry.
func (c Config) Registry() (Registry, error) {
	registry := make(Registry, len(c.TrustedKeys))
	for keyID, configured := range c.TrustedKeys {
		decoded, err := decodeCanonicalBase64(configured.PublicKey, ed25519.PublicKeySize)
		if err != nil {
			return nil, fmt.Errorf("%w: trusted key %q", err, keyID)
		}
		registry[keyID] = RegisteredKey{
			Caller:    configured.Caller,
			PublicKey: ed25519.PublicKey(decoded),
		}
	}
	if len(registry) > 0 {
		if _, err := NewVerifier("configuration-check", registry, SystemClock{}, configNonceStore{}); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

type configNonceStore struct{}

func (configNonceStore) Use(context.Context, string, string, string, time.Time) (bool, error) {
	return true, nil
}

func decodeCanonicalBase64(raw string, size int) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(decoded) != size || base64.StdEncoding.EncodeToString(decoded) != raw {
		return nil, ErrInvalidConfiguration
	}
	return decoded, nil
}

// SignerSet holds one caller key scoped into exact receiver audiences.
type SignerSet struct {
	signers map[string]*Signer
}

func NewSignerSet(config Config, audiences []string) (*SignerSet, error) {
	keyID, privateKey, present, err := config.SigningKey()
	if err != nil || !present || len(audiences) == 0 {
		return nil, ErrInvalidConfiguration
	}
	signers := make(map[string]*Signer, len(audiences))
	for _, audience := range audiences {
		if _, exists := signers[audience]; exists {
			return nil, ErrInvalidConfiguration
		}
		signer, err := NewSigner(keyID, audience, privateKey, SystemClock{}, rand.Reader)
		if err != nil {
			return nil, err
		}
		signers[audience] = signer
	}
	return &SignerSet{signers: signers}, nil
}

func (s *SignerSet) Sign(audience string, req *http.Request, body []byte) error {
	if s == nil {
		return ErrMissingSigner
	}
	signer, ok := s.signers[audience]
	if !ok {
		return ErrAudienceMismatch
	}
	return signer.Sign(req, body)
}

func (s *SignerSet) HasAudience(audience string) bool {
	if s == nil {
		return false
	}
	_, ok := s.signers[audience]
	return ok
}
