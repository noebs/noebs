package backofficeauth

import (
	"crypto/rand"
	"errors"
	"net/http"
	"testing"
)

func TestCSRFProtectorRequiresTokenAndSameOriginEvidence(t *testing.T) {
	protector, err := NewCSRFProtector("https://dsa.adonese.sd")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := GenerateCSRFSecret(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	valid := mutationRequest(http.MethodPost)
	valid.Header.Set("Origin", "https://dsa.adonese.sd")
	valid.Header.Set("Sec-Fetch-Site", "same-origin")
	if err := protector.ValidateMutation(valid, token, token); err != nil {
		t.Fatalf("valid mutation: %v", err)
	}

	referer := mutationRequest(http.MethodPost)
	referer.Header.Set("Referer", "https://dsa.adonese.sd/backoffice/t/acme/wallet")
	if err := protector.ValidateMutation(referer, token, token); err != nil {
		t.Fatalf("referer fallback: %v", err)
	}
	wrongToken := token[:len(token)-1] + "A"
	if wrongToken == token {
		wrongToken = token[:len(token)-1] + "B"
	}

	tests := map[string]struct {
		request *http.Request
		token   string
		want    error
	}{
		"wrong token": {
			request: withHeader(mutationRequest(http.MethodPost), "Origin", "https://dsa.adonese.sd"),
			token:   wrongToken,
			want:    ErrCSRF,
		},
		"missing origin": {request: mutationRequest(http.MethodPost), token: token, want: ErrOrigin},
		"sibling origin": {
			request: withHeader(mutationRequest(http.MethodPost), "Origin", "https://api.noebs.sd"),
			token:   token,
			want:    ErrOrigin,
		},
		"cross site fetch": {
			request: withHeaders(mutationRequest(http.MethodPost), map[string]string{
				"Origin":         "https://dsa.adonese.sd",
				"Sec-Fetch-Site": "cross-site",
			}),
			token: token,
			want:  ErrOrigin,
		},
		"lowercase get is mutation": {
			request: mutationRequest("get"),
			token:   token,
			want:    ErrOrigin,
		},
		"trace is mutation": {
			request: mutationRequest(http.MethodTrace),
			token:   token,
			want:    ErrOrigin,
		},
		"safe get is rejected by mutation API": {
			request: mutationRequest(http.MethodGet),
			token:   token,
			want:    ErrInvalidInput,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if err := protector.ValidateMutation(test.request, test.token, token); !errors.Is(err, test.want) {
				t.Fatalf("ValidateMutation() error = %v, want %v", err, test.want)
			}
		})
	}
	invalidExpected := withHeader(mutationRequest(http.MethodPost), "Origin", "https://dsa.adonese.sd")
	if err := protector.ValidateMutation(invalidExpected, token, "not-a-session-token"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid expected token error = %v", err)
	}
}

func TestCSRFProtectorRejectsDuplicateOrigin(t *testing.T) {
	protector, err := NewCSRFProtector("https://dsa.adonese.sd")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := GenerateCSRFSecret(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request := mutationRequest(http.MethodPost)
	request.Header.Add("Origin", "https://dsa.adonese.sd")
	request.Header.Add("Origin", "https://dsa.adonese.sd")
	if err := protector.ValidateMutation(request, token, token); !errors.Is(err, ErrOrigin) {
		t.Fatalf("duplicate Origin error = %v", err)
	}
}

func mutationRequest(method string) *http.Request {
	request, _ := http.NewRequest(method, "https://dsa.adonese.sd/backoffice/t/acme/wallet", nil)
	return request
}

func withHeader(request *http.Request, key, value string) *http.Request {
	request.Header.Set(key, value)
	return request
}

func withHeaders(request *http.Request, values map[string]string) *http.Request {
	for key, value := range values {
		request.Header.Set(key, value)
	}
	return request
}
