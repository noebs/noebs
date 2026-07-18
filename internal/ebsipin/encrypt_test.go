package ebsipin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"testing"
)

func TestEncryptProducesEBSCompatibleBlock(t *testing.T) {
	privateKey, encodedPublicKey := testRSAKey(t, 2048)

	block, err := Encrypt(encodedPublicKey, "1234", "transaction-uuid")
	if err != nil {
		t.Fatalf("Encrypt(): %v", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(block)
	if err != nil {
		t.Fatalf("decode block: %v", err)
	}
	plaintext, err := rsa.DecryptPKCS1v15(rand.Reader, privateKey, ciphertext)
	if err != nil {
		t.Fatalf("decrypt block: %v", err)
	}
	if got, want := string(plaintext), "transaction-uuid1234"; got != want {
		t.Fatalf("plaintext = %q, want %q", got, want)
	}
	if canonical := base64.StdEncoding.EncodeToString(ciphertext); canonical != block {
		t.Fatalf("block is not canonical base64: %q", block)
	}
}

func TestEncryptRejectsInvalidPublicKeys(t *testing.T) {
	_, valid := testRSAKey(t, 2048)
	_, weak := testRSAKey(t, 1024)
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	nonRSA := encodePublicKey(t, &ecdsaKey.PublicKey)

	for name, key := range map[string]string{
		"missing":       "",
		"malformed":     "not-base64",
		"non-canonical": valid + "\n",
		"non-RSA":       nonRSA,
		"weak RSA":      weak,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Encrypt(key, "1234", "uuid"); !errors.Is(err, ErrInvalidPublicKey) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidPublicKey)
			}
		})
	}
}

func testRSAKey(t *testing.T, bits int) (*rsa.PrivateKey, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return privateKey, encodePublicKey(t, &privateKey.PublicKey)
}

func encodePublicKey(t *testing.T, publicKey any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(der)
}
