package oidcauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://identity.example/realms/noebs"
	testAudience = "noebs-api"
	testClient   = "noebs-mobile"
	testSubject  = "08dc85cf-8f09-43c4-a840-3d8f17b5fac7"
)

var oidcTestNow = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func TestConstructorsRejectIncompleteConfiguration(t *testing.T) {
	keys := oidcTestKeys(t)
	clock := &fakeClock{now: oidcTestNow}
	static := staticTestKeys(t, map[string]*rsa.PrivateKey{"key-1": keys[0]})
	validVerifier := Config{
		Issuer:            testIssuer,
		Audience:          testAudience,
		AllowedClients:    []string{testClient},
		AccessTokenType:   "Bearer",
		MaxFutureIssuedAt: 30 * time.Second,
		Clock:             clock,
		Keys:              static,
	}
	verifierTests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"issuer", func(c *Config) { c.Issuer = "" }},
		{"audience", func(c *Config) { c.Audience = "" }},
		{"allowed clients", func(c *Config) { c.AllowedClients = nil }},
		{"empty client", func(c *Config) { c.AllowedClients = []string{""} }},
		{"duplicate client", func(c *Config) { c.AllowedClients = []string{testClient, testClient} }},
		{"access token type", func(c *Config) { c.AccessTokenType = "" }},
		{"negative issued-at skew", func(c *Config) { c.MaxFutureIssuedAt = -time.Second }},
		{"clock", func(c *Config) { c.Clock = nil }},
		{"keys", func(c *Config) { c.Keys = nil }},
	}
	for _, test := range verifierTests {
		t.Run("verifier "+test.name, func(t *testing.T) {
			config := validVerifier
			test.mutate(&config)
			if _, err := NewVerifier(config); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("error = %v, want invalid configuration", err)
			}
		})
	}

	validRemote := RemoteKeySetConfig{
		URL:                       "https://identity.example/realms/noebs/protocol/openid-connect/certs",
		Client:                    http.DefaultClient,
		RefreshInterval:           time.Hour,
		UnknownKeyRefreshInterval: time.Minute,
		Clock:                     clock,
	}
	remoteTests := []struct {
		name   string
		mutate func(*RemoteKeySetConfig)
	}{
		{"URL", func(c *RemoteKeySetConfig) { c.URL = "relative" }},
		{"client", func(c *RemoteKeySetConfig) { c.Client = nil }},
		{"refresh interval", func(c *RemoteKeySetConfig) { c.RefreshInterval = 0 }},
		{"unknown-key interval", func(c *RemoteKeySetConfig) { c.UnknownKeyRefreshInterval = 0 }},
		{"clock", func(c *RemoteKeySetConfig) { c.Clock = nil }},
	}
	for _, test := range remoteTests {
		t.Run("remote key set "+test.name, func(t *testing.T) {
			config := validRemote
			test.mutate(&config)
			if _, err := NewRemoteKeySet(config); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("error = %v, want invalid configuration", err)
			}
		})
	}

	if _, err := NewStaticKeySet(nil); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("empty static key set error = %v, want invalid configuration", err)
	}
}

func TestParseBearerIsExact(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		want          string
		wantErr       error
	}{
		{name: "valid", authorization: "Bearer aaa.bbb.ccc", want: "aaa.bbb.ccc"},
		{name: "missing", wantErr: ErrMissingAuthorization},
		{name: "raw token", authorization: "aaa.bbb.ccc", wantErr: ErrInvalidAuthorization},
		{name: "lowercase scheme", authorization: "bearer aaa.bbb.ccc", wantErr: ErrInvalidAuthorization},
		{name: "double space", authorization: "Bearer  aaa.bbb.ccc", wantErr: ErrInvalidAuthorization},
		{name: "tab separator", authorization: "Bearer\taaa.bbb.ccc", wantErr: ErrInvalidAuthorization},
		{name: "trailing whitespace", authorization: "Bearer aaa.bbb.ccc ", wantErr: ErrInvalidAuthorization},
		{name: "empty token", authorization: "Bearer ", wantErr: ErrInvalidAuthorization},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseBearer(test.authorization)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("token = %q, want %q", got, test.want)
			}
		})
	}
}

func TestVerifierUsesOnlySelectedOrganizationRoles(t *testing.T) {
	keys := oidcTestKeys(t)
	clock := &fakeClock{now: oidcTestNow}
	verifier := newTestVerifier(t, clock, staticTestKeys(t, map[string]*rsa.PrivateKey{"key-1": keys[0]}))
	claims := validTokenClaims(oidcTestNow)
	claims["organization"] = organizationClaims(
		"org-a", []string{"user", "tenant-admin", "wallet:workflow:approve"},
		"org-b", []string{"user", "wallet:workflow:reject"},
	)
	// Top-level resource_access is deliberately privileged. Keycloak can emit
	// roles aggregated from every organization there; it is never a tenant role
	// source for this verifier.
	claims["resource_access"] = map[string]any{
		testAudience: map[string]any{"roles": []string{"tenant-admin", "backoffice"}},
	}
	authorization := "Bearer " + signRS256(t, keys[0], "key-1", claims, joseTokenType)

	verified, err := verifier.VerifyBearer(context.Background(), authorization)
	if err != nil {
		t.Fatal(err)
	}
	identity := verified.Identity()
	if identity.Issuer != testIssuer || identity.Subject != testSubject || identity.AuthorizedParty != testClient {
		t.Fatalf("identity = %+v", identity)
	}
	if _, err := tenantauth.Authorize(verified, "tenant-a", tenantauth.RoleTenantAdmin); err != nil {
		t.Fatalf("tenant-a admin: %v", err)
	}
	if _, err := tenantauth.Authorize(verified, "tenant-b", tenantauth.RoleTenantAdmin); !errors.Is(err, tenantauth.ErrForbidden) {
		t.Fatalf("tenant-b admin error = %v, want forbidden", err)
	}
	if _, err := tenantauth.Authorize(verified, "tenant-b", tenantauth.RoleUser); err != nil {
		t.Fatalf("tenant-b user: %v", err)
	}
	approve, err := tenantauth.NewPermissionPolicy(tenantauth.PermissionWalletWorkflowApprove, tenantauth.RoleTenantAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := approve.Authorize(verified, "tenant-a"); err != nil {
		t.Fatalf("tenant-a approval permission: %v", err)
	}
	if _, err := approve.Authorize(verified, "tenant-b"); !errors.Is(err, tenantauth.ErrForbidden) {
		t.Fatalf("tenant-b approval permission error = %v, want forbidden", err)
	}
}

func TestVerifierRejectsInvalidTokens(t *testing.T) {
	keys := oidcTestKeys(t)
	clock := &fakeClock{now: oidcTestNow}
	verifier := newTestVerifier(t, clock, staticTestKeys(t, map[string]*rsa.PrivateKey{"key-1": keys[0]}))

	tests := []struct {
		name      string
		token     func(testing.TB) string
		secondary error
	}{
		{
			name: "missing subject",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				delete(claims, "sub")
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "missing expiration",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				delete(claims, "exp")
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "missing issued at",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				delete(claims, "iat")
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "future issued at",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				claims["iat"] = jwt.NewNumericDate(oidcTestNow.Add(31 * time.Second))
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "expired",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				claims["exp"] = jwt.NewNumericDate(oidcTestNow.Add(-time.Second))
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "wrong issuer",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				claims["iss"] = "https://identity.example/realms/other"
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "wrong audience",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				claims["aud"] = "account"
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "additional audience",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				claims["aud"] = []string{testAudience, "account"}
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "cross client",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				claims["azp"] = "untrusted-client"
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "refresh token type",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				claims["typ"] = "Refresh"
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "wrong JOSE type",
			token: func(tb testing.TB) string {
				return signRS256(tb, keys[0], "key-1", validTokenClaims(oidcTestNow), "at+jwt")
			},
		},
		{
			name: "missing organization id",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				claims["organization"] = organizationClaims("", []string{"user"}, "org-b", []string{"user"})
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "malformed organization",
			token: func(tb testing.TB) string {
				claims := validTokenClaims(oidcTestNow)
				claims["organization"] = "tenant-a"
				return signRS256(tb, keys[0], "key-1", claims, joseTokenType)
			},
		},
		{
			name: "missing key id",
			token: func(tb testing.TB) string {
				return signRS256(tb, keys[0], "", validTokenClaims(oidcTestNow), joseTokenType)
			},
			secondary: ErrUnknownKey,
		},
		{
			name: "unknown key id",
			token: func(tb testing.TB) string {
				return signRS256(tb, keys[0], "unknown", validTokenClaims(oidcTestNow), joseTokenType)
			},
			secondary: ErrUnknownKey,
		},
		{
			name: "RS384 algorithm",
			token: func(tb testing.TB) string {
				token := jwt.NewWithClaims(jwt.SigningMethodRS384, validTokenClaims(oidcTestNow))
				token.Header["kid"] = "key-1"
				token.Header["typ"] = joseTokenType
				signed, err := token.SignedString(keys[0])
				if err != nil {
					tb.Fatal(err)
				}
				return signed
			},
		},
		{
			name: "HS256 algorithm",
			token: func(tb testing.TB) string {
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, validTokenClaims(oidcTestNow))
				token.Header["kid"] = "key-1"
				token.Header["typ"] = joseTokenType
				signed, err := token.SignedString([]byte("not-an-rsa-key"))
				if err != nil {
					tb.Fatal(err)
				}
				return signed
			},
		},
		{
			name: "none algorithm",
			token: func(tb testing.TB) string {
				token := jwt.NewWithClaims(jwt.SigningMethodNone, validTokenClaims(oidcTestNow))
				token.Header["kid"] = "key-1"
				token.Header["typ"] = joseTokenType
				signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
				if err != nil {
					tb.Fatal(err)
				}
				return signed
			},
		},
		{name: "not a JWT", token: func(testing.TB) string { return "not-a-jwt" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := verifier.VerifyBearer(context.Background(), "Bearer "+test.token(t))
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("error = %v, want invalid token", err)
			}
			if test.secondary != nil && !errors.Is(err, test.secondary) {
				t.Fatalf("error = %v, want wrapped %v", err, test.secondary)
			}
		})
	}
}

func TestVerifierDoesNotAcceptRoleAliases(t *testing.T) {
	keys := oidcTestKeys(t)
	clock := &fakeClock{now: oidcTestNow}
	verifier := newTestVerifier(t, clock, staticTestKeys(t, map[string]*rsa.PrivateKey{"key-1": keys[0]}))
	claims := validTokenClaims(oidcTestNow)
	claims["organization"] = organizationClaims("org-a", []string{"administrator"}, "org-b", []string{"user"})

	verified, err := verifier.VerifyBearer(context.Background(), "Bearer "+signRS256(t, keys[0], "key-1", claims, joseTokenType))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenantauth.Authorize(verified, "tenant-a", tenantauth.RoleTenantAdmin); !errors.Is(err, tenantauth.ErrForbidden) {
		t.Fatalf("alias authorization error = %v, want forbidden", err)
	}
}

func TestVerifierAcceptsPlatformAdminOnlyFromRealmRoles(t *testing.T) {
	keys := oidcTestKeys(t)
	clock := &fakeClock{now: oidcTestNow}
	verifier := newTestVerifier(t, clock, staticTestKeys(t, map[string]*rsa.PrivateKey{"key-1": keys[0]}))
	claims := validTokenClaims(oidcTestNow)
	claims["organization"] = organizationClaims("org-a", []string{"user", "platform-admin"}, "org-b", []string{"user"})

	verified, err := verifier.VerifyBearer(context.Background(), "Bearer "+signRS256(t, keys[0], "key-1", claims, joseTokenType))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenantauth.Authorize(verified, "tenant-a", tenantauth.RolePlatformAdmin); !errors.Is(err, tenantauth.ErrForbidden) {
		t.Fatalf("organization platform-admin error = %v, want forbidden", err)
	}

	claims["realm_access"] = map[string]any{"roles": []string{"platform-admin"}}
	verified, err = verifier.VerifyBearer(context.Background(), "Bearer "+signRS256(t, keys[0], "key-1", claims, joseTokenType))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenantauth.Authorize(verified, "tenant-a", tenantauth.RolePlatformAdmin); err != nil {
		t.Fatalf("realm platform-admin: %v", err)
	}
	if _, err := tenantauth.Authorize(verified, "tenant-c", tenantauth.RolePlatformAdmin); !errors.Is(err, tenantauth.ErrUnknownTenant) {
		t.Fatalf("non-member realm platform-admin error = %v, want unknown tenant", err)
	}
}

func TestRemoteKeySetRefreshesForRotationAndBoundsUnknownKeys(t *testing.T) {
	keys := oidcTestKeys(t)
	clock := &fakeClock{now: oidcTestNow}
	var requests atomic.Int64
	var unavailable atomic.Bool
	var response struct {
		sync.RWMutex
		body []byte
	}
	response.body = jwksJSON(t, map[string]*rsa.PrivateKey{"key-1": keys[0]})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response.RLock()
		defer response.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response.body)
	}))
	defer server.Close()

	remote, err := NewRemoteKeySet(RemoteKeySetConfig{
		URL:                       server.URL,
		Client:                    server.Client(),
		RefreshInterval:           time.Hour,
		UnknownKeyRefreshInterval: time.Minute,
		Clock:                     clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier := newTestVerifier(t, clock, remote)
	verify := func(key *rsa.PrivateKey, keyID string) error {
		_, err := verifier.VerifyBearer(context.Background(), "Bearer "+signRS256(t, key, keyID, validTokenClaims(clock.Now()), joseTokenType))
		return err
	}

	if err := verify(keys[0], "key-1"); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("initial JWKS requests = %d, want 1", got)
	}

	response.Lock()
	response.body = jwksJSON(t, map[string]*rsa.PrivateKey{"key-1": keys[0], "key-2": keys[1]})
	response.Unlock()
	if err := verify(keys[1], "key-2"); err != nil {
		t.Fatalf("rotated key: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("rotation JWKS requests = %d, want 2", got)
	}

	if err := verify(keys[2], "key-3"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key error = %v", err)
	}
	if err := verify(keys[2], "key-3"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("repeated unknown key error = %v", err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("unknown-key JWKS requests = %d, want 3", got)
	}

	response.Lock()
	response.body = jwksJSON(t, map[string]*rsa.PrivateKey{
		"key-1": keys[0],
		"key-2": keys[1],
		"key-3": keys[2],
	})
	response.Unlock()
	clock.Advance(time.Minute)
	if err := verify(keys[2], "key-3"); err != nil {
		t.Fatalf("key after bounded refresh interval: %v", err)
	}
	if got := requests.Load(); got != 4 {
		t.Fatalf("post-interval JWKS requests = %d, want 4", got)
	}

	unavailable.Store(true)
	if err := verify(keys[2], "key-4"); !errors.Is(err, ErrKeySetUnavailable) {
		t.Fatalf("unavailable JWKS error = %v", err)
	}
	if err := verify(keys[2], "key-4"); !errors.Is(err, ErrKeySetUnavailable) {
		t.Fatalf("repeated unavailable JWKS error = %v", err)
	}
	if got := requests.Load(); got != 5 {
		t.Fatalf("unavailable JWKS requests = %d, want one bounded attempt", got)
	}
}

func TestParseJWKSRejectsMalformedSets(t *testing.T) {
	keys := oidcTestKeys(t)
	valid := jwksJSON(t, map[string]*rsa.PrivateKey{"key-1": keys[0]})
	if _, err := parseJWKS(valid); err != nil {
		t.Fatalf("valid JWKS: %v", err)
	}

	tests := [][]byte{
		[]byte("not-json"),
		[]byte(`{"keys":[]}`),
		[]byte(`{"keys":[{"kid":"key-1","kty":"RSA","use":"enc","alg":"RS256","n":"AQ","e":"Aw"}]}`),
		[]byte(`{"keys":[{"kid":"key-1","kty":"RSA","use":"sig","alg":"RS256","n":"AQ","e":"Ag"}]}`),
	}
	for _, body := range tests {
		if _, err := parseJWKS(body); !errors.Is(err, ErrInvalidJWKS) {
			t.Fatalf("parseJWKS(%q) error = %v, want invalid JWKS", body, err)
		}
	}
}

func BenchmarkVerifyBearerWarm(b *testing.B) {
	keys := oidcTestKeys(b)
	clock := &fakeClock{now: oidcTestNow}
	verifier := newTestVerifier(b, clock, staticTestKeys(b, map[string]*rsa.PrivateKey{"key-1": keys[0]}))
	authorization := "Bearer " + signRS256(b, keys[0], "key-1", validTokenClaims(oidcTestNow), joseTokenType)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := verifier.VerifyBearer(ctx, authorization); err != nil {
			b.Fatal(err)
		}
	}
}

func newTestVerifier(tb testing.TB, clock Clock, keys KeySet) *Verifier {
	tb.Helper()
	verifier, err := NewVerifier(Config{
		Issuer:            testIssuer,
		Audience:          testAudience,
		AllowedClients:    []string{testClient, "noebs-backoffice"},
		AccessTokenType:   "Bearer",
		MaxFutureIssuedAt: 30 * time.Second,
		Clock:             clock,
		Keys:              keys,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return verifier
}

func validTokenClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": testIssuer,
		"sub": testSubject,
		"aud": testAudience,
		"exp": jwt.NewNumericDate(now.Add(5 * time.Minute)),
		"iat": jwt.NewNumericDate(now),
		"nbf": jwt.NewNumericDate(now.Add(-time.Second)),
		"azp": testClient,
		"typ": "Bearer",
		"organization": organizationClaims(
			"org-a", []string{"user", "tenant-admin"},
			"org-b", []string{"user"},
		),
		"realm_access": map[string]any{"roles": []string{"offline_access"}},
	}
}

func organizationClaims(firstID string, firstRoles []string, secondID string, secondRoles []string) map[string]any {
	return map[string]any{
		"tenant-a": map[string]any{
			"id": firstID,
			"resource_access": map[string]any{
				testAudience: map[string]any{"roles": firstRoles},
			},
		},
		"tenant-b": map[string]any{
			"id": secondID,
			"resource_access": map[string]any{
				testAudience: map[string]any{"roles": secondRoles},
			},
		},
	}
}

func signRS256(tb testing.TB, key *rsa.PrivateKey, keyID string, claims jwt.Claims, headerType string) string {
	tb.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if keyID == "" {
		delete(token.Header, "kid")
	} else {
		token.Header["kid"] = keyID
	}
	token.Header["typ"] = headerType
	signed, err := token.SignedString(key)
	if err != nil {
		tb.Fatal(err)
	}
	return signed
}

func staticTestKeys(tb testing.TB, privateKeys map[string]*rsa.PrivateKey) *StaticKeySet {
	tb.Helper()
	publicKeys := make(map[string]*rsa.PublicKey, len(privateKeys))
	for keyID, key := range privateKeys {
		publicKeys[keyID] = &key.PublicKey
	}
	keys, err := NewStaticKeySet(publicKeys)
	if err != nil {
		tb.Fatal(err)
	}
	return keys
}

func jwksJSON(tb testing.TB, privateKeys map[string]*rsa.PrivateKey) []byte {
	tb.Helper()
	keys := make([]map[string]string, 0, len(privateKeys))
	for keyID, privateKey := range privateKeys {
		keys = append(keys, map[string]string{
			"kid": keyID,
			"kty": "RSA",
			"use": "sig",
			"alg": jwtRS256,
			"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.E)).Bytes()),
		})
	}
	body, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		tb.Fatal(err)
	}
	return body
}

var (
	testKeysOnce sync.Once
	testKeys     [3]*rsa.PrivateKey
	testKeysErr  error
)

func oidcTestKeys(tb testing.TB) [3]*rsa.PrivateKey {
	tb.Helper()
	testKeysOnce.Do(func() {
		for index := range testKeys {
			testKeys[index], testKeysErr = rsa.GenerateKey(rand.Reader, 2048)
			if testKeysErr != nil {
				return
			}
		}
	})
	if testKeysErr != nil {
		tb.Fatal(testKeysErr)
	}
	return testKeys
}

type fakeClock struct {
	sync.RWMutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.RLock()
	defer c.RUnlock()
	return c.now
}

func (c *fakeClock) Advance(delta time.Duration) {
	c.Lock()
	defer c.Unlock()
	c.now = c.now.Add(delta)
}
