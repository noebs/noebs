// Package ebsipin creates the RSA blocks required by EBS cardholder requests.
package ebsipin

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidPublicKey = errors.New("invalid EBS IPIN public key")

// Encrypt returns a canonical base64 RSA PKCS #1 v1.5 block containing the
// EBS transaction UUID followed by the supplied PIN value.
func Encrypt(encodedPublicKey, pin, transactionUUID string) (string, error) {
	if encodedPublicKey == "" || encodedPublicKey != strings.TrimSpace(encodedPublicKey) {
		return "", ErrInvalidPublicKey
	}
	der, err := base64.StdEncoding.DecodeString(encodedPublicKey)
	if err != nil || base64.StdEncoding.EncodeToString(der) != encodedPublicKey {
		return "", ErrInvalidPublicKey
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return "", ErrInvalidPublicKey
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok || publicKey.N == nil || publicKey.N.BitLen() < 2048 || publicKey.N.BitLen() > 4096 {
		return "", ErrInvalidPublicKey
	}

	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, publicKey, []byte(transactionUUID+pin))
	if err != nil {
		return "", fmt.Errorf("encrypt EBS IPIN block: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}
