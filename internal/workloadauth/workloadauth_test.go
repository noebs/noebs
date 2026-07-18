package workloadauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testNow = time.Unix(1_784_350_800, 0).UTC()

const (
	testKeyID    = "gateway-2026-07"
	testAudience = "consumer-api"
	testCaller   = "api-gateway"
)

var testNonce = [16]byte{
	0xf0, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7,
	0xf8, 0xf9, 0xfa, 0xfb, 0xfc, 0xfd, 0xfe, 0xff,
}

type fixedClock struct {
	now time.Time
}

func (c *fixedClock) Now() time.Time { return c.now }

type memoryNonceStore struct {
	mu      sync.Mutex
	clock   Clock
	entries map[string]time.Time
}

func newMemoryNonceStore(clock Clock) *memoryNonceStore {
	return &memoryNonceStore{clock: clock, entries: make(map[string]time.Time)}
}

func (s *memoryNonceStore) Use(_ context.Context, keyID, audience, nonce string, expiresAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	for key, expiry := range s.entries {
		if !expiry.After(now) {
			delete(s.entries, key)
		}
	}
	key := keyID + "\x00" + audience + "\x00" + nonce
	if _, exists := s.entries[key]; exists {
		return false, nil
	}
	if !expiresAt.After(now) {
		return false, errors.New("nonce expiry is not in the future")
	}
	s.entries[key] = expiresAt
	return true, nil
}

func testKeys() (ed25519.PrivateKey, Registry) {
	seed := sha256.Sum256([]byte("noebs workload auth fixed vector key"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	otherSeed := sha256.Sum256([]byte("noebs workload auth other test key"))
	otherPrivateKey := ed25519.NewKeyFromSeed(otherSeed[:])
	return privateKey, Registry{
		testKeyID: {
			Caller:    testCaller,
			PublicKey: privateKey.Public().(ed25519.PublicKey),
		},
		"other-2026-07": {
			Caller:    "other-workload",
			PublicKey: otherPrivateKey.Public().(ed25519.PublicKey),
		},
	}
}

func fixedRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(
		http.MethodPost,
		"https://consumer.internal/v1/payments/a%2Fb?z=last&z=first&empty=",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", `Application/JSON; profile="urn:noebs:v1"; Charset="UTF-8"`)
	req.Header.Set(HeaderRequestID, "req-01J2Y7K8V9M0N1P2Q3R4S5T6U7")
	req.Header.Set(HeaderTenantID, "tenant-alpha")
	req.Header.Set(HeaderUserID, "42")
	req.Header.Set(HeaderMobile, "+249912345678")
	req.Header.Set(HeaderSessionEpoch, "7")
	req.Header.Set(HeaderSessionToken, "session-token-secret")
	req.Header.Set(HeaderSourceIP, "100.64.0.10")
	req.Header.Set(HeaderAdminIdentity, "gateway-admin")
	req.Header.Set(HeaderAdminRole, "admin")
	req.Header.Set(HeaderAdminPermissions, "cards:read,cards:write")
	return req
}

func signFixedRequest(t *testing.T, clock Clock, body []byte) *http.Request {
	t.Helper()
	privateKey, _ := testKeys()
	signer, err := NewSigner(testKeyID, testAudience, privateKey, clock, bytes.NewReader(testNonce[:]))
	if err != nil {
		t.Fatal(err)
	}
	req := fixedRequest(t, body)
	if err := signer.Sign(req, body); err != nil {
		t.Fatal(err)
	}
	return req
}

func newTestVerifier(t *testing.T, clock Clock) *Verifier {
	t.Helper()
	_, registry := testKeys()
	verifier, err := NewVerifier(testAudience, registry, clock, newMemoryNonceStore(clock))
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func bodyDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func TestV1FixedVector(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte(`{"amount":12345,"currency":"SDG"}`)
	req := signFixedRequest(t, clock, body)

	in, err := requestInput(req)
	if err != nil {
		t.Fatal(err)
	}
	in.keyID = req.Header.Get(HeaderKeyID)
	in.audience = req.Header.Get(HeaderAudience)
	in.timestamp = req.Header.Get(HeaderTimestamp)
	in.nonce = req.Header.Get(HeaderNonce)
	in.bodyDigest = req.Header.Get(HeaderBodySHA256)
	record, err := canonicalRecord(in)
	if err != nil {
		t.Fatal(err)
	}
	recordHash := sha256.Sum256(record)
	privateKey, _ := testKeys()
	publicKey := privateKey.Public().(ed25519.PublicKey)

	const wantRecordSHA256 = "1bf50794bbb89e211f8fa1ba66a25fc94c9d7320a89ac8bb7d01575472893e39"
	const wantBodySHA256 = "c3c7a11dd4e24ad697b72fc5b10c42f8f22bba8ff085c00c9b6bc8c180c834f1"
	const wantPublicKey = "f8c480a47989235e722e0acadf3e6d3a7116046af4614ee209cb5334b3a7ba64"
	const wantSignature = "IFZzeXvStclPaq9w7nEyL5fnoZkBSkyQ12t3k4bQm96raTZKrydx3JsPsJJvNUHW5vM2DpC-U6iQpZ2p8GniAA"
	if got := hex.EncodeToString(recordHash[:]); got != wantRecordSHA256 {
		t.Fatalf("canonical record SHA-256 = %q", got)
	}
	if got := req.Header.Get(HeaderBodySHA256); got != wantBodySHA256 {
		t.Fatalf("body SHA-256 = %q", got)
	}
	if got := hex.EncodeToString(publicKey); got != wantPublicKey {
		t.Fatalf("public key = %q", got)
	}
	if got := req.Header.Get(HeaderSignature); got != wantSignature {
		t.Fatalf("signature = %q", got)
	}

	fields := decodeCanonicalFields(t, record)
	wantFields := []string{
		VersionMagic,
		testKeyID,
		testAudience,
		fmt.Sprint(testNow.Unix()),
		base64.RawURLEncoding.EncodeToString(testNonce[:]),
		http.MethodPost,
		"/v1/payments/a%2Fb?z=last&z=first&empty=",
		`application/json; charset=UTF-8; profile="urn:noebs:v1"`,
		wantBodySHA256,
		"req-01J2Y7K8V9M0N1P2Q3R4S5T6U7",
		"tenant-alpha",
		"42",
		"+249912345678",
		"7",
		"session-token-secret",
		"100.64.0.10",
		"gateway-admin",
		"admin",
		"cards:read,cards:write",
	}
	if len(fields) != len(wantFields) {
		t.Fatalf("field count = %d, want %d", len(fields), len(wantFields))
	}
	for i := range fields {
		if fields[i] != wantFields[i] {
			t.Fatalf("field %d = %q, want %q", i, fields[i], wantFields[i])
		}
	}

	principal, err := newTestVerifier(t, clock).Verify(req, body)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Caller != testCaller || principal.KeyID != testKeyID || principal.Audience != testAudience || principal.RequestID != wantFields[9] || principal.Nonce != wantFields[4] || !principal.SignedAt.Equal(testNow) {
		t.Fatalf("unexpected principal: %+v", principal)
	}
}

func decodeCanonicalFields(t *testing.T, record []byte) []string {
	t.Helper()
	var fields []string
	for len(record) != 0 {
		if len(record) < 4 {
			t.Fatal("truncated canonical length")
		}
		length := int(binary.BigEndian.Uint32(record[:4]))
		record = record[4:]
		if length > len(record) {
			t.Fatal("truncated canonical value")
		}
		fields = append(fields, string(record[:length]))
		record = record[length:]
	}
	return fields
}

func TestEmptyIdentityValuesRemainExplicitFields(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://consumer.internal/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderRequestID, "req-empty-identities")
	in, err := requestInput(req)
	if err != nil {
		t.Fatal(err)
	}
	in.keyID = testKeyID
	in.audience = testAudience
	in.timestamp = fmt.Sprint(testNow.Unix())
	in.nonce = base64.RawURLEncoding.EncodeToString(testNonce[:])
	in.bodyDigest = bodyDigest(nil)
	record, err := canonicalRecord(in)
	if err != nil {
		t.Fatal(err)
	}
	fields := decodeCanonicalFields(t, record)
	if len(fields) != 19 {
		t.Fatalf("field count = %d, want 19", len(fields))
	}
	for i := 10; i < 19; i++ {
		if fields[i] != "" {
			t.Fatalf("identity field %d = %q, want explicit empty value", i, fields[i])
		}
	}
}

func TestEveryCanonicalFieldIsBound(t *testing.T) {
	body := []byte(`{"amount":12345,"currency":"SDG"}`)
	otherNonce := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 16))
	otherBody := []byte(`{"amount":12346,"currency":"SDG"}`)
	otherDigest := bodyDigest(otherBody)

	tests := []struct {
		name   string
		mutate func(*http.Request) []byte
	}{
		{"key ID", func(r *http.Request) []byte { r.Header.Set(HeaderKeyID, "other-2026-07"); return body }},
		{"audience", func(r *http.Request) []byte { r.Header.Set(HeaderAudience, "card-vault"); return body }},
		{"timestamp", func(r *http.Request) []byte { r.Header.Set(HeaderTimestamp, fmt.Sprint(testNow.Unix()-1)); return body }},
		{"nonce", func(r *http.Request) []byte { r.Header.Set(HeaderNonce, otherNonce); return body }},
		{"method", func(r *http.Request) []byte { r.Method = http.MethodPut; return body }},
		{"escaped path", func(r *http.Request) []byte { r.URL.Path = "/v1/payments/other"; r.URL.RawPath = ""; return body }},
		{"escaped path byte case", func(r *http.Request) []byte {
			r.URL.RawPath = strings.Replace(r.URL.RawPath, "%2F", "%2f", 1)
			return body
		}},
		{"raw query", func(r *http.Request) []byte { r.URL.RawQuery = "z=first&z=last&empty="; return body }},
		{"content type", func(r *http.Request) []byte {
			r.Header.Set("Content-Type", "application/json; charset=UTF-16; profile=urn:noebs:v1")
			return body
		}},
		{"body digest", func(r *http.Request) []byte { r.Header.Set(HeaderBodySHA256, otherDigest); return otherBody }},
		{"request ID", func(r *http.Request) []byte { r.Header.Set(HeaderRequestID, "req-other"); return body }},
		{"tenant", func(r *http.Request) []byte { r.Header.Set(HeaderTenantID, "tenant-beta"); return body }},
		{"user", func(r *http.Request) []byte { r.Header.Set(HeaderUserID, "43"); return body }},
		{"mobile", func(r *http.Request) []byte { r.Header.Set(HeaderMobile, "+249912345679"); return body }},
		{"session epoch", func(r *http.Request) []byte { r.Header.Set(HeaderSessionEpoch, "8"); return body }},
		{"session token", func(r *http.Request) []byte { r.Header.Set(HeaderSessionToken, "other-session"); return body }},
		{"source IP", func(r *http.Request) []byte { r.Header.Set(HeaderSourceIP, "100.64.0.11"); return body }},
		{"admin identity", func(r *http.Request) []byte { r.Header.Set(HeaderAdminIdentity, "other-admin"); return body }},
		{"admin role", func(r *http.Request) []byte { r.Header.Set(HeaderAdminRole, "viewer"); return body }},
		{"admin permissions", func(r *http.Request) []byte { r.Header.Set(HeaderAdminPermissions, "cards:read"); return body }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &fixedClock{now: testNow}
			req := signFixedRequest(t, clock, body)
			verifyBody := test.mutate(req)
			if _, err := newTestVerifier(t, clock).Verify(req, verifyBody); err == nil {
				t.Fatal("mutated signed field was accepted")
			}
		})
	}
}

func TestContentTypeNormalization(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("{}")
	req := signFixedRequest(t, clock, body)
	req.Header.Set("Content-Type", `APPLICATION/JSON ; PROFILE="urn:noebs:v1" ; charset=UTF-8`)
	if _, err := newTestVerifier(t, clock).Verify(req, body); err != nil {
		t.Fatalf("equivalent normalized content type rejected: %v", err)
	}

	tests := []string{
		"application/json; charset=UTF-8; charset=UTF-16",
		"not a media type",
		"application/json\r\nX-Injected: yes",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			req := signFixedRequest(t, clock, body)
			req.Header["Content-Type"] = []string{raw}
			if _, err := newTestVerifier(t, clock).Verify(req, body); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want invalid request", err)
			}
		})
	}
}

func TestReplayClaimIsAtomic(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("payload")
	req := signFixedRequest(t, clock, body)
	verifier := newTestVerifier(t, clock)

	const attempts = 64
	var successes atomic.Int32
	var replays atomic.Int32
	var unexpected atomic.Value
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := verifier.Verify(req, body)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrReplay):
				replays.Add(1)
			default:
				unexpected.Store(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if err, _ := unexpected.Load().(error); err != nil {
		t.Fatal(err)
	}
	if successes.Load() != 1 || replays.Load() != attempts-1 {
		t.Fatalf("successes=%d replays=%d", successes.Load(), replays.Load())
	}
}

func TestTimestampWindow(t *testing.T) {
	body := []byte("payload")
	tests := []struct {
		name    string
		now     time.Time
		wantErr error
	}{
		{"thirty seconds old accepted", testNow.Add(30 * time.Second), nil},
		{"older than thirty seconds rejected", testNow.Add(31 * time.Second), ErrTimestampExpired},
		{"five seconds ahead accepted", testNow.Add(-5 * time.Second), nil},
		{"more than five seconds ahead rejected", testNow.Add(-6 * time.Second), ErrTimestampInFuture},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signingClock := &fixedClock{now: testNow}
			req := signFixedRequest(t, signingClock, body)
			verifyClock := &fixedClock{now: test.now}
			_, err := newTestVerifier(t, verifyClock).Verify(req, body)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestRequestBodiesRemainReadable(t *testing.T) {
	clock := &fixedClock{now: testNow}
	privateKey, registry := testKeys()
	body := []byte("body that must survive signing and verification")
	signer, err := NewSigner(testKeyID, testAudience, privateKey, clock, bytes.NewReader(testNonce[:]))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(testAudience, registry, clock, newMemoryNonceStore(clock))
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, "https://consumer.internal/test", bytes.NewBuffer(body))
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody = nil
	req.Header.Set(HeaderRequestID, "req-readable")
	if err := signer.SignRequest(req); err != nil {
		t.Fatal(err)
	}
	assertReadableBody(t, req, body)
	if _, err := verifier.VerifyRequest(req); err != nil {
		t.Fatal(err)
	}
	assertReadableBody(t, req, body)
}

func TestPrecomputedBodyDoesNotReadRequestStream(t *testing.T) {
	clock := &fixedClock{now: testNow}
	body := []byte("precomputed")
	req := fixedRequest(t, body)
	req.GetBody = nil
	req.Body = panicReader{}
	privateKey, _ := testKeys()
	signer, err := NewSigner(testKeyID, testAudience, privateKey, clock, bytes.NewReader(testNonce[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Sign(req, body); err != nil {
		t.Fatal(err)
	}
	if _, err := newTestVerifier(t, clock).Verify(req, body); err != nil {
		t.Fatal(err)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("request body was read") }
func (panicReader) Close() error             { return nil }

func assertReadableBody(t *testing.T, req *http.Request, want []byte) {
	t.Helper()
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("body = %q, want %q", got, want)
	}
	req.Body = io.NopCloser(bytes.NewReader(got))
}

func TestBodyReadErrorRestoresConsumedPrefix(t *testing.T) {
	boom := errors.New("boom")
	req, err := http.NewRequest(http.MethodPost, "https://consumer.internal/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Body = &oneErrorReader{data: []byte("partial"), err: boom}
	_, err = readRequestBody(req)
	if !errors.Is(err, ErrBodyRead) {
		t.Fatalf("error = %v, want body read error", err)
	}
	got, readErr := io.ReadAll(req.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "partial" {
		t.Fatalf("restored body = %q", got)
	}
}

type oneErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *oneErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), r.err
}

func (*oneErrorReader) Close() error { return nil }

func TestStripCredentialsAndRejectRedirect(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://external.example/", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range identityHeaders {
		req.Header[strings.ToLower(name)] = []string{"sensitive"}
	}
	for _, name := range workloadHeaders {
		req.Header[strings.ToLower(name)] = []string{"credential"}
	}
	req.Header.Set(HeaderRequestID, "req-preserved")
	req.Header.Set("Authorization", "Bearer unrelated")
	StripCredentials(req)
	for name := range req.Header {
		if isCredentialHeader(name) {
			t.Fatalf("credential header %q survived stripping", name)
		}
	}
	if req.Header.Get(HeaderRequestID) != "req-preserved" || req.Header.Get("Authorization") != "Bearer unrelated" {
		t.Fatal("non-Noebs headers were changed")
	}
	if err := RejectRedirect(req, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect error = %v", err)
	}
}
