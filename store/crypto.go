package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

const (
	encPrefix  = "enc:"
	hashPrefix = "h:"
)

type dataCrypto struct {
	gcm    cipher.AEAD
	macKey []byte
}

func newDataCrypto(key string) (*dataCrypto, error) {
	if key == "" {
		return nil, nil
	}
	encKey := sha256.Sum256([]byte("enc:" + key))
	macKey := sha256.Sum256([]byte("mac:" + key))
	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &dataCrypto{gcm: gcm, macKey: macKey[:]}, nil
}

func (c *dataCrypto) Encrypt(value string) (string, error) {
	if c == nil || value == "" {
		return value, nil
	}
	if strings.HasPrefix(value, encPrefix) {
		return value, nil
	}
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	cipherText := c.gcm.Seal(nil, nonce, []byte(value), nil)
	return encPrefix + base64.RawStdEncoding.EncodeToString(nonce) + ":" + base64.RawStdEncoding.EncodeToString(cipherText), nil
}

func (c *dataCrypto) Decrypt(value string) (string, error) {
	if c == nil || value == "" {
		return value, nil
	}
	if !strings.HasPrefix(value, encPrefix) {
		return value, nil
	}
	parts := strings.SplitN(strings.TrimPrefix(value, encPrefix), ":", 2)
	if len(parts) != 2 {
		return "", errors.New("invalid encrypted payload")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	cipherText, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	plain, err := c.gcm.Open(nil, nonce, cipherText, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (c *dataCrypto) Hash(value string) string {
	if c == nil || value == "" {
		return value
	}
	if strings.HasPrefix(value, hashPrefix) {
		return value
	}
	mac := hmac.New(sha256.New, c.macKey)
	_, _ = mac.Write([]byte(value))
	sum := mac.Sum(nil)
	return hashPrefix + hex.EncodeToString(sum)
}

func (c *dataCrypto) IsHash(value string) bool {
	return strings.HasPrefix(value, hashPrefix)
}

func (c *dataCrypto) IsEncrypted(value string) bool {
	return strings.HasPrefix(value, encPrefix)
}

func looksLikePAN(value string) bool {
	if value == "" {
		return false
	}
	if len(value) < 12 || len(value) > 19 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
