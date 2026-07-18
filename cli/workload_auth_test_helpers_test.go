package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/workloadauth"
)

type testWorkloadNonceStore struct {
	mu     sync.Mutex
	nonces map[string]time.Time
}

func (s *testWorkloadNonceStore) Use(_ context.Context, keyID, audience, nonce string, expiresAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := keyID + "\x00" + audience + "\x00" + nonce
	if _, exists := s.nonces[key]; exists {
		return false, nil
	}
	s.nonces[key] = expiresAt
	return true, nil
}

func testWorkloadPrivateKey(caller string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("noebs workload test key: " + caller))
	return ed25519.NewKeyFromSeed(seed[:])
}

func testWorkloadKeyID(caller string) string {
	return caller + "-test-2026-07"
}

func newTestWorkloadSigners(t *testing.T, caller string, audiences ...string) *workloadauth.SignerSet {
	t.Helper()
	set, err := workloadauth.NewSignerSet(workloadauth.Config{
		SigningKeyID:      testWorkloadKeyID(caller),
		SigningPrivateKey: base64.StdEncoding.EncodeToString(testWorkloadPrivateKey(caller)),
	}, audiences)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func newTestWorkloadVerifier(t *testing.T, audience string, callers ...string) *workloadauth.Verifier {
	t.Helper()
	registry := make(workloadauth.Registry, len(callers))
	for _, caller := range callers {
		privateKey := testWorkloadPrivateKey(caller)
		registry[testWorkloadKeyID(caller)] = workloadauth.RegisteredKey{
			Caller:    caller,
			PublicKey: privateKey.Public().(ed25519.PublicKey),
		}
	}
	verifier, err := workloadauth.NewVerifier(
		audience,
		registry,
		workloadauth.SystemClock{},
		&testWorkloadNonceStore{nonces: make(map[string]time.Time)},
	)
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}
