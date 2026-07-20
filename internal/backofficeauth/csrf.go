package backofficeauth

import (
	"crypto/subtle"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
)

const HeaderCSRFToken = "X-Noebs-Backoffice-CSRF"

type CSRFProtector struct {
	origin string
}

func NewCSRFProtector(origin string) (*CSRFProtector, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != origin {
		return nil, ErrInvalidConfiguration
	}
	return &CSRFProtector{origin: origin}, nil
}

func GenerateCSRFSecret(entropy io.Reader) ([]byte, string, error) {
	if entropy == nil {
		return nil, "", ErrInvalidConfiguration
	}
	secret := make([]byte, opaqueTokenBytes)
	if _, err := io.ReadFull(entropy, secret); err != nil {
		return nil, "", ErrEntropyUnavailable
	}
	return secret, base64.RawURLEncoding.EncodeToString(secret), nil
}

func CSRFToken(secret []byte) (string, error) {
	if len(secret) != opaqueTokenBytes {
		return "", ErrInvalidInput
	}
	return base64.RawURLEncoding.EncodeToString(secret), nil
}

func ValidateCSRFToken(token string) error {
	_, err := decodeCSRFToken(token)
	return err
}

// ValidateMutation requires both an unguessable synchronizer token and
// browser-supplied same-origin evidence. SameSite cookies remain an additional
// browser defense, not the CSRF decision.
func (p *CSRFProtector) ValidateMutation(request *http.Request, submitted, expected string) error {
	if p == nil || request == nil || isSafeMethod(request.Method) {
		return ErrInvalidInput
	}
	expectedBytes, err := decodeCSRFToken(expected)
	if err != nil {
		return ErrInvalidInput
	}
	provided, err := decodeCSRFToken(submitted)
	if err != nil || subtle.ConstantTimeCompare(provided, expectedBytes) != 1 {
		return ErrCSRF
	}
	if err := p.validateFetchSite(request.Header.Values("Sec-Fetch-Site")); err != nil {
		return err
	}
	origins := request.Header.Values("Origin")
	if len(origins) != 0 {
		if len(origins) != 1 || origins[0] != p.origin {
			return ErrOrigin
		}
		return nil
	}
	referers := request.Header.Values("Referer")
	if len(referers) != 1 || !p.matchesReferer(referers[0]) {
		return ErrOrigin
	}
	return nil
}

func decodeCSRFToken(token string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != opaqueTokenBytes || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return nil, ErrCSRF
	}
	return decoded, nil
}

func (p *CSRFProtector) validateFetchSite(values []string) error {
	if len(values) == 0 {
		return nil
	}
	if len(values) != 1 || values[0] != "same-origin" {
		return ErrOrigin
	}
	return nil
}

func (p *CSRFProtector) matchesReferer(raw string) bool {
	referer, err := url.Parse(raw)
	if err != nil || referer.User != nil || referer.Scheme == "" || referer.Host == "" {
		return false
	}
	return referer.Scheme+"://"+referer.Host == p.origin
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
