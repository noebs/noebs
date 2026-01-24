package utils

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

func rsaEncrypt(text string, key string) (string, error) {
	block, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return "", err
	}

	pub, err := x509.ParsePKIXPublicKey(block)
	if err != nil {
		return "", err
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("invalid public key type %T", pub)
	}

	// do the encryption
	rsakey, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, []byte(key))
	if err != nil {
		return "", err
	}
	encodedKey := base64.StdEncoding.EncodeToString(rsakey)
	return encodedKey, nil
}

func StringsToBytes(s []string) (bytes.Buffer, error) {
	b := bytes.Buffer{}
	err := json.NewEncoder(&b).Encode(s)
	return b, err
}
