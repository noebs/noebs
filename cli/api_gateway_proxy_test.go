package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/fiber/v2"
)

func TestAPIGatewayRequiresOIDCVerifier(t *testing.T) {
	previous := oidcVerifier
	oidcVerifier = nil
	t.Cleanup(func() { oidcVerifier = previous })

	if err := registerAPIGatewayProxyRoutes(fiber.New(), ebs_fields.NoebsConfig{}, gatewayTestTenantCatalog(t)); err == nil {
		t.Fatal("registerAPIGatewayProxyRoutes() error = nil")
	}
}

func TestGatewayRequestSourceRequiresOneCanonicalProxyValue(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		status int
		source string
	}{
		{name: "canonical IPv4", values: []string{"203.0.113.9"}, status: http.StatusOK, source: "203.0.113.9"},
		{name: "missing", status: http.StatusBadRequest},
		{name: "forwarding chain", values: []string{"198.51.100.4, 203.0.113.9"}, status: http.StatusBadRequest},
		{name: "duplicate", values: []string{"198.51.100.4", "203.0.113.9"}, status: http.StatusBadRequest},
		{name: "invalid", values: []string{"provider.example"}, status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotSource string
			app := fiber.New()
			app.Get("/", func(c *fiber.Ctx) error {
				source, err := gatewayRequestSource(c)
				if err != nil {
					return c.SendStatus(http.StatusBadRequest)
				}
				gotSource = source
				return c.SendStatus(http.StatusOK)
			})
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header[http.CanonicalHeaderKey(fiber.HeaderXForwardedFor)] = test.values
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer closeResponseBody(t, response.Body)
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
			if gotSource != test.source {
				t.Fatalf("source = %q, want %q", gotSource, test.source)
			}
		})
	}
}

func TestAPIGatewayCatalogIsExactOIDCCutoverSurface(t *testing.T) {
	expected := map[string]gatewayAuthMode{
		gatewayRouteKey(http.MethodPost, "/consumer/auth/profile"):                                    gatewayAuthMobilePrincipal,
		gatewayRouteKey(http.MethodPost, "/consumer/kyc"):                                             gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/consumer/user"):                                             gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPut, "/consumer/user"):                                             gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/consumer/user/lang"):                                        gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPut, "/consumer/user/lang"):                                        gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/user/device"):                                     gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/consumer/cards"):                                            gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPatch, "/consumer/cards/:card_id"):                                 gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodDelete, "/consumer/cards/:card_id"):                                gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPut, "/consumer/cards/:card_id/main"):                              gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/cards/enrollment-intents"):                        gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/cards/enrollment-intents/:enrollment_id/confirm"): gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/balance"):                                         gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/status"):                                          gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/is_alive"):                                        gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/consumer/biller"):                                           gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/n/status"):                                        gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/consumer/nec2name"):                                         gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/generate_qr"):                                     gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/qr_status"):                                       gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/qr_refund"):                                       gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/consumer/qr_complete"):                                     gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/consumer/transaction"):                                      gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/consumer/transactions"):                                     gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/ws"):                                                        gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/psp/webhooks/:callback_id"):                                gatewayAuthTenantWebhook,
		gatewayRouteKey(http.MethodGet, "/wallet/methods"):                                            gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodPost, "/wallet/wallets"):                                           gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/wallet/wallets/:id/transactions"):                           gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/wallet/wallets/:id"):                                        gatewayAuthMobileUser,
		gatewayRouteKey(http.MethodGet, "/backoffice/assets/*"):                                       gatewayAuthPublic,
	}

	for _, spec := range gatewayProxyRouteSpecs() {
		key := gatewayRouteKey(spec.method, spec.path)
		want, ok := expected[key]
		if !ok {
			t.Errorf("unexpected gateway route %s", key)
			continue
		}
		if spec.auth != want {
			t.Errorf("%s auth = %d, want %d", key, spec.auth, want)
		}
		delete(expected, key)
	}
	for key := range expected {
		t.Errorf("missing gateway route %s", key)
	}
}

func TestAPIGatewayCatalogContainsNoLegacyOrCallerSelectedTenantRoutes(t *testing.T) {
	for _, spec := range gatewayProxyRouteSpecs() {
		switch spec.path {
		case "/consumer/login", "/consumer/register", "/consumer/refresh",
			"/consumer/key", "/consumer/ipin_key", "/psp/webhooks/:provider",
			"/consumer/notifications", "/admin/wallet", "/dashboard":
			t.Errorf("retired or unsafe route remains: %s %s", spec.method, spec.path)
		}
		if spec.auth != gatewayAuthPublic &&
			spec.auth != gatewayAuthMobilePrincipal &&
			spec.auth != gatewayAuthMobileUser &&
			spec.auth != gatewayAuthTenantWebhook {
			t.Errorf("%s %s uses unknown auth mode %d", spec.method, spec.path, spec.auth)
		}
	}
}

func TestClearGatewayIdentityHeadersRemovesCompleteSignedAuthority(t *testing.T) {
	app := fiber.New()
	app.Use(clearGatewayIdentityHeaders)
	app.Post("/", func(c *fiber.Ctx) error {
		for _, name := range append(workloadauth.IdentityHeaderNames(), workloadauth.WorkloadHeaderNames()...) {
			if c.Get(name) != "" {
				return fiber.NewError(http.StatusBadRequest, name)
			}
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, name := range append(workloadauth.IdentityHeaderNames(), workloadauth.WorkloadHeaderNames()...) {
		req.Header.Set(name, "spoofed")
	}
	response, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer closeResponseBody(t, response.Body)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
}

func TestSelectActiveTenantRequiresOneCanonicalHeader(t *testing.T) {
	selectTenant := selectActiveTenant(gatewayTestTenantCatalog(t))
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		tenantID, err := selectTenant(c)
		if err != nil {
			return c.SendStatus(http.StatusForbidden)
		}
		return c.SendString(tenantID)
	})

	tests := []struct {
		name   string
		values []string
		status int
	}{
		{name: "missing", status: http.StatusForbidden},
		{name: "duplicate", values: []string{"tenant-a", "tenant-a"}, status: http.StatusForbidden},
		{name: "case alias", values: []string{"TENANT-A"}, status: http.StatusForbidden},
		{name: "canonical but outside catalog", values: []string{"tenant-b"}, status: http.StatusForbidden},
		{name: "valid", values: []string{"tenant-a"}, status: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, value := range test.values {
				req.Header.Add("X-Active-Tenant", value)
			}
			response, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer closeResponseBody(t, response.Body)
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
		})
	}
}

func gatewayTestTenantCatalog(t testing.TB) tenantcatalog.Catalog {
	t.Helper()
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "tenant-1", Name: "Tenant One"}, {ID: "tenant-a", Name: "Tenant A"}, {ID: "test-tenant", Name: "Test Tenant"}})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func gatewayRouteKey(method, path string) string {
	return method + " " + path
}
