package backofficeauth

import (
	"crypto/rand"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestCookiePolicyIssuesHostOnlySecureCookies(t *testing.T) {
	policy, err := NewCookiePolicy(CookiePolicyConfig{
		FlowName:    "__Host-noebs_bo_flow",
		SessionName: "__Host-noebs_bo",
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := generateOpaque(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	cookie, err := policy.Session(value, now, now.Add(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if cookie.Name != "__Host-noebs_bo" || cookie.Value != value || cookie.Path != "/" || cookie.Domain != "" ||
		!cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 8*60*60 {
		t.Fatalf("session cookie is not strict: %#v", cookie)
	}
	clear := policy.ClearSession()
	if clear.MaxAge != -1 || !clear.Secure || !clear.HttpOnly || clear.Path != "/" || clear.Domain != "" ||
		clear.SameSite != http.SameSiteLaxMode {
		t.Fatalf("clear cookie is not strict: %#v", clear)
	}
}

func TestCookiePolicyRejectsDuplicateAndMalformedCookies(t *testing.T) {
	policy, err := NewCookiePolicy(CookiePolicyConfig{
		FlowName:    "__Host-noebs_bo_flow",
		SessionName: "__Host-noebs_bo",
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := generateOpaque(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://dsa.adonese.sd/backoffice/tenants", nil)
	request.Header.Add("Cookie", policy.sessionName+"="+value)
	request.Header.Add("Cookie", policy.sessionName+"="+value)
	if _, err := policy.ReadSession(request); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("duplicate cookie error = %v", err)
	}
	malformed, _ := http.NewRequest(http.MethodGet, "https://dsa.adonese.sd/backoffice/tenants", nil)
	malformed.Header.Set("Cookie", policy.sessionName+"=plaintext-session")
	if _, err := policy.ReadSession(malformed); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("malformed cookie error = %v", err)
	}
}

func TestCookiePolicyRequiresHostPrefix(t *testing.T) {
	for _, config := range []CookiePolicyConfig{
		{FlowName: "flow", SessionName: "__Host-session"},
		{FlowName: "__Host-same", SessionName: "__Host-same"},
		{FlowName: "__Host-", SessionName: "__Host-session"},
	} {
		if _, err := NewCookiePolicy(config); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("NewCookiePolicy(%+v) error = %v", config, err)
		}
	}
}
