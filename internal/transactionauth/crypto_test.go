package transactionauth

import (
	"bytes"
	"errors"
	"testing"
)

func TestKeyringBindsCiphertextToPurposeRecordAndKey(t *testing.T) {
	record := digestString("intent-1")
	key := bytes.Repeat([]byte{0x41}, aes256KeyBytes)
	ring, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "key-1",
		Keys:        map[string][]byte{"key-1": key},
		Entropy:     bytes.NewReader(bytes.Repeat([]byte{0x22}, 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := ring.Seal("pkce", record, []byte("verifier"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := ring.Open("pkce", record, envelope)
	if err != nil || string(plaintext) != "verifier" {
		t.Fatalf("Open() = %q, %v", plaintext, err)
	}

	mutations := map[string]func(*Envelope, *string, *Digest){
		"ciphertext": func(envelope *Envelope, _ *string, _ *Digest) { envelope.Ciphertext[0] ^= 1 },
		"nonce":      func(envelope *Envelope, _ *string, _ *Digest) { envelope.Nonce[0] ^= 1 },
		"purpose":    func(_ *Envelope, purpose *string, _ *Digest) { *purpose = "tokens" },
		"record":     func(_ *Envelope, _ *string, record *Digest) { *record = digestString("intent-2") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			copyEnvelope := Envelope{
				KeyID:      envelope.KeyID,
				Nonce:      bytes.Clone(envelope.Nonce),
				Ciphertext: bytes.Clone(envelope.Ciphertext),
			}
			purpose := "pkce"
			copyRecord := record
			mutate(&copyEnvelope, &purpose, &copyRecord)
			if _, err := ring.Open(purpose, copyRecord, copyEnvelope); !errors.Is(err, ErrEncryption) {
				t.Fatalf("Open() error = %v, want %v", err, ErrEncryption)
			}
		})
	}
}

func TestKeyringSupportsExplicitRotationAndRejectsUnknownKeys(t *testing.T) {
	oldKey := bytes.Repeat([]byte{0x31}, aes256KeyBytes)
	newKey := bytes.Repeat([]byte{0x32}, aes256KeyBytes)
	record := digestString("intent")
	oldRing, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "old",
		Keys:        map[string][]byte{"old": oldKey},
		Entropy:     bytes.NewReader(bytes.Repeat([]byte{0x11}, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := oldRing.Seal("pkce", record, []byte("verifier"))
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "new",
		Keys:        map[string][]byte{"old": oldKey, "new": newKey},
		Entropy:     bytes.NewReader(bytes.Repeat([]byte{0x12}, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, err := rotated.Open("pkce", record, envelope); err != nil || string(plaintext) != "verifier" {
		t.Fatalf("rotated Open() = %q, %v", plaintext, err)
	}
	envelope.KeyID = "missing"
	if _, err := rotated.Open("pkce", record, envelope); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown-key error = %v, want %v", err, ErrUnknownKey)
	}
}

func TestKeyringFailsClosedOnInvalidMaterialAndEntropyFailure(t *testing.T) {
	key := bytes.Repeat([]byte{0x41}, aes256KeyBytes)
	if _, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "key-1",
		Keys:        map[string][]byte{"key-1": key[:len(key)-1]},
		Entropy:     bytes.NewReader(nil),
	}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("invalid key error = %v, want %v", err, ErrInvalidConfiguration)
	}
	ring, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "key-1",
		Keys:        map[string][]byte{"key-1": key},
		Entropy:     bytes.NewReader(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ring.Seal("pkce", digestString("intent"), []byte("verifier")); !errors.Is(err, ErrEntropyUnavailable) {
		t.Fatalf("entropy error = %v, want %v", err, ErrEntropyUnavailable)
	}
	if _, err := ring.Open("pkce", digestString("intent"), Envelope{KeyID: "key-1"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("malformed envelope error = %v, want %v", err, ErrInvalidInput)
	}
}
