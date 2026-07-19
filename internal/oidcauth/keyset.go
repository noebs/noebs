package oidcauth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const maxJWKSBytes = 1 << 20

type StaticKeySet struct {
	keys map[string]*rsa.PublicKey
}

func NewStaticKeySet(keys map[string]*rsa.PublicKey) (*StaticKeySet, error) {
	copyKeys, err := copyAndValidateKeys(keys)
	if err != nil {
		return nil, ErrInvalidConfiguration
	}
	return &StaticKeySet{keys: copyKeys}, nil
}

func (s *StaticKeySet) Key(_ context.Context, keyID string) (*rsa.PublicKey, error) {
	key, exists := s.keys[keyID]
	if !exists {
		return nil, ErrUnknownKey
	}
	return key, nil
}

type RemoteKeySetConfig struct {
	URL                       string
	Client                    *http.Client
	RefreshInterval           time.Duration
	UnknownKeyRefreshInterval time.Duration
	Clock                     Clock
}

// RemoteKeySet keeps verified RSA signing keys in memory. Expired caches fail
// closed if the JWKS endpoint cannot be refreshed, while unknown key IDs cause
// at most one refresh per configured interval.
type RemoteKeySet struct {
	url                       string
	client                    *http.Client
	refreshInterval           time.Duration
	unknownKeyRefreshInterval time.Duration
	clock                     Clock

	mu                  sync.RWMutex
	keys                map[string]*rsa.PublicKey
	refreshedAt         time.Time
	unknownKeyRefreshAt time.Time
	refreshFailureAt    time.Time
	refreshMu           sync.Mutex
}

func NewRemoteKeySet(config RemoteKeySetConfig) (*RemoteKeySet, error) {
	parsedURL, err := url.Parse(config.URL)
	if err != nil || !parsedURL.IsAbs() || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.Fragment != "" ||
		config.Client == nil || config.RefreshInterval <= 0 || config.UnknownKeyRefreshInterval <= 0 || config.Clock == nil {
		return nil, ErrInvalidConfiguration
	}
	return &RemoteKeySet{
		url:                       config.URL,
		client:                    config.Client,
		refreshInterval:           config.RefreshInterval,
		unknownKeyRefreshInterval: config.UnknownKeyRefreshInterval,
		clock:                     config.Clock,
		keys:                      make(map[string]*rsa.PublicKey),
	}, nil
}

func (s *RemoteKeySet) Key(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	if keyID == "" {
		return nil, ErrUnknownKey
	}
	if key, done, err := s.cached(keyID, s.clock.Now()); done {
		return key, err
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	now := s.clock.Now()
	if key, done, err := s.cached(keyID, now); done {
		return key, err
	}
	keys, err := s.fetch(ctx)
	if err != nil {
		s.mu.Lock()
		s.refreshFailureAt = now
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %w", ErrKeySetUnavailable, err)
	}
	s.mu.Lock()
	s.keys = keys
	s.refreshedAt = now
	s.refreshFailureAt = time.Time{}
	key, exists := keys[keyID]
	if !exists {
		s.unknownKeyRefreshAt = now
	}
	s.mu.Unlock()
	if !exists {
		return nil, ErrUnknownKey
	}
	return key, nil
}

func (s *RemoteKeySet) cached(keyID string, now time.Time) (*rsa.PublicKey, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, exists := s.keys[keyID]
	cacheFresh := !s.refreshedAt.IsZero() && now.Before(s.refreshedAt.Add(s.refreshInterval))
	if exists && cacheFresh {
		return key, true, nil
	}
	if !s.refreshFailureAt.IsZero() && now.Before(s.refreshFailureAt.Add(s.unknownKeyRefreshInterval)) {
		return nil, true, ErrKeySetUnavailable
	}
	if !exists && cacheFresh && !s.unknownKeyRefreshAt.IsZero() &&
		now.Before(s.unknownKeyRefreshAt.Add(s.unknownKeyRefreshInterval)) {
		return nil, true, ErrUnknownKey
	}
	return nil, false, nil
}

func (s *RemoteKeySet) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP status %d", ErrInvalidJWKS, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxJWKSBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxJWKSBytes {
		return nil, ErrInvalidJWKS
	}
	return parseJWKS(body)
}

type jsonWebKeySet struct {
	Keys []jsonWebKey `json:"keys"`
}

type jsonWebKey struct {
	KeyID     string `json:"kid"`
	KeyType   string `json:"kty"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
}

func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var set jsonWebKeySet
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidJWKS, err)
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, key := range set.Keys {
		if key.KeyType != "RSA" || key.Use != "sig" || key.Algorithm != jwtRS256 {
			continue
		}
		if key.KeyID == "" {
			return nil, ErrInvalidJWKS
		}
		if _, duplicate := keys[key.KeyID]; duplicate {
			return nil, ErrInvalidJWKS
		}
		publicKey, err := rsaKey(key.Modulus, key.Exponent)
		if err != nil {
			return nil, err
		}
		keys[key.KeyID] = publicKey
	}
	if len(keys) == 0 {
		return nil, ErrInvalidJWKS
	}
	return keys, nil
}

const jwtRS256 = "RS256"

func rsaKey(rawModulus, rawExponent string) (*rsa.PublicKey, error) {
	modulus, err := decodeBase64URLUInt(rawModulus)
	if err != nil || len(modulus) == 0 || modulus[0] == 0 {
		return nil, ErrInvalidJWKS
	}
	exponentBytes, err := decodeBase64URLUInt(rawExponent)
	if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 || exponentBytes[0] == 0 {
		return nil, ErrInvalidJWKS
	}
	exponent := new(big.Int).SetBytes(exponentBytes)
	if !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64() > int64(^uint(0)>>1) || exponent.Bit(0) == 0 {
		return nil, ErrInvalidJWKS
	}
	key := &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: int(exponent.Int64())}
	if !validRSAKey(key) {
		return nil, ErrInvalidJWKS
	}
	return key, nil
}

func decodeBase64URLUInt(raw string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return nil, ErrInvalidJWKS
	}
	return decoded, nil
}

func copyAndValidateKeys(keys map[string]*rsa.PublicKey) (map[string]*rsa.PublicKey, error) {
	if len(keys) == 0 {
		return nil, ErrInvalidConfiguration
	}
	copyKeys := make(map[string]*rsa.PublicKey, len(keys))
	for keyID, key := range keys {
		if keyID == "" || !validRSAKey(key) {
			return nil, ErrInvalidConfiguration
		}
		copyKeys[keyID] = &rsa.PublicKey{N: new(big.Int).Set(key.N), E: key.E}
	}
	return copyKeys, nil
}

func validRSAKey(key *rsa.PublicKey) bool {
	return key != nil && key.N != nil && key.N.Sign() > 0 && key.N.BitLen() >= 2048 && key.E >= 3 && key.E&1 == 1
}
