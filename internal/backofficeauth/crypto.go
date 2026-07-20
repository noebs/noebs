package backofficeauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const (
	aes256KeyBytes  = 32
	envelopeVersion = "noebs-backofficeauth-envelope-v1"
)

type KeyringConfig struct {
	ActiveKeyID string
	Keys        map[string][]byte
	Entropy     io.Reader
}

// Keyring encrypts new records with one active key and retains explicitly
// configured older keys solely to decrypt records during rotation.
type Keyring struct {
	activeKeyID string
	keys        map[string][aes256KeyBytes]byte
	entropy     io.Reader
}

func NewKeyring(config KeyringConfig) (*Keyring, error) {
	if config.ActiveKeyID == "" || config.Entropy == nil || len(config.Keys) == 0 {
		return nil, ErrInvalidConfiguration
	}
	keys := make(map[string][aes256KeyBytes]byte, len(config.Keys))
	for keyID, raw := range config.Keys {
		if keyID == "" || len(keyID) > 128 || strings.ContainsRune(keyID, '\x00') || len(raw) != aes256KeyBytes {
			return nil, ErrInvalidConfiguration
		}
		var key [aes256KeyBytes]byte
		copy(key[:], raw)
		keys[keyID] = key
	}
	if _, ok := keys[config.ActiveKeyID]; !ok {
		return nil, ErrInvalidConfiguration
	}
	return &Keyring{activeKeyID: config.ActiveKeyID, keys: keys, entropy: config.Entropy}, nil
}

func (k *Keyring) Seal(purpose string, record Digest, plaintext []byte) (Envelope, error) {
	if k == nil || purpose == "" || strings.ContainsRune(purpose, '\x00') || plaintext == nil {
		return Envelope{}, ErrInvalidInput
	}
	aead, err := k.aead(k.activeKeyID)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(k.entropy, nonce); err != nil {
		return Envelope{}, fmt.Errorf("%w: %w", ErrEntropyUnavailable, err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, envelopeAAD(k.activeKeyID, purpose, record))
	return Envelope{KeyID: k.activeKeyID, Nonce: nonce, Ciphertext: ciphertext}, nil
}

func (k *Keyring) Open(purpose string, record Digest, envelope Envelope) ([]byte, error) {
	if k == nil || purpose == "" || strings.ContainsRune(purpose, '\x00') ||
		envelope.KeyID == "" || len(envelope.Nonce) == 0 || len(envelope.Ciphertext) == 0 {
		return nil, ErrInvalidInput
	}
	aead, err := k.aead(envelope.KeyID)
	if err != nil {
		return nil, err
	}
	if len(envelope.Nonce) != aead.NonceSize() {
		return nil, ErrEncryption
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, envelopeAAD(envelope.KeyID, purpose, record))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEncryption, err)
	}
	return plaintext, nil
}

func (k *Keyring) aead(keyID string) (cipher.AEAD, error) {
	key, ok := k.keys[keyID]
	if !ok {
		return nil, ErrUnknownKey
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEncryption, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEncryption, err)
	}
	return aead, nil
}

func envelopeAAD(keyID, purpose string, record Digest) []byte {
	aad := make([]byte, 0, len(envelopeVersion)+len(keyID)+len(purpose)+len(record)+3)
	aad = append(aad, envelopeVersion...)
	aad = append(aad, 0)
	aad = append(aad, keyID...)
	aad = append(aad, 0)
	aad = append(aad, purpose...)
	aad = append(aad, 0)
	aad = append(aad, record[:]...)
	return aad
}

func generateOpaque(entropy io.Reader) (string, error) {
	raw := make([]byte, opaqueTokenBytes)
	if _, err := io.ReadFull(entropy, raw); err != nil {
		return "", fmt.Errorf("%w: %w", ErrEntropyUnavailable, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func digestOpaque(value string) (Digest, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != opaqueTokenBytes || base64.RawURLEncoding.EncodeToString(raw) != value {
		return Digest{}, ErrInvalidInput
	}
	return sha256.Sum256([]byte(value)), nil
}

func digestString(value string) Digest {
	return sha256.Sum256([]byte(value))
}
