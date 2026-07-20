package backofficeauth

import (
	"crypto/rand"
	"errors"
	"sync"
	"testing"
)

func TestKeyringBindsCiphertextToPurposeRecordAndKeyVersion(t *testing.T) {
	oldKey := make([]byte, aes256KeyBytes)
	newKey := make([]byte, aes256KeyBytes)
	for i := range oldKey {
		oldKey[i] = byte(i + 1)
		newKey[i] = byte(i + 33)
	}
	oldRing, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "2026-07-a",
		Keys:        map[string][]byte{"2026-07-a": oldKey},
		Entropy:     rand.Reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	record := digestString("record-a")
	envelope, err := oldRing.Seal("session", record, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	rotated, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "2026-08-a",
		Keys: map[string][]byte{
			"2026-07-a": oldKey,
			"2026-08-a": newKey,
		},
		Entropy: rand.Reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := rotated.Open("session", record, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "secret" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	newEnvelope, err := rotated.Seal("session", record, []byte("new-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if newEnvelope.KeyID != "2026-08-a" {
		t.Fatalf("new envelope key = %q", newEnvelope.KeyID)
	}
	if _, err := rotated.Open("flow", record, envelope); !errors.Is(err, ErrEncryption) {
		t.Fatalf("purpose substitution error = %v", err)
	}
	if _, err := rotated.Open("session", digestString("record-b"), envelope); !errors.Is(err, ErrEncryption) {
		t.Fatalf("record substitution error = %v", err)
	}
	tampered := envelope
	tampered.Ciphertext = append([]byte(nil), envelope.Ciphertext...)
	tampered.Ciphertext[0] ^= 1
	if _, err := rotated.Open("session", record, tampered); !errors.Is(err, ErrEncryption) {
		t.Fatalf("ciphertext tampering error = %v", err)
	}
}

func TestKeyringRejectsInvalidConfigurationAndUnknownKey(t *testing.T) {
	key := make([]byte, aes256KeyBytes)
	for name, config := range map[string]KeyringConfig{
		"missing active":  {Keys: map[string][]byte{"a": key}, Entropy: rand.Reader},
		"active absent":   {ActiveKeyID: "b", Keys: map[string][]byte{"a": key}, Entropy: rand.Reader},
		"short key":       {ActiveKeyID: "a", Keys: map[string][]byte{"a": make([]byte, 31)}, Entropy: rand.Reader},
		"missing entropy": {ActiveKeyID: "a", Keys: map[string][]byte{"a": key}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewKeyring(config); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("NewKeyring() error = %v", err)
			}
		})
	}
	ring, err := NewKeyring(KeyringConfig{ActiveKeyID: "a", Keys: map[string][]byte{"a": key}, Entropy: rand.Reader})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ring.Open("session", Digest{}, Envelope{KeyID: "missing", Nonce: make([]byte, 12), Ciphertext: make([]byte, 16)}); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key error = %v", err)
	}
}

func TestKeyringConcurrentSealOpen(t *testing.T) {
	key := make([]byte, aes256KeyBytes)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	ring, err := NewKeyring(KeyringConfig{ActiveKeyID: "active", Keys: map[string][]byte{"active": key}, Entropy: rand.Reader})
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	var wg sync.WaitGroup
	errorsCh := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			record := digestString("same-record")
			envelope, err := ring.Seal("session", record, []byte("plaintext"))
			if err != nil {
				errorsCh <- err
				return
			}
			plaintext, err := ring.Open("session", record, envelope)
			if err != nil {
				errorsCh <- err
				return
			}
			if string(plaintext) != "plaintext" {
				errorsCh <- errors.New("plaintext mismatch")
			}
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
}
