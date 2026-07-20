package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	issuer  = "https://accounts.google.com"
	client  = "google-client"
	keyID   = "noebs-google-mock"
	subject = "noebs-google-test-subject"
)

type authorization struct {
	nonce string
}

type server struct {
	key            *rsa.PrivateKey
	mu             sync.Mutex
	authorizations map[string]authorization
}

func main() {
	listen := flag.String("listen", ":443", "TLS listen address")
	certificate := flag.String("certificate", "", "TLS certificate")
	privateKey := flag.String("private-key", "", "TLS private key")
	flag.Parse()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	mock := &server{key: key, authorizations: map[string]authorization{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/o/oauth2/v2/auth", mock.authorize)
	mux.HandleFunc("/token", mock.token)
	mux.HandleFunc("/oauth2/v3/certs", mock.keys)
	mux.HandleFunc("/v1/userinfo", mock.userInfo)
	log.Fatal(http.ListenAndServeTLS(*listen, *certificate, *privateKey, mux))
}

func (s *server) authorize(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	redirect, err := url.Parse(query.Get("redirect_uri"))
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" {
		http.Error(writer, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	code := randomToken()
	s.mu.Lock()
	s.authorizations[code] = authorization{nonce: query.Get("nonce")}
	s.mu.Unlock()
	callback := redirect.Query()
	callback.Set("code", code)
	callback.Set("state", query.Get("state"))
	redirect.RawQuery = callback.Encode()
	http.Redirect(writer, request, redirect.String(), http.StatusFound)
}

func (s *server) token(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		http.Error(writer, "invalid form", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	authorization, found := s.authorizations[request.Form.Get("code")]
	delete(s.authorizations, request.Form.Get("code"))
	s.mu.Unlock()
	if !found {
		http.Error(writer, "invalid code", http.StatusBadRequest)
		return
	}
	now := time.Now().Unix()
	idToken, err := s.sign(map[string]any{
		"iss":            issuer,
		"aud":            client,
		"azp":            client,
		"sub":            subject,
		"email":          "wallet-authorizer@example.invalid",
		"email_verified": true,
		"given_name":     "Wallet",
		"family_name":    "Authorizer",
		"name":           "Wallet Authorizer",
		"nonce":          authorization.nonce,
		"iat":            now,
		"exp":            now + 300,
	})
	if err != nil {
		http.Error(writer, "sign token", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, map[string]any{
		"access_token": "google-mock-access-token",
		"expires_in":   300,
		"id_token":     idToken,
		"scope":        "openid profile email",
		"token_type":   "Bearer",
	})
}

func (s *server) keys(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, map[string]any{"keys": []map[string]string{{
		"alg": "RS256",
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(s.key.PublicKey.E)).Bytes()),
		"kid": keyID,
		"kty": "RSA",
		"n":   base64.RawURLEncoding.EncodeToString(s.key.PublicKey.N.Bytes()),
		"use": "sig",
	}}})
}

func (*server) userInfo(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, map[string]any{
		"sub":            subject,
		"email":          "wallet-authorizer@example.invalid",
		"email_verified": true,
		"given_name":     "Wallet",
		"family_name":    "Authorizer",
		"name":           "Wallet Authorizer",
	})
}

func (s *server) sign(claims map[string]any) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": keyID, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func randomToken() string {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		log.Print(fmt.Errorf("encode response: %w", err))
	}
}
