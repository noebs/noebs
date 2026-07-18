package workloadauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConstructorsRejectIncompleteConfiguration(t *testing.T) {
	clock := &fixedClock{now: testNow}
	privateKey, registry := testKeys()
	nonces := newMemoryNonceStore(clock)

	signerTests := []struct {
		name       string
		keyID      string
		audience   string
		privateKey ed25519.PrivateKey
		clock      Clock
		random     io.Reader
	}{
		{"missing key ID", "", testAudience, privateKey, clock, bytes.NewReader(testNonce[:])},
		{"invalid key ID", "key id", testAudience, privateKey, clock, bytes.NewReader(testNonce[:])},
		{"missing audience", testKeyID, "", privateKey, clock, bytes.NewReader(testNonce[:])},
		{"invalid audience", testKeyID, "consumer api", privateKey, clock, bytes.NewReader(testNonce[:])},
		{"short private key", testKeyID, testAudience, privateKey[:31], clock, bytes.NewReader(testNonce[:])},
		{"inconsistent private key", testKeyID, testAudience, corruptPrivateKey(privateKey), clock, bytes.NewReader(testNonce[:])},
		{"missing clock", testKeyID, testAudience, privateKey, nil, bytes.NewReader(testNonce[:])},
		{"missing random", testKeyID, testAudience, privateKey, clock, nil},
	}
	for _, test := range signerTests {
		t.Run("signer "+test.name, func(t *testing.T) {
			if _, err := NewSigner(test.keyID, test.audience, test.privateKey, test.clock, test.random); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("error = %v, want invalid configuration", err)
			}
		})
	}

	verifierTests := []struct {
		name     string
		audience string
		registry Registry
		clock    Clock
		nonces   NonceStore
	}{
		{"missing audience", "", registry, clock, nonces},
		{"invalid audience", "consumer api", registry, clock, nonces},
		{"empty registry", testAudience, Registry{}, clock, nonces},
		{"invalid key ID", testAudience, Registry{"key id": registry[testKeyID]}, clock, nonces},
		{"missing caller", testAudience, Registry{testKeyID: {PublicKey: registry[testKeyID].PublicKey}}, clock, nonces},
		{"invalid caller", testAudience, Registry{testKeyID: {Caller: "api gateway", PublicKey: registry[testKeyID].PublicKey}}, clock, nonces},
		{"short public key", testAudience, Registry{testKeyID: {Caller: testCaller, PublicKey: registry[testKeyID].PublicKey[:12]}}, clock, nonces},
		{"missing clock", testAudience, registry, nil, nonces},
		{"missing nonce store", testAudience, registry, clock, nil},
	}
	for _, test := range verifierTests {
		t.Run("verifier "+test.name, func(t *testing.T) {
			if _, err := NewVerifier(test.audience, test.registry, test.clock, test.nonces); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("error = %v, want invalid configuration", err)
			}
		})
	}
}

func corruptPrivateKey(privateKey ed25519.PrivateKey) ed25519.PrivateKey {
	corrupt := append(ed25519.PrivateKey(nil), privateKey...)
	corrupt[len(corrupt)-1] ^= 0x01
	return corrupt
}

func TestVerifierRejectsMissingRequiredHeaders(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	for _, name := range []string{
		HeaderRequestID,
		HeaderKeyID,
		HeaderAudience,
		HeaderTimestamp,
		HeaderNonce,
		HeaderBodySHA256,
		HeaderSignature,
	} {
		t.Run(name, func(t *testing.T) {
			req := signFixedRequest(t, clock, body)
			deleteHeader(req.Header, name)
			if _, err := newTestVerifier(t, clock).Verify(req, body); !errors.Is(err, ErrMissingHeader) {
				t.Fatalf("error = %v, want missing header", err)
			}
		})
	}
}

func TestVerifierRejectsEveryDuplicateSignedHeader(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	names := []string{"Content-Type", HeaderRequestID}
	for _, name := range identityHeaders {
		names = append(names, name)
	}
	for _, name := range workloadHeaders {
		names = append(names, name)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			req := signFixedRequest(t, clock, body)
			req.Header[strings.ToLower(name)] = []string{req.Header.Get(name)}
			if _, err := newTestVerifier(t, clock).Verify(req, body); !errors.Is(err, ErrDuplicateHeader) {
				t.Fatalf("error = %v, want duplicate header", err)
			}
		})
	}
}

func TestVerifierRejectsMalformedFields(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	validNonce := base64.RawURLEncoding.EncodeToString(testNonce[:])
	validSignature := func() string {
		req := signFixedRequest(t, clock, body)
		return req.Header.Get(HeaderSignature)
	}()
	signatureBytes, err := base64.RawURLEncoding.DecodeString(validSignature)
	if err != nil {
		t.Fatal(err)
	}
	signatureBytes[0] ^= 0x80
	alteredSignature := base64.RawURLEncoding.EncodeToString(signatureBytes)

	tests := []struct {
		name    string
		mutate  func(*http.Request)
		wantErr error
	}{
		{"unknown key", func(r *http.Request) { r.Header.Set(HeaderKeyID, "unknown-2026-07") }, ErrUnknownKey},
		{"empty key", func(r *http.Request) { r.Header.Set(HeaderKeyID, "") }, ErrUnknownKey},
		{"wrong audience", func(r *http.Request) { r.Header.Set(HeaderAudience, "card-vault") }, ErrAudienceMismatch},
		{"empty audience", func(r *http.Request) { r.Header.Set(HeaderAudience, "") }, ErrAudienceMismatch},
		{"timestamp plus sign", func(r *http.Request) { r.Header.Set(HeaderTimestamp, "+"+fmt.Sprint(testNow.Unix())) }, ErrInvalidTimestamp},
		{"timestamp leading zero", func(r *http.Request) { r.Header.Set(HeaderTimestamp, "0"+fmt.Sprint(testNow.Unix())) }, ErrInvalidTimestamp},
		{"timestamp zero", func(r *http.Request) { r.Header.Set(HeaderTimestamp, "0") }, ErrInvalidTimestamp},
		{"timestamp text", func(r *http.Request) { r.Header.Set(HeaderTimestamp, "later") }, ErrInvalidTimestamp},
		{"nonce padded", func(r *http.Request) { r.Header.Set(HeaderNonce, validNonce+"==") }, ErrInvalidNonce},
		{"nonce too short", func(r *http.Request) { r.Header.Set(HeaderNonce, base64.RawURLEncoding.EncodeToString(testNonce[:15])) }, ErrInvalidNonce},
		{"nonce too long", func(r *http.Request) {
			r.Header.Set(HeaderNonce, base64.RawURLEncoding.EncodeToString(append(testNonce[:], 1)))
		}, ErrInvalidNonce},
		{"nonce standard alphabet", func(r *http.Request) { r.Header.Set(HeaderNonce, "/////////////////////w") }, ErrInvalidNonce},
		{"digest uppercase", func(r *http.Request) { r.Header.Set(HeaderBodySHA256, strings.ToUpper(r.Header.Get(HeaderBodySHA256))) }, ErrInvalidBodyDigest},
		{"digest short", func(r *http.Request) { r.Header.Set(HeaderBodySHA256, r.Header.Get(HeaderBodySHA256)[:63]) }, ErrInvalidBodyDigest},
		{"digest non-hex", func(r *http.Request) { r.Header.Set(HeaderBodySHA256, strings.Repeat("z", 64)) }, ErrInvalidBodyDigest},
		{"signature padded", func(r *http.Request) { r.Header.Set(HeaderSignature, validSignature+"==") }, ErrInvalidSignature},
		{"signature short", func(r *http.Request) {
			r.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(signatureBytes[:63]))
		}, ErrInvalidSignature},
		{"signature altered", func(r *http.Request) { r.Header.Set(HeaderSignature, alteredSignature) }, ErrInvalidSignature},
		{"empty request ID", func(r *http.Request) { r.Header.Set(HeaderRequestID, "") }, ErrInvalidRequest},
		{"spaced request ID", func(r *http.Request) { r.Header.Set(HeaderRequestID, "request id") }, ErrInvalidRequest},
		{"lowercase method", func(r *http.Request) { r.Method = "post" }, ErrInvalidRequest},
		{"empty method", func(r *http.Request) { r.Method = "" }, ErrInvalidRequest},
		{"invalid method", func(r *http.Request) { r.Method = "POST GET" }, ErrInvalidRequest},
		{"empty path", func(r *http.Request) { r.URL.Path = ""; r.URL.RawPath = "" }, ErrInvalidRequest},
		{"force query", func(r *http.Request) { r.URL.ForceQuery = true }, ErrInvalidRequest},
		{"fragment", func(r *http.Request) { r.URL.Fragment = "not-signed" }, ErrInvalidRequest},
		{"opaque URL", func(r *http.Request) { r.URL.Opaque = "//consumer.internal/test" }, ErrInvalidRequest},
		{"invalid raw path escape", func(r *http.Request) { r.URL.RawPath = "/v1/payments/a%ZZ" }, ErrInvalidRequest},
		{"raw path disagrees with path", func(r *http.Request) { r.URL.RawPath = "/v1/payments/different" }, ErrInvalidRequest},
		{"malformed content type", func(r *http.Request) { r.Header.Set("Content-Type", "not a media type") }, ErrInvalidRequest},
		{"header newline", func(r *http.Request) { r.Header[HeaderSessionToken] = []string{"token\r\nInjected: yes"} }, ErrInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := signFixedRequest(t, clock, body)
			test.mutate(req)
			if _, err := newTestVerifier(t, clock).Verify(req, body); !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestBodyMismatchAndInvalidSignatureDoNotClaimNonce(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	underlying := newMemoryNonceStore(clock)
	store := &countingNonceStore{underlying: underlying}
	_, registry := testKeys()
	verifier, err := NewVerifier(testAudience, registry, clock, store)
	if err != nil {
		t.Fatal(err)
	}

	req := signFixedRequest(t, clock, body)
	if _, err := verifier.Verify(req, []byte("different")); !errors.Is(err, ErrBodyDigestMismatch) {
		t.Fatalf("body error = %v", err)
	}
	if store.calls.Load() != 0 {
		t.Fatal("body mismatch claimed nonce")
	}

	signature := req.Header.Get(HeaderSignature)
	req.Header.Set(HeaderSignature, strings.Repeat("A", len(signature)))
	if _, err := verifier.Verify(req, body); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("signature error = %v", err)
	}
	if store.calls.Load() != 0 {
		t.Fatal("invalid signature claimed nonce")
	}

	req.Header.Set(HeaderSignature, signature)
	if _, err := verifier.Verify(req, body); err != nil {
		t.Fatal(err)
	}
	if store.calls.Load() != 1 {
		t.Fatalf("nonce claims = %d, want 1", store.calls.Load())
	}
	if _, err := verifier.Verify(req, body); !errors.Is(err, ErrReplay) {
		t.Fatalf("second verification error = %v, want replay", err)
	}
}

type countingNonceStore struct {
	underlying NonceStore
	calls      atomic.Int32
}

func (s *countingNonceStore) Use(ctx context.Context, keyID, audience, nonce string, expiresAt time.Time) (bool, error) {
	s.calls.Add(1)
	return s.underlying.Use(ctx, keyID, audience, nonce, expiresAt)
}

func TestNonceStoreFailureFailsClosedAndPreservesCause(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	req := signFixedRequest(t, clock, body)
	storeErr := errors.New("store unavailable")
	_, registry := testKeys()
	verifier, err := NewVerifier(testAudience, registry, clock, errorNonceStore{err: storeErr})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(req, body); !errors.Is(err, ErrNonceStore) || !errors.Is(err, storeErr) {
		t.Fatalf("error = %v, want nonce store error and cause", err)
	}
}

func TestNonceRetentionIsExactlyNinetySeconds(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	req := signFixedRequest(t, clock, body)
	store := &expiryNonceStore{}
	_, registry := testKeys()
	verifier, err := NewVerifier(testAudience, registry, clock, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(req, body); err != nil {
		t.Fatal(err)
	}
	if want := testNow.Add(90 * time.Second); !store.expiresAt.Equal(want) {
		t.Fatalf("nonce expiry = %s, want %s", store.expiresAt, want)
	}

	memory := newMemoryNonceStore(clock)
	used, err := memory.Use(context.Background(), testKeyID, testAudience, "nonce", testNow.Add(nonceRetention))
	if err != nil || !used {
		t.Fatalf("initial use = %v, %v", used, err)
	}
	clock.now = testNow.Add(89 * time.Second)
	used, err = memory.Use(context.Background(), testKeyID, testAudience, "nonce", testNow.Add(2*nonceRetention))
	if err != nil || used {
		t.Fatalf("use before expiry = %v, %v", used, err)
	}
	clock.now = testNow.Add(90 * time.Second)
	used, err = memory.Use(context.Background(), testKeyID, testAudience, "nonce", testNow.Add(2*nonceRetention))
	if err != nil || !used {
		t.Fatalf("use at expiry = %v, %v", used, err)
	}
}

type expiryNonceStore struct {
	expiresAt time.Time
}

func (s *expiryNonceStore) Use(_ context.Context, _, _, _ string, expiresAt time.Time) (bool, error) {
	s.expiresAt = expiresAt
	return true, nil
}

type errorNonceStore struct{ err error }

func (s errorNonceStore) Use(context.Context, string, string, string, time.Time) (bool, error) {
	return false, s.err
}

func TestSignerRejectsExistingCredentials(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	privateKey, _ := testKeys()
	for _, existing := range workloadHeaders {
		t.Run(existing, func(t *testing.T) {
			req := fixedRequest(t, body)
			req.Header[strings.ToLower(existing)] = []string{"stale-credential"}
			signer, err := NewSigner(testKeyID, testAudience, privateKey, clock, bytes.NewReader(testNonce[:]))
			if err != nil {
				t.Fatal(err)
			}
			if err := signer.Sign(req, body); !errors.Is(err, ErrCredentialsPresent) {
				t.Fatalf("error = %v, want credentials present", err)
			}
			for _, name := range workloadHeaders {
				if name != existing && hasHeader(req.Header, name) {
					t.Fatalf("partial credential %s was written", name)
				}
			}
		})
	}
}

func TestSignerRejectsAmbiguousContextBeforeWritingCredentials(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	req := fixedRequest(t, body)
	req.Header[strings.ToLower(HeaderUserID)] = []string{"42"}
	privateKey, _ := testKeys()
	signer, err := NewSigner(testKeyID, testAudience, privateKey, clock, bytes.NewReader(testNonce[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Sign(req, body); !errors.Is(err, ErrDuplicateHeader) {
		t.Fatalf("error = %v, want duplicate header", err)
	}
	for _, name := range workloadHeaders {
		if hasHeader(req.Header, name) {
			t.Fatalf("partial credential %s was written", name)
		}
	}
}

func TestNonceSourceFailureWritesNoCredentials(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	req := fixedRequest(t, body)
	privateKey, _ := testKeys()
	sourceErr := errors.New("entropy unavailable")
	signer, err := NewSigner(testKeyID, testAudience, privateKey, clock, errorReader{err: sourceErr})
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Sign(req, body); !errors.Is(err, ErrNonceSource) || !errors.Is(err, sourceErr) {
		t.Fatalf("error = %v, want nonce source error and cause", err)
	}
	for _, name := range workloadHeaders {
		if hasHeader(req.Header, name) {
			t.Fatalf("partial credential %s was written", name)
		}
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestVerifierCopiesRegistryAndPublicKeys(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	privateKey, registry := testKeys()
	signer, err := NewSigner(testKeyID, testAudience, privateKey, clock, bytes.NewReader(testNonce[:]))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(testAudience, registry, clock, newMemoryNonceStore(clock))
	if err != nil {
		t.Fatal(err)
	}
	registered := registry[testKeyID]
	for i := range registered.PublicKey {
		registered.PublicKey[i] = 0
	}
	delete(registry, testKeyID)

	req := fixedRequest(t, body)
	if err := signer.Sign(req, body); err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(req, body)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Caller != testCaller {
		t.Fatalf("caller = %q", principal.Caller)
	}
}

func TestEmptyContentTypeIsSignedWithoutDefault(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	privateKey, _ := testKeys()
	signer, err := NewSigner(testKeyID, testAudience, privateKey, clock, bytes.NewReader(testNonce[:]))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, "https://consumer.internal/test", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderRequestID, "req-no-content-type")
	if err := signer.Sign(req, body); err != nil {
		t.Fatal(err)
	}
	if _, err := newTestVerifier(t, clock).Verify(req, body); err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if _, err := newTestVerifier(t, clock).Verify(req, body); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("error = %v, want invalid signature", err)
	}
}

func TestRejectRedirectPreventsCredentialForwarding(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetHits.Add(1)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := &http.Client{CheckRedirect: RejectRedirect}
	req, err := http.NewRequest(http.MethodGet, source.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderSessionToken, "must-not-leave")
	req.Header.Set(HeaderSignature, "must-not-leave")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if targetHits.Load() != 0 {
		t.Fatal("redirect target received the signed request")
	}
}

func deleteHeader(headers http.Header, name string) {
	for key := range headers {
		if strings.EqualFold(key, name) {
			delete(headers, key)
		}
	}
}
