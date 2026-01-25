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
	"sync"
)

const (
	encPrefix  = "enc:"
	hashPrefix = "h:"
)

var luhnScratchPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 19)
		return &b
	},
}

var luhnDoubles = [10]byte{0, 2, 4, 6, 8, 1, 3, 5, 7, 9}

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
	bufPtr := luhnScratchPool.Get().(*[]byte)
	buf := (*bufPtr)[:0]
	defer func() {
		*bufPtr = buf[:0]
		luhnScratchPool.Put(bufPtr)
	}()
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < '0' || c > '9' {
			return false
		}
		buf = append(buf, c)
	}
	if len(buf) < 12 || len(buf) > 19 {
		return false
	}
	sum := 0
	alt := false
	for i := len(buf) - 1; i >= 0; i-- {
		d := int(buf[i] - '0')
		if alt {
			sum += int(luhnDoubles[d])
		} else {
			sum += d
		}
		alt = !alt
	}
	return sum%10 == 0
}
