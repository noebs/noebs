package backofficeauth

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type testClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *testClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type idTokenVerifierFunc func(context.Context, string) (*oidc.IDToken, error)

func (f idTokenVerifierFunc) Verify(ctx context.Context, raw string) (*oidc.IDToken, error) {
	return f(ctx, raw)
}

type accessTokenVerifierFunc func(context.Context, string) (tenantauth.Claims, error)

func (f accessTokenVerifierFunc) VerifyAccessToken(ctx context.Context, raw string) (tenantauth.Claims, error) {
	return f(ctx, raw)
}

func TestOAuthScopesRequireAllOrganizationMemberships(t *testing.T) {
	if validScopes([]string{"openid", "organization"}) {
		t.Fatalf("plain organization scope must not be accepted")
	}
	if validScopes([]string{"openid", "organization:*", "organization"}) {
		t.Fatalf("plain organization scope must not accompany organization:*")
	}
	if !validScopes([]string{"openid", "organization:*"}) {
		t.Fatalf("organization:* scope must be accepted")
	}
}

func TestOAuthClientBuildsCodeS256AuthorizationAndExchangesTokens(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: now}
	state := opaqueForTest(1)
	nonce := opaqueForTest(2)
	verifier := opaqueForTest(3)
	claims := claimsForTest(t, now, now.Add(5*time.Minute))

	tokenServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		clientID, clientSecret, ok := request.BasicAuth()
		if !ok || clientID != "noebs-backoffice" || clientSecret != "client-secret" {
			t.Errorf("token endpoint basic auth = %q/%q/%t", clientID, clientSecret, ok)
		}
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Form.Get("grant_type") != "authorization_code" || request.Form.Get("code") != "authorization-code" ||
			request.Form.Get("code_verifier") != verifier || request.Form.Get("redirect_uri") != "https://dsa.adonese.sd/backoffice/callback" {
			t.Errorf("token form = %v", request.Form)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"access_token":"access-1","refresh_token":"refresh-1","refresh_expires_in":1800,"id_token":"id-1","token_type":"Bearer","expires_in":300}`)
	}))
	defer tokenServer.Close()

	client := oauthClientForTest(t, clock, tokenServer.URL,
		idTokenVerifierFunc(func(_ context.Context, raw string) (*oidc.IDToken, error) {
			if raw != "id-1" {
				t.Fatalf("ID token = %q", raw)
			}
			return &oidc.IDToken{
				Issuer:   "https://api.noebs.sd/auth/realms/noebs",
				Audience: []string{"noebs-backoffice"},
				Subject:  "operator-1",
				Expiry:   now.Add(5 * time.Minute),
				IssuedAt: now.Add(-time.Minute),
				Nonce:    nonce,
			}, nil
		}),
		accessTokenVerifierFunc(func(_ context.Context, raw string) (tenantauth.Claims, error) {
			if raw != "access-1" {
				return tenantauth.Claims{}, errors.New("unexpected access token")
			}
			return claims, nil
		}),
	)

	authorizationURL, err := client.authorizationURL(state, nonce, verifier)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.String() == "" || query.Get("response_type") != "code" || query.Get("client_id") != "noebs-backoffice" ||
		query.Get("redirect_uri") != "https://dsa.adonese.sd/backoffice/callback" || query.Get("state") != state ||
		query.Get("nonce") != nonce || query.Get("response_mode") != "query" || query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") != oauth2.S256ChallengeFromVerifier(verifier) || query.Get("scope") != "openid organization:*" {
		t.Fatalf("authorization query = %v", query)
	}

	result, err := client.exchange(context.Background(), "authorization-code", verifier, digestString(nonce))
	if err != nil {
		t.Fatal(err)
	}
	if result.accessToken != "access-1" || result.refreshToken != "refresh-1" || result.idToken != "id-1" ||
		result.claims.Identity().Subject != "operator-1" || !result.accessExpiresAt.Equal(now.Add(5*time.Minute)) ||
		!result.refreshExpiresAt.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("exchange result = %+v", result)
	}
}

func TestOAuthClientRejectsNonceMismatch(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: now}
	tokenServer := staticTokenServer(t, `{"access_token":"access-1","refresh_token":"refresh-1","refresh_expires_in":1800,"id_token":"id-1","token_type":"Bearer"}`)
	defer tokenServer.Close()
	client := oauthClientForTest(t, clock, tokenServer.URL,
		idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) {
			return &oidc.IDToken{
				Issuer: "https://api.noebs.sd/auth/realms/noebs", Audience: []string{"noebs-backoffice"},
				Subject: "operator-1", Expiry: now.Add(time.Minute), IssuedAt: now.Add(-time.Minute), Nonce: opaqueForTest(8),
			}, nil
		}),
		accessTokenVerifierFunc(func(context.Context, string) (tenantauth.Claims, error) {
			return claimsForTest(t, now, now.Add(time.Minute)), nil
		}),
	)
	_, err := client.exchange(context.Background(), "code", opaqueForTest(3), digestString(opaqueForTest(2)))
	if !errors.Is(err, ErrInvalidIDToken) {
		t.Fatalf("nonce mismatch error = %v", err)
	}
}

func TestOAuthClientRejectsFutureIDTokenIssuedAt(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: now}
	nonce := opaqueForTest(2)
	tokenServer := staticTokenServer(t, `{"access_token":"access-1","refresh_token":"refresh-1","refresh_expires_in":1800,"id_token":"id-1","token_type":"Bearer"}`)
	defer tokenServer.Close()
	client := oauthClientForTest(t, clock, tokenServer.URL,
		idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) {
			return &oidc.IDToken{
				Issuer: "https://api.noebs.sd/auth/realms/noebs", Audience: []string{"noebs-backoffice"},
				Subject: "operator-1", Expiry: now.Add(5 * time.Minute), IssuedAt: now.Add(2 * time.Minute), Nonce: nonce,
			}, nil
		}),
		accessTokenVerifierFunc(func(context.Context, string) (tenantauth.Claims, error) {
			return claimsForTest(t, now, now.Add(time.Minute)), nil
		}),
	)
	_, err := client.exchange(context.Background(), "code", opaqueForTest(3), digestString(nonce))
	if !errors.Is(err, ErrInvalidIDToken) {
		t.Fatalf("future ID token error = %v", err)
	}
}

func TestOAuthClientRefreshRequiresRotatedRefreshToken(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: now}
	claims := claimsForTest(t, now, now.Add(5*time.Minute))

	t.Run("rotated token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if request.Form.Get("grant_type") != "refresh_token" || request.Form.Get("refresh_token") != "refresh-old" {
				t.Errorf("refresh form = %v", request.Form)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"access_token":"access-new","refresh_token":"refresh-new","refresh_expires_in":1800,"token_type":"Bearer","expires_in":300}`)
		}))
		defer server.Close()
		client := oauthClientForTest(t, clock, server.URL,
			idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) {
				return nil, errors.New("refresh without a new ID token must not invoke verifier")
			}),
			accessTokenVerifierFunc(func(_ context.Context, raw string) (tenantauth.Claims, error) {
				if raw != "access-new" {
					return tenantauth.Claims{}, errors.New("wrong access token")
				}
				return claims, nil
			}),
		)
		result, err := client.refresh(context.Background(), "access-old", "refresh-old", "id-old", "operator-1")
		if err != nil {
			t.Fatal(err)
		}
		if result.refreshToken != "refresh-new" || result.idToken != "id-old" || result.accessToken != "access-new" ||
			!result.refreshExpiresAt.Equal(now.Add(30*time.Minute)) {
			t.Fatalf("refresh result = %+v", result)
		}
	})

	t.Run("omitted token fails closed", func(t *testing.T) {
		server := staticTokenServer(t, `{"access_token":"access-new","refresh_expires_in":1800,"token_type":"Bearer","expires_in":300}`)
		defer server.Close()
		client := oauthClientForTest(t, clock, server.URL,
			idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) { return nil, errors.New("unused") }),
			accessTokenVerifierFunc(func(context.Context, string) (tenantauth.Claims, error) { return claims, nil }),
		)
		_, err := client.refresh(context.Background(), "access-old", "refresh-old", "id-old", "operator-1")
		if !errors.Is(err, ErrOAuthExchange) {
			t.Fatalf("missing rotated token error = %v", err)
		}
	})

	t.Run("unchanged token fails closed", func(t *testing.T) {
		server := staticTokenServer(t, `{"access_token":"access-new","refresh_token":"refresh-old","refresh_expires_in":1800,"token_type":"Bearer","expires_in":300}`)
		defer server.Close()
		client := oauthClientForTest(t, clock, server.URL,
			idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) { return nil, errors.New("unused") }),
			accessTokenVerifierFunc(func(context.Context, string) (tenantauth.Claims, error) { return claims, nil }),
		)
		_, err := client.refresh(context.Background(), "access-old", "refresh-old", "id-old", "operator-1")
		if !errors.Is(err, ErrOAuthExchange) {
			t.Fatalf("unchanged refresh token error = %v", err)
		}
	})

	t.Run("missing refresh expiry fails closed", func(t *testing.T) {
		server := staticTokenServer(t, `{"access_token":"access-new","refresh_token":"refresh-new","token_type":"Bearer","expires_in":300}`)
		defer server.Close()
		client := oauthClientForTest(t, clock, server.URL,
			idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) { return nil, errors.New("unused") }),
			accessTokenVerifierFunc(func(context.Context, string) (tenantauth.Claims, error) { return claims, nil }),
		)
		_, err := client.refresh(context.Background(), "access-old", "refresh-old", "id-old", "operator-1")
		if !errors.Is(err, ErrOAuthExchange) {
			t.Fatalf("missing refresh expiry error = %v", err)
		}
	})
}

func TestOAuthClientMapsInvalidGrantToRevokedSession(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"error":"invalid_grant"}`)
	}))
	defer server.Close()
	client := oauthClientForTest(t, &testClock{now: now}, server.URL,
		idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) { return nil, errors.New("unused") }),
		accessTokenVerifierFunc(func(context.Context, string) (tenantauth.Claims, error) {
			return tenantauth.Claims{}, errors.New("unused")
		}),
	)
	_, err := client.refresh(context.Background(), "access-old", "refresh-old", "id-old", "operator-1")
	if !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("invalid_grant error = %v", err)
	}
}

func oauthClientForTest(
	t testing.TB,
	clock Clock,
	tokenURL string,
	idTokens IDTokenVerifier,
	accessTokens AccessTokenVerifier,
) *OAuthClient {
	t.Helper()
	client, err := NewOAuthClient(OAuthClientConfig{
		Issuer:            "https://api.noebs.sd/auth/realms/noebs",
		ClientID:          "noebs-backoffice",
		ClientSecret:      "client-secret",
		AuthorizationURL:  "https://api.noebs.sd/auth/realms/noebs/protocol/openid-connect/auth",
		TokenURL:          tokenURL,
		RedirectURL:       "https://dsa.adonese.sd/backoffice/callback",
		EndSessionURL:     "https://api.noebs.sd/auth/realms/noebs/protocol/openid-connect/logout",
		PostLogoutURL:     "https://dsa.adonese.sd/backoffice/logged-out",
		Scopes:            []string{"openid", "organization:*"},
		HTTPClient:        &http.Client{Timeout: 2 * time.Second},
		IDTokens:          idTokens,
		AccessTokens:      accessTokens,
		Clock:             clock,
		MaxFutureIssuedAt: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func claimsForTest(t testing.TB, issuedAt, expiresAt time.Time) tenantauth.Claims {
	t.Helper()
	organization, err := tenantauth.NewOrganization(
		"org-acme",
		[]tenantauth.Role{tenantauth.RoleBackoffice},
		[]tenantauth.Permission{tenantauth.PermissionWalletRead},
	)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := tenantauth.NewClaims(tenantauth.Identity{
		Issuer:          "https://api.noebs.sd/auth/realms/noebs",
		Subject:         "operator-1",
		AuthorizedParty: "noebs-backoffice",
		IssuedAt:        issuedAt,
		ExpiresAt:       expiresAt,
	}, map[string]tenantauth.Organization{"acme": organization})
	if err != nil {
		t.Fatal(err)
	}
	return claims
}

func opaqueForTest(fill byte) string {
	raw := make([]byte, opaqueTokenBytes)
	for index := range raw {
		raw[index] = fill
	}
	return base64RawURL(raw)
}

func base64RawURL(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func staticTokenServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, body)
	}))
}
