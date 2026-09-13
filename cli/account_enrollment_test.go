package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/oidcauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

func TestAccountEnrollmentAcceptsUnassignedIdentityAndSignsOnlySelectedTenant(t *testing.T) {
	previousConfig, previousCatalog, previousVerifier, previousSigners := noebsConfig, runtimeTenantCatalog, oidcVerifier, workloadSigners
	t.Cleanup(func() {
		noebsConfig, runtimeTenantCatalog, oidcVerifier, workloadSigners = previousConfig, previousCatalog, previousVerifier, previousSigners
	})
	const issuer = "https://example.test/auth/realms/noebs"
	const subject = "11111111-1111-4111-8111-111111111111"
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := oidcauth.NewStaticKeySet(map[string]*rsa.PublicKey{"test": &key.PublicKey})
	if err != nil {
		t.Fatal(err)
	}
	oidcVerifier, err = oidcauth.NewVerifier(oidcauth.Config{Issuer: issuer, Audience: "noebs-api", AllowedClients: []string{"noebs-mobile", "noebs-web"}, AccessTokenType: "Bearer", MaxFutureIssuedAt: time.Second, Clock: oidcauth.SystemClock{}, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{"iss": issuer, "sub": subject, "aud": "noebs-api", "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(), "azp": "noebs-mobile", "typ": "Bearer"} // No organization claim yet.
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test"
	token.Header["typ"] = "JWT"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	runtimeTenantCatalog, err = tenantcatalog.New([]tenantcatalog.Tenant{{ID: "tenant-a", Name: "Tenant A"}, {ID: "tenant-b", Name: "Tenant B"}})
	if err != nil {
		t.Fatal(err)
	}
	workloadSigners = newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleIdentityAuth))
	verifier := newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleAPIGateway))
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if _, err := verifier.Verify(r, body); err != nil {
			t.Errorf("workload signature: %v", err)
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != accountEnrollmentInternalPath || r.Method != "POST" || string(body) != "{}" {
			t.Errorf("unexpected internal request %s %s %s", r.Method, r.URL.Path, body)
		}
		if r.Header.Get(workloadauth.HeaderTenantID) != "tenant-a" || r.Header.Get(workloadauth.HeaderSubject) != subject || r.Header.Get("Authorization") != "" || r.Header.Get(workloadauth.HeaderRoles) != "" || r.Header.Get(workloadauth.HeaderOrganizationID) != "" {
			t.Error("unassigned identity was confused with tenant authorization")
		}
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"tenants":[{"id":"tenant-a","name":"Tenant A"}],"preferred_tenant_id":"tenant-a","enrollment_status":"complete","refresh_required":true}`)
	}))
	t.Cleanup(upstream.Close)
	noebsConfig = ebs_fields.NoebsConfig{AccountEnrollment: accountenrollment.RuntimeConfig{Enabled: true, Issuer: issuer}, ServiceDiscovery: map[string]string{string(serviceRoleIdentityAuth): upstream.URL}}
	app := fiber.New()
	app.Use(gateway.RequestID())
	registerAccountEnrollmentGatewayRoutes(app, noebsConfig)
	protected, err := gateway.NewOIDCAuthMiddleware(gateway.OIDCAuthConfig{Verifier: oidcVerifier, SelectTenant: selectActiveTenant(runtimeTenantCatalog), AllowedClients: []string{"noebs-mobile"}, AllowedRoles: []tenantauth.Role{tenantauth.RoleUser}})
	if err != nil {
		t.Fatal(err)
	}
	app.Get("/protected", protected, func(c *fiber.Ctx) error { return c.SendStatus(200) })
	for _, tc := range []struct {
		name, tenant, body, path string
		auth                     bool
		want                     int
	}{{"selected unassigned", "tenant-a", "{}", "/consumer/auth/enrollment", true, 200}, {"missing tenant", "", "{}", "/consumer/auth/enrollment", true, 400}, {"unknown tenant", "tenant-z", "{}", "/consumer/auth/enrollment", true, 400}, {"body tenant injection", "tenant-a", `{"tenant_id":"tenant-b"}`, "/consumer/auth/enrollment", true, 400}, {"anonymous", "tenant-a", "{}", "/consumer/auth/enrollment", false, 401}, {"tenant APIs stay protected", "tenant-a", "", "/protected", true, 403}} {
		t.Run(tc.name, func(t *testing.T) {
			method := "POST"
			if tc.path == "/protected" {
				method = "GET"
			}
			request := httptest.NewRequest(method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Forwarded-For", "203.0.113.9")
			if tc.tenant != "" {
				request.Header.Set("X-Active-Tenant", tc.tenant)
			}
			request.Header.Set(workloadauth.HeaderSubject, "spoofed")
			request.Header.Set(workloadauth.HeaderTenantID, "tenant-b")
			if tc.auth {
				request.Header.Set("Authorization", "Bearer "+signed)
			}
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.want {
				var payload any
				_ = json.NewDecoder(response.Body).Decode(&payload)
				t.Fatalf("status=%d want=%d payload=%+v", response.StatusCode, tc.want, payload)
			}
		})
	}
	if hits != 1 {
		t.Fatalf("unapproved enrollment requests reached identity-auth: %d", hits)
	}
}

func TestAccountEnrollmentCredentialDoesNotBelongToGateway(t *testing.T) {
	cfg := ebs_fields.NoebsConfig{AccountEnrollment: accountenrollment.RuntimeConfig{KeycloakClientSecret: "synthetic"}}
	if err := validateAccountEnrollmentRuntimeConfig(serviceRoleAPIGateway, cfg); err == nil {
		t.Fatal("gateway accepted enrollment service credential")
	}
}
