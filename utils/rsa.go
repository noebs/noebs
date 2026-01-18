package utils

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func rsaEncrypt(text string, key string) {
	block, err := base64.StdEncoding.DecodeString(text)

	if err != nil {
		panic(err)
	}

	pub, err := x509.ParsePKIXPublicKey(block)
	if err != nil {
		panic(err)
	}

	rsaPub, _ := pub.(*rsa.PublicKey)
	fmt.Printf("The key is: %v, its type is %T", rsaPub, rsaPub)

	// do the encryption
	rsakey, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, []byte(key))
	if err != nil {
		panic(err)
	}
	fmt.Printf("the encryption is: %v", rsakey)
	encodedKey := base64.StdEncoding.EncodeToString(rsakey)
	fmt.Printf("the key is: %v", encodedKey)
}

func StringsToBytes(s []string) (bytes.Buffer, error) {
	b := bytes.Buffer{}
	err := json.NewEncoder(&b).Encode(s)
	return b, err
}

// MarshalIntoRedis marshals a type interface{} into a redis data
func MarshalIntoRedis(ctx context.Context, f interface{}, r *redis.Client, key string) error {
	res, err := json.Marshal(f)
	if err != nil {
		return err
	}
	mem := &redis.Z{
		Member: res,
	}
	_, err = r.ZAdd(ctx, key, *mem).Result()
	return err
}
