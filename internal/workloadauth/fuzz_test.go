package workloadauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

func FuzzFieldParsing(f *testing.F) {
	for _, seed := range []string{
		"",
		"0",
		strconv.FormatInt(testNow.Unix(), 10),
		base64.RawURLEncoding.EncodeToString(testNonce[:]),
		bodyDigest([]byte("payload")),
		"IFZzeXvStclPaq9w7nEyL5fnoZkBSkyQ12t3k4bQm96raTZKrydx3JsPsJJvNUHW5vM2DpC-U6iQpZ2p8GniAA",
		"/////////////////////w==",
		"\x00\r\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 2048 {
			t.Skip()
		}
		if timestamp, err := parseTimestamp(raw); err == nil && strconv.FormatInt(timestamp, 10) != raw {
			t.Fatalf("timestamp parser accepted non-canonical %q", raw)
		}
		if nonce, err := parseNonce(raw); err == nil && base64.RawURLEncoding.EncodeToString(nonce[:]) != raw {
			t.Fatalf("nonce parser accepted non-canonical %q", raw)
		}
		if digest, err := parseBodyDigest(raw); err == nil && hex.EncodeToString(digest[:]) != raw {
			t.Fatalf("digest parser accepted non-canonical %q", raw)
		}
		if signature, err := parseSignature(raw); err == nil && base64.RawURLEncoding.EncodeToString(signature) != raw {
			t.Fatalf("signature parser accepted non-canonical %q", raw)
		}
	})
}

func FuzzCanonicalization(f *testing.F) {
	f.Add(
		http.MethodPost,
		"/v1/cards/a/b",
		"/v1/cards/a%2Fb",
		"z=last&z=first",
		`Application/JSON; Charset="UTF-8"`,
		"req-fixed",
		"session-token",
	)
	f.Add("post", "", "", "", "not a media type", "request id", "\r\n")
	f.Fuzz(func(t *testing.T, method, path, rawPath, rawQuery, contentType, requestID, sessionToken string) {
		for _, value := range []string{method, path, rawPath, rawQuery, contentType, requestID, sessionToken} {
			if len(value) > 2048 {
				t.Skip()
			}
		}
		req := &http.Request{
			Method: method,
			URL: &url.URL{
				Path:     path,
				RawPath:  rawPath,
				RawQuery: rawQuery,
			},
			Header: make(http.Header),
		}
		req.Header["Content-Type"] = []string{contentType}
		req.Header[HeaderRequestID] = []string{requestID}
		req.Header[HeaderSubject] = []string{sessionToken}

		first, err := requestInput(req)
		if err != nil {
			return
		}
		second, err := requestInput(req)
		if err != nil {
			t.Fatalf("same request became invalid: %v", err)
		}
		first.keyID, second.keyID = testKeyID, testKeyID
		first.audience, second.audience = testAudience, testAudience
		first.timestamp, second.timestamp = strconv.FormatInt(testNow.Unix(), 10), strconv.FormatInt(testNow.Unix(), 10)
		first.nonce, second.nonce = base64.RawURLEncoding.EncodeToString(testNonce[:]), base64.RawURLEncoding.EncodeToString(testNonce[:])
		first.bodyDigest, second.bodyDigest = bodyDigest(nil), bodyDigest(nil)
		firstRecord, err := canonicalRecord(first)
		if err != nil {
			t.Fatal(err)
		}
		secondRecord, err := canonicalRecord(second)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstRecord, secondRecord) {
			t.Fatal("canonicalization is not deterministic")
		}
		fields := decodeCanonicalFields(t, firstRecord)
		if len(fields) != 20 {
			t.Fatalf("canonical field count = %d", len(fields))
		}
		if fields[5] != method || fields[6] != first.target || fields[7] != first.contentType || fields[9] != requestID || fields[12] != sessionToken {
			t.Fatal("canonical record changed a bound request value")
		}
	})
}

func FuzzReplay(f *testing.F) {
	f.Add([]byte("payload"), "z=last&z=first")
	f.Add([]byte{}, "")
	f.Add([]byte{0, 1, 2, 3}, "unicode=سلام")
	f.Fuzz(func(t *testing.T, body []byte, queryValue string) {
		if len(body) > 4096 || len(queryValue) > 1024 {
			t.Skip()
		}
		clock := &fixedClock{now: testNow}
		privateKey, registry := testKeys()
		nonceSeed := sha256.Sum256(append(append([]byte(nil), body...), queryValue...))
		signer, err := NewSigner(testKeyID, testAudience, privateKey, clock, bytes.NewReader(nonceSeed[:16]))
		if err != nil {
			t.Fatal(err)
		}
		verifier, err := NewVerifier(testAudience, registry, clock, newMemoryNonceStore(clock))
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, "https://consumer.internal/v1/fuzz?value="+url.QueryEscape(queryValue), bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(HeaderRequestID, fmt.Sprintf("fuzz-%x", nonceSeed[:12]))
		req.Header.Set("Content-Type", "application/octet-stream")
		if err := signer.Sign(req, body); err != nil {
			t.Fatal(err)
		}
		if _, err := verifier.Verify(req, body); err != nil {
			t.Fatalf("first verification: %v", err)
		}
		if _, err := verifier.Verify(req, body); err != ErrReplay {
			t.Fatalf("second verification = %v, want replay", err)
		}
	})
}
