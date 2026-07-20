package transactionauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	testIssuer      = "https://api.noebs.sd/auth/realms/noebs"
	testClientID    = "noebs-wallet-authorizer"
	testRequiredACR = "urn:noebs:acr:google-totp"
	testRedirectURL = "https://api.noebs.sd/wallet/authorizations/oauth/callback"
)

type idTokenVerifierFunc func(context.Context, string) (*oidc.IDToken, error)

func (function idTokenVerifierFunc) Verify(ctx context.Context, raw string) (*oidc.IDToken, error) {
	return function(ctx, raw)
}

func TestOAuthClientBuildsExactLoA2PKCERequestAndDiscardsTokens(t *testing.T) {
	now := testNow
	clock := &testClock{now: now}
	state := opaqueForTest(1)
	nonce := opaqueForTest(2)
	verifier := opaqueForTest(3)
	idToken := idTokenForTest(t, clock, map[string]any{
		"iss":       testIssuer,
		"aud":       testClientID,
		"azp":       testClientID,
		"sub":       "subject-1",
		"exp":       now.Add(5 * time.Minute).Unix(),
		"iat":       now.Add(-time.Second).Unix(),
		"nonce":     nonce,
		"acr":       testRequiredACR,
		"auth_time": now.Add(-time.Second).Unix(),
	})

	tokenServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		clientID, clientSecret, ok := request.BasicAuth()
		if !ok || clientID != testClientID || clientSecret != "client-secret" {
			t.Errorf("token endpoint basic auth = %q/%q/%t", clientID, clientSecret, ok)
		}
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Form.Get("grant_type") != "authorization_code" || request.Form.Get("code") != "authorization-code" ||
			request.Form.Get("code_verifier") != verifier || request.Form.Get("redirect_uri") != testRedirectURL {
			t.Errorf("token form = %v", request.Form)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"access_token":"access-token","refresh_token":"discard-me","id_token":"id-token","token_type":"Bearer","expires_in":300}`)
	}))
	defer tokenServer.Close()

	httpClient := tokenServer.Client()
	httpClient.Timeout = 2 * time.Second
	client := oauthClientForTest(t, clock, tokenServer.URL, idTokenVerifierFunc(func(_ context.Context, raw string) (*oidc.IDToken, error) {
		if raw != "id-token" {
			t.Fatalf("raw ID token = %q", raw)
		}
		return idToken, nil
	}), httpClient)
	authorizationURL, err := client.AuthorizationURL(state, nonce, verifier)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("response_type") != "code" || query.Get("client_id") != testClientID ||
		query.Get("redirect_uri") != testRedirectURL || query.Get("scope") != "openid" ||
		query.Get("state") != state || query.Get("nonce") != nonce || query.Get("response_mode") != "query" ||
		query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") != oauth2.S256ChallengeFromVerifier(verifier) ||
		query.Get("acr_values") != testRequiredACR || query.Get("max_age") != "0" {
		t.Fatalf("authorization query = %v", query)
	}
	identity, err := client.Exchange(context.Background(), "authorization-code", verifier, digestString(nonce))
	if err != nil {
		t.Fatal(err)
	}
	if identity.Issuer != testIssuer || identity.Subject != "subject-1" || identity.ACR != testRequiredACR ||
		!identity.AuthenticationTime.Equal(now.Add(-time.Second)) {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestOAuthClientRejectsEveryLoA2IdentityMismatch(t *testing.T) {
	now := testNow
	nonce := opaqueForTest(2)
	base := map[string]any{
		"iss":       testIssuer,
		"aud":       testClientID,
		"azp":       testClientID,
		"sub":       "subject-1",
		"exp":       now.Add(5 * time.Minute).Unix(),
		"iat":       now.Unix(),
		"nonce":     nonce,
		"acr":       testRequiredACR,
		"auth_time": now.Unix(),
	}
	tests := map[string]func(map[string]any){
		"issuer":             func(claims map[string]any) { claims["iss"] = "https://other.example/realms/noebs" },
		"audience":           func(claims map[string]any) { claims["aud"] = "other-client" },
		"multiple audiences": func(claims map[string]any) { claims["aud"] = []string{testClientID, "other-client"} },
		"authorized party":   func(claims map[string]any) { claims["azp"] = "other-client" },
		"subject":            func(claims map[string]any) { claims["sub"] = "" },
		"nonce":              func(claims map[string]any) { claims["nonce"] = opaqueForTest(9) },
		"acr":                func(claims map[string]any) { claims["acr"] = "urn:noebs:acr:google" },
		"missing auth time":  func(claims map[string]any) { delete(claims, "auth_time") },
		"stale auth time":    func(claims map[string]any) { claims["auth_time"] = now.Add(-3 * time.Minute).Unix() },
		"future auth time":   func(claims map[string]any) { claims["auth_time"] = now.Add(2 * time.Minute).Unix() },
		"future issued at":   func(claims map[string]any) { claims["iat"] = now.Add(2 * time.Minute).Unix() },
		"expired":            func(claims map[string]any) { claims["exp"] = now.Add(-time.Second).Unix() },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			claims := cloneClaims(base)
			mutate(claims)
			clock := &testClock{now: now}
			token := idTokenForTest(t, clock, claims)
			client := oauthClientForTest(t, clock, "https://127.0.0.1:1/token", idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) {
				return token, nil
			}))
			_, err := client.validateIDToken(token, "access-token", digestString(nonce))
			if !errors.Is(err, ErrInvalidIDToken) {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}

func TestOAuthClientRejectsMalformedTokenResponse(t *testing.T) {
	for name, response := range map[string]string{
		"missing access token": `{"id_token":"id-token","token_type":"Bearer"}`,
		"wrong token type":     `{"access_token":"access-token","id_token":"id-token","token_type":"DPoP"}`,
		"missing id token":     `{"access_token":"access-token","token_type":"Bearer"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, response)
			}))
			defer server.Close()
			httpClient := server.Client()
			httpClient.Timeout = 2 * time.Second
			client := oauthClientForTest(t, &testClock{now: testNow}, server.URL,
				idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) {
					return nil, errors.New("must not verify malformed response")
				}),
				httpClient,
			)
			_, err := client.Exchange(context.Background(), "code", opaqueForTest(3), digestString(opaqueForTest(2)))
			if !errors.Is(err, ErrOAuthExchange) && !errors.Is(err, ErrInvalidIDToken) {
				t.Fatalf("exchange error = %v", err)
			}
		})
	}
}

func TestOAuthClientRejectsPlaintextTokenEndpoint(t *testing.T) {
	_, err := NewOAuthClient(OAuthClientConfig{
		Issuer:               testIssuer,
		ClientID:             testClientID,
		ClientSecret:         "client-secret",
		AuthorizationURL:     testIssuer + "/protocol/openid-connect/auth",
		TokenURL:             "http://identity.example/token",
		RedirectURL:          testRedirectURL,
		HTTPClient:           &http.Client{Timeout: 2 * time.Second},
		IDTokens:             idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) { return nil, nil }),
		Clock:                &testClock{now: testNow},
		RequiredACR:          testRequiredACR,
		MaxAuthenticationAge: 2 * time.Minute,
		MaxFutureIssuedAt:    time.Minute,
	})
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("NewOAuthClient() error = %v, want %v", err, ErrInvalidConfiguration)
	}
}

func oauthClientForTest(t testing.TB, clock Clock, tokenURL string, idTokens IDTokenVerifier, clients ...*http.Client) *OAuthClient {
	t.Helper()
	httpClient := &http.Client{Timeout: 2 * time.Second}
	if len(clients) == 1 {
		httpClient = clients[0]
	}
	client, err := NewOAuthClient(OAuthClientConfig{
		Issuer:               testIssuer,
		ClientID:             testClientID,
		ClientSecret:         "client-secret",
		AuthorizationURL:     testIssuer + "/protocol/openid-connect/auth",
		TokenURL:             tokenURL,
		RedirectURL:          testRedirectURL,
		HTTPClient:           httpClient,
		IDTokens:             idTokens,
		Clock:                clock,
		RequiredACR:          testRequiredACR,
		MaxAuthenticationAge: 2 * time.Minute,
		MaxFutureIssuedAt:    time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type payloadKeySet struct {
	payload []byte
}

func (set payloadKeySet) VerifySignature(context.Context, string) ([]byte, error) {
	return set.payload, nil
}

func idTokenForTest(t testing.TB, clock Clock, claims map[string]any) *oidc.IDToken {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	rawPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	raw := header + "." + rawPayload + "." + signature
	verifier := oidc.NewVerifier(testIssuer, payloadKeySet{payload: payload}, &oidc.Config{
		SkipClientIDCheck: true,
		SkipExpiryCheck:   true,
		SkipIssuerCheck:   true,
		Now:               clock.Now,
	})
	token, err := verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func cloneClaims(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func opaqueForTest(fill byte) string {
	raw := make([]byte, opaqueTokenBytes)
	for index := range raw {
		raw[index] = fill
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
