package workloadauth

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"mime"
	"net/http"
	"strings"
)

// V1 is a fixed sequence of nineteen UTF-8 fields. Each field is framed as a
// four-byte unsigned big-endian byte length followed by those bytes:
//
//	NOEBS-WORKLOAD-V1, key_id, audience, unix_seconds, nonce, method,
//	escaped_path_and_raw_query, normalized_content_type, body_sha256,
//	X-Request-ID, then the nine X-Noebs identity/admin/session/source values
//	in identityHeaders order.
//
// Empty identity values are zero-length fields; fields are never omitted.
type canonicalInput struct {
	keyID       string
	audience    string
	timestamp   string
	nonce       string
	method      string
	target      string
	contentType string
	bodyDigest  string
	requestID   string
	identity    [len(identityHeaders)]string
}

func canonicalRecord(in canonicalInput) ([]byte, error) {
	fields := [...]string{
		VersionMagic,
		in.keyID,
		in.audience,
		in.timestamp,
		in.nonce,
		in.method,
		in.target,
		in.contentType,
		in.bodyDigest,
		in.requestID,
		in.identity[0],
		in.identity[1],
		in.identity[2],
		in.identity[3],
		in.identity[4],
		in.identity[5],
		in.identity[6],
		in.identity[7],
		in.identity[8],
	}

	var size uint64
	for _, field := range fields {
		if uint64(len(field)) > math.MaxUint32 {
			return nil, ErrInvalidRequest
		}
		size += 4 + uint64(len(field))
	}
	if size > uint64(int(^uint(0)>>1)) {
		return nil, ErrInvalidRequest
	}

	var record bytes.Buffer
	record.Grow(int(size))
	var length [4]byte
	for _, field := range fields {
		binary.BigEndian.PutUint32(length[:], uint32(len(field)))
		record.Write(length[:])
		record.WriteString(field)
	}
	return record.Bytes(), nil
}

func requestInput(req *http.Request) (canonicalInput, error) {
	if req == nil || req.URL == nil || req.Header == nil {
		return canonicalInput{}, ErrInvalidRequest
	}
	if !validUpperMethod(req.Method) {
		return canonicalInput{}, fmt.Errorf("%w: method", ErrInvalidRequest)
	}
	if req.URL.Opaque != "" || req.URL.Fragment != "" || req.URL.RawFragment != "" || req.URL.ForceQuery {
		return canonicalInput{}, fmt.Errorf("%w: request target", ErrInvalidRequest)
	}
	path := req.URL.EscapedPath()
	if req.URL.RawPath != "" && path != req.URL.RawPath {
		return canonicalInput{}, fmt.Errorf("%w: request target", ErrInvalidRequest)
	}
	if path == "" || path[0] != '/' {
		return canonicalInput{}, fmt.Errorf("%w: request target", ErrInvalidRequest)
	}
	target := path
	if req.URL.RawQuery != "" {
		target += "?" + req.URL.RawQuery
	}

	rawContentType, err := uniqueHeader(req.Header, "Content-Type", false)
	if err != nil {
		return canonicalInput{}, err
	}
	contentType, err := normalizeContentType(rawContentType)
	if err != nil {
		return canonicalInput{}, err
	}
	requestID, err := uniqueHeader(req.Header, HeaderRequestID, true)
	if err != nil {
		return canonicalInput{}, err
	}
	if !validOpaqueHeaderValue(requestID) {
		return canonicalInput{}, fmt.Errorf("%w: %s", ErrInvalidRequest, HeaderRequestID)
	}

	in := canonicalInput{
		method:      req.Method,
		target:      target,
		contentType: contentType,
		requestID:   requestID,
	}
	for i, name := range identityHeaders {
		value, err := uniqueHeader(req.Header, name, false)
		if err != nil {
			return canonicalInput{}, err
		}
		if !validHTTPHeaderValue(value) {
			return canonicalInput{}, fmt.Errorf("%w: %s", ErrInvalidRequest, name)
		}
		in.identity[i] = value
	}
	return in, nil
}

func normalizeContentType(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	mediaType, params, err := mime.ParseMediaType(raw)
	if err != nil {
		return "", fmt.Errorf("%w: Content-Type", ErrInvalidRequest)
	}
	normalized := mime.FormatMediaType(strings.ToLower(mediaType), params)
	if normalized == "" {
		return "", fmt.Errorf("%w: Content-Type", ErrInvalidRequest)
	}
	return normalized, nil
}

func uniqueHeader(headers http.Header, name string, required bool) (string, error) {
	var values []string
	found := false
	for key, current := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		found = true
		values = append(values, current...)
	}
	if len(values) > 1 {
		return "", fmt.Errorf("%w: %s", ErrDuplicateHeader, name)
	}
	if len(values) == 0 {
		if required || found {
			return "", fmt.Errorf("%w: %s", ErrMissingHeader, name)
		}
		return "", nil
	}
	return values[0], nil
}

func hasHeader(headers http.Header, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func validUpperMethod(method string) bool {
	if method == "" {
		return false
	}
	for i := 0; i < len(method); i++ {
		c := method[i]
		if c >= 'a' && c <= 'z' || !isTokenByte(c) {
			return false
		}
	}
	return true
}

func isTokenByte(c byte) bool {
	if c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
		return true
	}
	return strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))
}

func validOpaqueHeaderValue(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func validHTTPHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\r' || c == '\n' || c == 0x7f || c < 0x20 && c != '\t' {
			return false
		}
	}
	return true
}

func parseBodyDigest(raw string) ([32]byte, error) {
	var digest [32]byte
	if len(raw) != hex.EncodedLen(len(digest)) {
		return digest, ErrInvalidBodyDigest
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || hex.EncodeToString(decoded) != raw {
		return digest, ErrInvalidBodyDigest
	}
	copy(digest[:], decoded)
	return digest, nil
}
