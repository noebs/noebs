package utils

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/adonese/noebs/ebs_fields"

	//"encoding/pem"
	//"crypto/sha256"
	"crypto/x509"
)


func main(){
	text := "MFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBAJ4HwthfqXiK09AgShnnLqAqMyT5VUV0hvSdG+ySMx+a54Ui5EStkmO8iOdVG9DlWv55eLBoodjSfd0XRxN7an0CAwEAAQ=="

	msg := "12413940-4350-4fdd-9a96-fa08715d35130000"
	rsaEncrypt(text, msg)

}

func rsaEncrypt(text string, key string){
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
	rsakey,err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, []byte(key))
	if err != nil {
		panic(err)
	}
	fmt.Printf("the encryption is: %v", rsakey)
	encodedKey := base64.StdEncoding.EncodeToString(rsakey)
	fmt.Printf("the key is: %v", encodedKey)
}


func StringsToBytes(s []string) (bytes.Buffer, error){
	b := bytes.Buffer{}
	err := json.NewEncoder(&b).Encode(s)
	return b, err
}

func RedisHelper(s []string) ebs_fields.CardsRedis{
	var c ebs_fields.CardsRedis
	if len(s) == 1{
		for _, v := range s{
			json.Unmarshal(v, &c)
		}
	}
	return c
}