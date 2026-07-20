package gateway

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/oidcauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

const (
	gatewayOIDCTestIssuer   = "https://identity.example/realms/noebs"
	gatewayOIDCTestAudience = "noebs-api"
	gatewayOIDCTestClient   = "noebs-mobile"
	gatewayOIDCTestSubject  = "a82a3e3a-83e7-42f4-85f0-9b6039283031"
	gatewayOIDCTestKeyID    = "gateway-test-key"
	activeTenantHeader      = "X-Active-Tenant"
)

var (
	gatewayOIDCTestNow     = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	errMissingActiveTenant = errors.New("missing active tenant")
)

func TestNewOIDCAuthMiddlewareRejectsInvalidConfiguration(t *testing.T) {
	verifier, _ := gatewayOIDCTestVerifier(t)
	valid := OIDCAuthConfig{
		Verifier:       verifier,
		SelectTenant:   gatewayOIDCTestTenantSelector,
		AllowedClients: []string{gatewayOIDCTestClient},
		AllowedRoles:   []tenantauth.Role{tenantauth.RoleUser},
	}
	tests := []struct {
		name   string
		mutate func(*OIDCAuthConfig)
	}{
		{"verifier", func(c *OIDCAuthConfig) { c.Verifier = nil }},
		{"tenant selector", func(c *OIDCAuthConfig) { c.SelectTenant = nil }},
		{"allowed clients", func(c *OIDCAuthConfig) { c.AllowedClients = nil }},
		{"empty client", func(c *OIDCAuthConfig) { c.AllowedClients = []string{""} }},
		{"duplicate client", func(c *OIDCAuthConfig) { c.AllowedClients = []string{gatewayOIDCTestClient, gatewayOIDCTestClient} }},
		{"allowed roles", func(c *OIDCAuthConfig) { c.AllowedRoles = nil }},
		{"invalid role", func(c *OIDCAuthConfig) { c.AllowedRoles = []tenantauth.Role{"administrator"} }},
		{"invalid permission", func(c *OIDCAuthConfig) { c.RequiredPermission = "wallet:all" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if _, err := NewOIDCAuthMiddleware(config); !errors.Is(err, ErrInvalidOIDCAuthConfiguration) {
				t.Fatalf("error = %v, want invalid configuration", err)
			}
		})
	}
}

func TestOIDCAuthMiddlewareRequiresPermissionFromSelectedOrganization(t *testing.T) {
	verifier, key := gatewayOIDCTestVerifier(t)
	middleware, err := NewOIDCAuthMiddleware(OIDCAuthConfig{
		Verifier:           verifier,
		SelectTenant:       gatewayOIDCTestTenantSelector,
		AllowedClients:     []string{gatewayOIDCTestClient},
		AllowedRoles:       []tenantauth.Role{tenantauth.RoleTenantAdmin},
		RequiredPermission: tenantauth.PermissionWalletWorkflowApprove,
	})
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/", middleware, func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
	token := gatewayOIDCTestToken(t, key, gatewayOIDCTestOrganizations(
		[]string{"tenant-admin", "wallet:workflow:approve"},
		[]string{"tenant-admin", "wallet:workflow:reject"},
	), nil)
	for _, test := range []struct {
		tenant string
		status int
	}{
		{tenant: "tenant-a", status: http.StatusNoContent},
		{tenant: "tenant-b", status: http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://gateway.test/", nil)
		req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
		req.Header.Set(activeTenantHeader, test.tenant)
		response, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != test.status {
			body, _ := io.ReadAll(response.Body)
			closeTestResponseBody(t, response.Body)
			t.Fatalf("tenant %s status = %d, want %d: %s", test.tenant, response.StatusCode, test.status, body)
		}
		closeTestResponseBody(t, response.Body)
	}
}

func TestOIDCAuthMiddlewareRejectsAuthorizedPartyOutsideRoute(t *testing.T) {
	verifier, key := gatewayOIDCTestVerifier(t)
	middleware, err := NewOIDCAuthMiddleware(OIDCAuthConfig{
		Verifier:       verifier,
		SelectTenant:   gatewayOIDCTestTenantSelector,
		AllowedClients: []string{"noebs-backoffice"},
		AllowedRoles:   []tenantauth.Role{tenantauth.RoleUser},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/", middleware, func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
	req := httptestOIDCRequest(gatewayOIDCTestToken(t, key, gatewayOIDCTestOrganizations(
		[]string{"user"}, []string{"user"},
	), nil))
	req.Header.Set(activeTenantHeader, "tenant-a")
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResponseBody(t, response.Body)
	assertOIDCFailure(t, response, http.StatusForbidden, "authorization_denied", req.Header.Get(fiber.HeaderAuthorization))
}

func TestOIDCAuthMiddlewareStoresTypedTenantPrincipal(t *testing.T) {
	verifier, key := gatewayOIDCTestVerifier(t)
	middleware := gatewayOIDCTestMiddleware(t, verifier, tenantauth.RoleTenantAdmin)
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	var captured tenantauth.Principal
	app.Get("/", middleware, func(c *fiber.Ctx) error {
		principal, ok := OIDCPrincipal(c)
		if !ok {
			t.Fatal("typed OIDC principal is missing")
		}
		captured = principal
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptestOIDCRequest(gatewayOIDCTestToken(t, key, gatewayOIDCTestOrganizations(
		[]string{"user", "tenant-admin"},
		[]string{"user"},
	), nil))
	req.Header.Set(activeTenantHeader, "tenant-a")
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResponseBody(t, response.Body)
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, http.StatusNoContent, body)
	}
	if captured.Tenant() != "tenant-a" || captured.OrganizationID() != "org-a" || !captured.HasRole(tenantauth.RoleTenantAdmin) {
		t.Fatalf("principal = tenant %q organization %q roles %v", captured.Tenant(), captured.OrganizationID(), captured.Roles())
	}
	identity := captured.Identity()
	if identity.Issuer != gatewayOIDCTestIssuer || identity.Subject != gatewayOIDCTestSubject || identity.AuthorizedParty != gatewayOIDCTestClient {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestOIDCAuthMiddlewareRejectsMissingMalformedAndDuplicateAuthorization(t *testing.T) {
	verifier, key := gatewayOIDCTestVerifier(t)
	middleware := gatewayOIDCTestMiddleware(t, verifier, tenantauth.RoleUser)
	token := gatewayOIDCTestToken(t, key, gatewayOIDCTestOrganizations([]string{"user"}, []string{"user"}), nil)
	tests := []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "bare token", values: []string{token}},
		{name: "lowercase scheme", values: []string{"bearer " + token}},
		{name: "basic", values: []string{"Basic " + token}},
		{name: "duplicate", values: []string{"Bearer " + token, "Bearer " + token}},
		{name: "malformed token", values: []string{"Bearer not-a-jwt"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := fiber.New(fiber.Config{DisableStartupMessage: true})
			app.Get("/", middleware, func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
			req, err := http.NewRequest(http.MethodGet, "http://gateway.test/", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header["Authorization"] = test.values
			req.Header.Set(activeTenantHeader, "tenant-a")
			response, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer closeTestResponseBody(t, response.Body)
			assertOIDCFailure(t, response, http.StatusUnauthorized, "authentication_failed", token)
		})
	}
}

func TestOIDCAuthMiddlewareNeverUnionsRolesAcrossTenants(t *testing.T) {
	verifier, key := gatewayOIDCTestVerifier(t)
	middleware := gatewayOIDCTestMiddleware(t, verifier, tenantauth.RoleTenantAdmin)
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/", middleware, func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
	token := gatewayOIDCTestToken(t, key, gatewayOIDCTestOrganizations(
		[]string{"user", "tenant-admin"},
		[]string{"user"},
	), nil)
	req := httptestOIDCRequest(token)
	req.Header.Set(activeTenantHeader, "tenant-b")

	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResponseBody(t, response.Body)
	assertOIDCFailure(t, response, http.StatusForbidden, "authorization_denied", token)
}

func TestOIDCAuthMiddlewareIgnoresPoisonedTopLevelResourceAccess(t *testing.T) {
	verifier, key := gatewayOIDCTestVerifier(t)
	middleware := gatewayOIDCTestMiddleware(t, verifier, tenantauth.RoleTenantAdmin)
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/", middleware, func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
	poisonedTopLevelRoles := map[string]any{
		gatewayOIDCTestAudience: map[string]any{"roles": []string{"tenant-admin", "backoffice"}},
	}
	token := gatewayOIDCTestToken(t, key, gatewayOIDCTestOrganizations(
		[]string{"user"},
		[]string{"user"},
	), poisonedTopLevelRoles)
	req := httptestOIDCRequest(token)
	req.Header.Set(activeTenantHeader, "tenant-b")

	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResponseBody(t, response.Body)
	assertOIDCFailure(t, response, http.StatusForbidden, "authorization_denied", token)
}

func TestOIDCAuthMiddlewareRequiresSelectorResultToMatchMembership(t *testing.T) {
	verifier, key := gatewayOIDCTestVerifier(t)
	middleware := gatewayOIDCTestMiddleware(t, verifier, tenantauth.RoleUser)
	token := gatewayOIDCTestToken(t, key, gatewayOIDCTestOrganizations([]string{"user"}, []string{"user"}), nil)
	tests := []struct {
		name   string
		tenant string
	}{
		{name: "missing"},
		{name: "unknown", tenant: "tenant-c"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := fiber.New(fiber.Config{DisableStartupMessage: true})
			app.Get("/", middleware, func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
			req := httptestOIDCRequest(token)
			if test.tenant != "" {
				req.Header.Set(activeTenantHeader, test.tenant)
			}
			response, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer closeTestResponseBody(t, response.Body)
			assertOIDCFailure(t, response, http.StatusForbidden, "authorization_denied", token)
		})
	}
}

func gatewayOIDCTestMiddleware(tb testing.TB, verifier *oidcauth.Verifier, roles ...tenantauth.Role) fiber.Handler {
	tb.Helper()
	middleware, err := NewOIDCAuthMiddleware(OIDCAuthConfig{
		Verifier:       verifier,
		SelectTenant:   gatewayOIDCTestTenantSelector,
		AllowedClients: []string{gatewayOIDCTestClient},
		AllowedRoles:   roles,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return middleware
}

func gatewayOIDCTestTenantSelector(c *fiber.Ctx) (string, error) {
	tenant := c.Get(activeTenantHeader)
	if tenant == "" {
		return "", errMissingActiveTenant
	}
	return tenant, nil
}

func gatewayOIDCTestVerifier(tb testing.TB) (*oidcauth.Verifier, *rsa.PrivateKey) {
	tb.Helper()
	key := gatewayOIDCTestRSAKey(tb)
	keys, err := oidcauth.NewStaticKeySet(map[string]*rsa.PublicKey{gatewayOIDCTestKeyID: &key.PublicKey})
	if err != nil {
		tb.Fatal(err)
	}
	verifier, err := oidcauth.NewVerifier(oidcauth.Config{
		Issuer:            gatewayOIDCTestIssuer,
		Audience:          gatewayOIDCTestAudience,
		AllowedClients:    []string{gatewayOIDCTestClient},
		AccessTokenType:   "Bearer",
		MaxFutureIssuedAt: 30 * time.Second,
		Clock:             gatewayOIDCTestClock{},
		Keys:              keys,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return verifier, key
}

func gatewayOIDCTestToken(tb testing.TB, key *rsa.PrivateKey, organizations map[string]any, topLevelResourceAccess map[string]any) string {
	tb.Helper()
	claims := jwt.MapClaims{
		"iss":          gatewayOIDCTestIssuer,
		"sub":          gatewayOIDCTestSubject,
		"aud":          gatewayOIDCTestAudience,
		"exp":          jwt.NewNumericDate(gatewayOIDCTestNow.Add(5 * time.Minute)),
		"iat":          jwt.NewNumericDate(gatewayOIDCTestNow),
		"azp":          gatewayOIDCTestClient,
		"typ":          "Bearer",
		"organization": organizations,
		"realm_access": map[string]any{"roles": []string{"offline_access"}},
	}
	if topLevelResourceAccess != nil {
		claims["resource_access"] = topLevelResourceAccess
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = gatewayOIDCTestKeyID
	token.Header["typ"] = "JWT"
	signed, err := token.SignedString(key)
	if err != nil {
		tb.Fatal(err)
	}
	return signed
}

func gatewayOIDCTestOrganizations(tenantARoles, tenantBRoles []string) map[string]any {
	return map[string]any{
		"tenant-a": map[string]any{
			"id": "org-a",
			"resource_access": map[string]any{
				gatewayOIDCTestAudience: map[string]any{"roles": tenantARoles},
			},
		},
		"tenant-b": map[string]any{
			"id": "org-b",
			"resource_access": map[string]any{
				gatewayOIDCTestAudience: map[string]any{"roles": tenantBRoles},
			},
		},
	}
}

func httptestOIDCRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://gateway.test/", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
	return req
}

func assertOIDCFailure(t *testing.T, response *http.Response, wantStatus int, wantCode, token string) {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, wantStatus, body)
	}
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v: %s", err, body)
	}
	wantMessage := map[int]string{
		http.StatusUnauthorized: "authentication failed",
		http.StatusForbidden:    "authorization denied",
	}[wantStatus]
	if payload["code"] != wantCode || payload["message"] != wantMessage || len(payload) != 2 {
		t.Fatalf("response = %#v, want code %q and generic message", payload, wantCode)
	}
	if strings.Contains(string(body), token) || strings.Contains(string(body), gatewayOIDCTestSubject) {
		t.Fatalf("authentication details leaked in response: %s", body)
	}
	if wantStatus == http.StatusUnauthorized && response.Header.Get(fiber.HeaderWWWAuthenticate) != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q, want Bearer", response.Header.Get(fiber.HeaderWWWAuthenticate))
	}
}

var (
	gatewayOIDCTestKeyOnce sync.Once
	gatewayOIDCTestKey     *rsa.PrivateKey
	gatewayOIDCTestKeyErr  error
)

func gatewayOIDCTestRSAKey(tb testing.TB) *rsa.PrivateKey {
	tb.Helper()
	gatewayOIDCTestKeyOnce.Do(func() {
		gatewayOIDCTestKey, gatewayOIDCTestKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if gatewayOIDCTestKeyErr != nil {
		tb.Fatal(gatewayOIDCTestKeyErr)
	}
	return gatewayOIDCTestKey
}

type gatewayOIDCTestClock struct{}

func (gatewayOIDCTestClock) Now() time.Time { return gatewayOIDCTestNow }
