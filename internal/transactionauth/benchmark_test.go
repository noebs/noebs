package transactionauth

import (
	"context"
	"crypto/rand"
	"testing"
	"time"
)

var (
	benchmarkEnvelope      Envelope
	benchmarkPlaintext     []byte
	benchmarkAuthorization Authorization
)

func BenchmarkKeyring(b *testing.B) {
	keyring, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "active",
		Keys:        map[string][]byte{"active": make([]byte, aes256KeyBytes)},
		Entropy:     rand.Reader,
	})
	if err != nil {
		b.Fatal(err)
	}
	record := digestString("flow-state")
	plaintext := []byte("0123456789012345678901234567890123456789012")
	envelope, err := keyring.Seal(flowVerifierPurpose, record, plaintext)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("SealPKCEVerifier", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(plaintext)))
		for b.Loop() {
			sealed, err := keyring.Seal(flowVerifierPurpose, record, plaintext)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkEnvelope = sealed
		}
	})
	b.Run("OpenPKCEVerifier", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(plaintext)))
		for b.Loop() {
			opened, err := keyring.Open(flowVerifierPurpose, record, envelope)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkPlaintext = opened
		}
	})
}

func BenchmarkServiceIntentLifecycle(b *testing.B) {
	repository := newMemoryRepository()
	oauth := &testOAuth{}
	clock := &testClock{now: testNow}
	keyring, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "active",
		Keys:        map[string][]byte{"active": make([]byte, aes256KeyBytes)},
		Entropy:     rand.Reader,
	})
	if err != nil {
		b.Fatal(err)
	}
	service, err := NewService(ServiceConfig{
		Repository:       repository,
		OAuth:            oauth,
		Keys:             keyring,
		Clock:            clock,
		Entropy:          rand.Reader,
		RequiredACR:      "urn:noebs:acr:google-totp",
		BrowserStartTTL:  10 * time.Minute,
		FlowTTL:          5 * time.Minute,
		AuthorizationTTL: 2 * time.Minute,
	})
	if err != nil {
		b.Fatal(err)
	}
	binding := testBinding()
	oauth.identity = VerifiedIdentity{
		Issuer:             binding.Issuer,
		Subject:            binding.Subject,
		ACR:                "urn:noebs:acr:google-totp",
		AuthenticationTime: clock.Now(),
	}
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		initiated, err := service.Begin(ctx, binding)
		if err != nil {
			b.Fatal(err)
		}
		challenge, err := service.StartBrowser(ctx, initiated.BrowserStartToken)
		if err != nil {
			b.Fatal(err)
		}
		completed, err := service.Complete(ctx, oauth.state, challenge.BrowserBinding, "code")
		if err != nil {
			b.Fatal(err)
		}
		if err := service.Claim(ctx, initiated.IntentToken, binding); err != nil {
			b.Fatal(err)
		}
		benchmarkAuthorization = completed
	}
}
