package workloadauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// Signer holds one workload's private key and the one audience for which it is
// valid. Construct a separate signer for a different audience.
type Signer struct {
	keyID      string
	audience   string
	privateKey ed25519.PrivateKey
	clock      Clock
	random     io.Reader
}

func NewSigner(keyID, audience string, privateKey ed25519.PrivateKey, clock Clock, random io.Reader) (*Signer, error) {
	if !validOpaqueHeaderValue(keyID) || !validOpaqueHeaderValue(audience) || len(privateKey) != ed25519.PrivateKeySize || clock == nil || random == nil {
		return nil, ErrInvalidConfiguration
	}
	derived := ed25519.NewKeyFromSeed(privateKey.Seed())
	if subtle.ConstantTimeCompare(privateKey, derived) != 1 {
		return nil, ErrInvalidConfiguration
	}
	return &Signer{
		keyID:      keyID,
		audience:   audience,
		privateKey: append(ed25519.PrivateKey(nil), privateKey...),
		clock:      clock,
		random:     random,
	}, nil
}

// Sign hashes body and signs req without reading or replacing req.Body.
func (s *Signer) Sign(req *http.Request, body []byte) error {
	in, err := requestInput(req)
	if err != nil {
		return err
	}
	for _, name := range workloadHeaders {
		if hasHeader(req.Header, name) {
			return fmt.Errorf("%w: %s", ErrCredentialsPresent, name)
		}
	}

	var nonceBytes [16]byte
	if _, err := io.ReadFull(s.random, nonceBytes[:]); err != nil {
		return fmt.Errorf("%w: %w", ErrNonceSource, err)
	}
	timestamp := s.clock.Now().Unix()
	if timestamp <= 0 {
		return fmt.Errorf("%w: clock", ErrInvalidConfiguration)
	}
	digest := sha256.Sum256(body)

	in.keyID = s.keyID
	in.audience = s.audience
	in.timestamp = strconv.FormatInt(timestamp, 10)
	in.nonce = base64.RawURLEncoding.EncodeToString(nonceBytes[:])
	in.bodyDigest = hex.EncodeToString(digest[:])
	record, err := canonicalRecord(in)
	if err != nil {
		return err
	}
	signature := ed25519.Sign(s.privateKey, record)

	req.Header.Set(HeaderKeyID, in.keyID)
	req.Header.Set(HeaderAudience, in.audience)
	req.Header.Set(HeaderTimestamp, in.timestamp)
	req.Header.Set(HeaderNonce, in.nonce)
	req.Header.Set(HeaderBodySHA256, in.bodyDigest)
	req.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(signature))
	return nil
}

// SignRequest reads req.Body to sign it and restores an equivalent readable
// stream before returning. Sign is preferable when the caller already has the
// serialized bytes.
func (s *Signer) SignRequest(req *http.Request) error {
	body, err := readRequestBody(req)
	if err != nil {
		return err
	}
	return s.Sign(req, body)
}
