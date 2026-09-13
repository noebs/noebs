package backofficeauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/coreos/go-oidc/v3/oidc"
)

func TestUnenrolledAccountSessionRequiresExplicitPolicy(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(map[bool]string{false: "operator-default", true: "account-setup"}[allowed], func(t *testing.T) {
			now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
			clock := &testClock{now: now}
			identity := claimsForTest(t, now.Add(-time.Minute), now.Add(10*time.Minute)).Identity()
			claims, err := tenantauth.NewClaims(identity, nil)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","refresh_expires_in":1800,"id_token":"id","token_type":"Bearer"}`)
			}))
			defer server.Close()
			var nonce string
			oauth := oauthClientForTest(t, clock, server.URL, idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) {
				return &oidc.IDToken{Issuer: identity.Issuer, Audience: []string{identity.AuthorizedParty}, Subject: identity.Subject, IssuedAt: identity.IssuedAt, Expiry: identity.ExpiresAt, Nonce: nonce}, nil
			}), accessTokenVerifierFunc(func(context.Context, string) (tenantauth.Claims, error) { return claims, nil }))
			service := serviceForTest(t, clock, newMemoryRepository(), oauth, allowed)
			started, err := service.BeginLogin(context.Background(), "/backoffice/home")
			if err != nil {
				t.Fatal(err)
			}
			location, _ := url.Parse(started.AuthorizationURL)
			nonce = location.Query().Get("nonce")
			completed, err := service.CompleteLogin(context.Background(), location.Query().Get("state"), started.FlowCookie.Value, "code")
			if !allowed {
				if !errors.Is(err, ErrInvalidAccessToken) {
					t.Fatalf("unenrolled operator login: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			session, err := service.Authenticate(context.Background(), completed.SessionCookie.Value)
			if err != nil || len(session.Claims.Memberships()) != 0 || session.CSRFToken == "" {
				t.Fatalf("account setup session err=%v", err)
			}
			service.allowUnenrolledAccounts = false
			if _, err := service.Authenticate(context.Background(), completed.SessionCookie.Value); !errors.Is(err, ErrInvalidAccessToken) {
				t.Fatalf("operator session accepted unenrolled account: %v", err)
			}
		})
	}
}
