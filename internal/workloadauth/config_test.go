package workloadauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func TestConfigDecodesCanonicalEd25519Keys(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		SigningKeyID:      "gateway-2026-07-a",
		SigningPrivateKey: base64.StdEncoding.EncodeToString(privateKey),
		TrustedKeys: map[string]TrustedKeyConfig{
			"gateway-2026-07-a": {Caller: "api-gateway", PublicKey: base64.StdEncoding.EncodeToString(publicKey)},
		},
	}
	keyID, gotPrivate, present, err := config.SigningKey()
	if err != nil || !present || keyID != config.SigningKeyID || !bytes.Equal(gotPrivate, privateKey) {
		t.Fatalf("SigningKey() = %q, present=%t, err=%v", keyID, present, err)
	}
	registry, err := config.Registry()
	if err != nil {
		t.Fatal(err)
	}
	registered := registry[config.SigningKeyID]
	if registered.Caller != "api-gateway" || !bytes.Equal(registered.PublicKey, publicKey) {
		t.Fatalf("registry entry = %+v", registered)
	}
}

func TestConfigRejectsNonCanonicalOrInconsistentKeys(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append(ed25519.PrivateKey(nil), privateKey...)
	corrupt[len(corrupt)-1] ^= 1
	tests := []Config{
		{SigningKeyID: "only-id"},
		{SigningPrivateKey: base64.StdEncoding.EncodeToString(privateKey)},
		{SigningKeyID: "key", SigningPrivateKey: base64.RawStdEncoding.EncodeToString(privateKey)},
		{SigningKeyID: "key", SigningPrivateKey: base64.StdEncoding.EncodeToString(corrupt)},
	}
	for _, config := range tests {
		if _, _, _, err := config.SigningKey(); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("SigningKey() error = %v", err)
		}
	}
}

func TestSignerSetRejectsUnknownAudience(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	set, err := NewSignerSet(Config{
		SigningKeyID:      "gateway-2026-07-a",
		SigningPrivateKey: base64.StdEncoding.EncodeToString(privateKey),
	}, []string{"identity-auth"})
	if err != nil {
		t.Fatal(err)
	}
	if set.HasAudience("card-vault") {
		t.Fatal("unknown audience is present")
	}
}

func TestConfigRegistryRejectsMalformedEntries(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(publicKey)
	tests := []Config{
		{TrustedKeys: map[string]TrustedKeyConfig{"key id": {Caller: "api-gateway", PublicKey: encoded}}},
		{TrustedKeys: map[string]TrustedKeyConfig{"key": {Caller: "api gateway", PublicKey: encoded}}},
		{TrustedKeys: map[string]TrustedKeyConfig{"key": {Caller: "api-gateway", PublicKey: base64.RawStdEncoding.EncodeToString(publicKey)}}},
		{TrustedKeys: map[string]TrustedKeyConfig{"key": {Caller: "api-gateway", PublicKey: base64.StdEncoding.EncodeToString(publicKey[:12])}}},
	}
	for _, config := range tests {
		if _, err := config.Registry(); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("Registry() error = %v", err)
		}
	}
}
