package workloadauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type Verifier struct {
	audience string
	registry Registry
	clock    Clock
	nonces   NonceStore
}

func NewVerifier(audience string, registry Registry, clock Clock, nonces NonceStore) (*Verifier, error) {
	if !validOpaqueHeaderValue(audience) || len(registry) == 0 || clock == nil || nonces == nil {
		return nil, ErrInvalidConfiguration
	}
	registryCopy := make(Registry, len(registry))
	for keyID, registered := range registry {
		if !validOpaqueHeaderValue(keyID) || !validOpaqueHeaderValue(registered.Caller) || len(registered.PublicKey) != ed25519.PublicKeySize {
			return nil, ErrInvalidConfiguration
		}
		registryCopy[keyID] = RegisteredKey{
			Caller:    registered.Caller,
			PublicKey: append(ed25519.PublicKey(nil), registered.PublicKey...),
		}
	}
	return &Verifier{audience: audience, registry: registryCopy, clock: clock, nonces: nonces}, nil
}

// Verify authenticates req against the supplied serialized body without
// reading or replacing req.Body.
func (v *Verifier) Verify(req *http.Request, body []byte) (Principal, error) {
	in, err := requestInput(req)
	if err != nil {
		return Principal{}, err
	}
	in.keyID, err = uniqueHeader(req.Header, HeaderKeyID, true)
	if err != nil {
		return Principal{}, err
	}
	if !validOpaqueHeaderValue(in.keyID) {
		return Principal{}, ErrUnknownKey
	}
	registered, ok := v.registry[in.keyID]
	if !ok {
		return Principal{}, ErrUnknownKey
	}

	in.audience, err = uniqueHeader(req.Header, HeaderAudience, true)
	if err != nil {
		return Principal{}, err
	}
	if !constantTimeEqual(in.audience, v.audience) {
		return Principal{}, ErrAudienceMismatch
	}

	in.timestamp, err = uniqueHeader(req.Header, HeaderTimestamp, true)
	if err != nil {
		return Principal{}, err
	}
	timestamp, err := parseTimestamp(in.timestamp)
	if err != nil {
		return Principal{}, err
	}
	nowTime := v.clock.Now()
	now := nowTime.Unix()
	if timestamp < now-int64(oldestAccepted/time.Second) {
		return Principal{}, ErrTimestampExpired
	}
	if timestamp > now+int64(newestAccepted/time.Second) {
		return Principal{}, ErrTimestampInFuture
	}

	in.nonce, err = uniqueHeader(req.Header, HeaderNonce, true)
	if err != nil {
		return Principal{}, err
	}
	if _, err := parseNonce(in.nonce); err != nil {
		return Principal{}, err
	}

	in.bodyDigest, err = uniqueHeader(req.Header, HeaderBodySHA256, true)
	if err != nil {
		return Principal{}, err
	}
	signedDigest, err := parseBodyDigest(in.bodyDigest)
	if err != nil {
		return Principal{}, err
	}
	actualDigest := sha256.Sum256(body)
	if subtle.ConstantTimeCompare(signedDigest[:], actualDigest[:]) != 1 {
		return Principal{}, ErrBodyDigestMismatch
	}

	rawSignature, err := uniqueHeader(req.Header, HeaderSignature, true)
	if err != nil {
		return Principal{}, err
	}
	signature, err := parseSignature(rawSignature)
	if err != nil {
		return Principal{}, err
	}
	record, err := canonicalRecord(in)
	if err != nil {
		return Principal{}, err
	}
	if !ed25519.Verify(registered.PublicKey, record, signature) {
		return Principal{}, ErrInvalidSignature
	}

	expiresAt := nowTime.Add(nonceRetention)
	used, err := v.nonces.Use(req.Context(), in.keyID, v.audience, in.nonce, expiresAt)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %w", ErrNonceStore, err)
	}
	if !used {
		return Principal{}, ErrReplay
	}

	return Principal{
		Caller:    registered.Caller,
		KeyID:     in.keyID,
		Audience:  v.audience,
		RequestID: in.requestID,
		Nonce:     in.nonce,
		SignedAt:  time.Unix(timestamp, 0).UTC(),
	}, nil
}

// VerifyRequest reads req.Body to verify it and restores an equivalent readable
// stream before returning. Verify is preferable when the boundary has already
// retained the serialized bytes.
func (v *Verifier) VerifyRequest(req *http.Request) (Principal, error) {
	body, err := readRequestBody(req)
	if err != nil {
		return Principal{}, err
	}
	return v.Verify(req, body)
}

func parseTimestamp(raw string) (int64, error) {
	timestamp, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || timestamp <= 0 || strconv.FormatInt(timestamp, 10) != raw {
		return 0, ErrInvalidTimestamp
	}
	return timestamp, nil
}

func parseNonce(raw string) ([16]byte, error) {
	var nonce [16]byte
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != len(nonce) || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return nonce, ErrInvalidNonce
	}
	copy(nonce[:], decoded)
	return nonce, nil
}

func parseSignature(raw string) ([]byte, error) {
	signature, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.RawURLEncoding.EncodeToString(signature) != raw {
		return nil, ErrInvalidSignature
	}
	return signature, nil
}

func constantTimeEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
