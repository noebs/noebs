package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func TestAPIGatewayRequiresServiceDiscovery(t *testing.T) {
	err := registerAPIGatewayProxyRoutes(fiber.New(), ebs_fields.NoebsConfig{}, auth, func(c *fiber.Ctx) error {
		return c.Next()
	})
	if err == nil {
		t.Fatal("expected missing service discovery error")
	}
}

func TestAPIGatewayProxiesEveryServiceOwnedRoute(t *testing.T) {
	ensureInit()
	proxied := map[string]bool{}
	for _, spec := range gatewayProxyRouteSpecs() {
		proxied[gatewayRouteKey(spec.method, spec.path)] = true
	}

	roles := []serviceRole{
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
		serviceRoleWalletAPI,
	}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			if role == serviceRoleWalletAPI {
				configureWalletRouteTest(t)
			} else {
				setServiceRoleForTest(t, role)
			}
			route := GetMainEngine()
			for _, owned := range route.GetRoutes(true) {
				if isInternalServiceRoute(owned.Method, owned.Path) {
					continue
				}
				key := gatewayRouteKey(owned.Method, owned.Path)
				if !proxied[key] {
					t.Fatalf("%s owns unproxied route %s", role, key)
				}
			}
		})
	}
}

func TestUserServiceRolesRejectBearerWithoutGatewayIdentity(t *testing.T) {
	ensureInit()
	authorization := testAuthorizationHeader(t)
	tests := []struct {
		name   string
		role   serviceRole
		method string
		path   string
	}{
		{name: "identity", role: serviceRoleIdentityAuth, method: http.MethodGet, path: "/consumer/auth/me"},
		{name: "card vault", role: serviceRoleCardVault, method: http.MethodGet, path: "/consumer/get_cards"},
		{name: "ebs adapter", role: serviceRoleEBSAdapter, method: http.MethodGet, path: "/consumer/transactions"},
		{name: "notification", role: serviceRoleNotification, method: http.MethodGet, path: "/consumer/notifications"},
		{name: "notification websocket", role: serviceRoleNotification, method: http.MethodGet, path: "/ws"},
		{name: "beneficiary", role: serviceRoleBeneficiary, method: http.MethodGet, path: "/consumer/beneficiary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setServiceRoleForTest(t, tt.role)
			route := GetMainEngine()

			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", authorization)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}

	t.Run("wallet api", func(t *testing.T) {
		configureWalletRouteTest(t)
		route := GetMainEngine()

		req := httptest.NewRequest(http.MethodPost, "/wallet/wallets", nil)
		req.Header.Set("Authorization", authorization)
		resp, err := route.Test(req)
		if err != nil {
			t.Fatalf("route.Test() error = %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})
}

func TestAdminServiceRolesRejectPublicAdminKeyWithoutGatewayIdentity(t *testing.T) {
	ensureInit()
	adminKey := setAdminKeyForTest(t)
	tests := []struct {
		name   string
		role   serviceRole
		method string
		path   string
	}{
		{name: "identity api key", role: serviceRoleIdentityAuth, method: http.MethodPost, path: "/generate_api_key"},
		{name: "admin reporting", role: serviceRoleAdminReporting, method: http.MethodGet, path: "/dashboard/count"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setServiceRoleForTest(t, tt.role)
			route := GetMainEngine()

			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("X-Admin-Key", adminKey)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}

	t.Run("wallet api", func(t *testing.T) {
		configureWalletRouteTest(t)
		route := GetMainEngine()

		req := httptest.NewRequest(http.MethodGet, "/admin/wallet/", nil)
		req.Header.Set("X-Admin-Key", adminKey)
		resp, err := route.Test(req)
		if err != nil {
			t.Fatalf("route.Test() error = %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})
}

func TestServiceMetricsRejectPublicAdminKeyWithoutGatewayIdentity(t *testing.T) {
	ensureInit()
	adminKey := setAdminKeyForTest(t)
	roles := []serviceRole{
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
	}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			setServiceRoleForTest(t, role)
			route := GetMainEngine()

			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			req.Header.Set("X-Admin-Key", adminKey)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}

	t.Run("wallet api", func(t *testing.T) {
		configureWalletRouteTest(t)
		route := GetMainEngine()

		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("X-Admin-Key", adminKey)
		resp, err := route.Test(req)
		if err != nil {
			t.Fatalf("route.Test() error = %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})
}

func TestServiceMetricsAcceptGatewayAdminIdentity(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleIdentityAuth)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	setGatewayAdminIdentityHeader(req)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestAPIGatewayMetricsAcceptsPublicAdminKey(t *testing.T) {
	ensureInit()
	adminKey := setAdminKeyForTest(t)
	configureGatewayProxyForTest(t)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("X-Admin-Key", adminKey)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestAPIGatewayDoesNotExposeLegacyConsumerTestRoute(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	route := GetMainEngine()
	for _, registered := range route.GetRoutes(true) {
		if registered.Method == http.MethodPost && registered.Path == "/consumer/test" {
			t.Fatalf("api-gateway registered legacy route %s", registered.Path)
		}
	}
}

func TestAPIGatewayEnforcesUserAuthBeforeProxy(t *testing.T) {
	ensureInit()
	tests := []struct {
		name string
		path string
	}{
		{name: "card vault", path: "/consumer/get_cards"},
		{name: "notification websocket", path: "/ws"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(upstream.Close)

			setGatewayDiscoveryForTest(t, upstream.URL)
			setServiceRoleForTest(t, serviceRoleAPIGateway)
			route := GetMainEngine()

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
			if hits.Load() != 0 {
				t.Fatalf("upstream hits = %d, want 0", hits.Load())
			}
		})
	}
}

func TestAPIGatewayPropagatesVerifiedUserIdentity(t *testing.T) {
	ensureInit()
	authorization := testAuthorizationHeader(t)
	type observedHeaders struct {
		tenant      string
		userID      string
		mobile      string
		auth        string
		adminKey    string
		adminRole   string
		permissions string
	}
	observed := make(chan observedHeaders, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- observedHeaders{
			tenant:      r.Header.Get(gateway.GatewayTenantIDHeader),
			userID:      r.Header.Get(gateway.GatewayUserIDHeader),
			mobile:      r.Header.Get(gateway.GatewayMobileHeader),
			auth:        r.Header.Get("Authorization"),
			adminKey:    r.Header.Get("X-Admin-Key"),
			adminRole:   r.Header.Get("X-Admin-Role"),
			permissions: r.Header.Get("X-Admin-Permissions"),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/consumer/get_cards", nil)
	req.Header.Set("Authorization", authorization)
	req.Header.Set(gateway.GatewayTenantIDHeader, "spoofed-tenant")
	req.Header.Set(gateway.GatewayUserIDHeader, "999")
	req.Header.Set(gateway.GatewayMobileHeader, "0911111111")
	req.Header.Set("X-Admin-Key", "public-admin")
	req.Header.Set("X-Admin-Role", "admin")
	req.Header.Set("X-Admin-Permissions", "config:manage")
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	assertGatewayProxied(t, resp)

	got := <-observed
	if got.tenant != "test-tenant" || got.userID != "1" || got.mobile != "0912345678" {
		t.Fatalf("forwarded identity = %+v, want tenant=test-tenant userID=1 mobile=0912345678", got)
	}
	if got.auth != "" || got.adminKey != "" || got.adminRole != "" || got.permissions != "" {
		t.Fatalf("gateway forwarded public credentials on user route: %+v", got)
	}
}

func TestAPIGatewayClearsIdentityAndCredentialHeadersOnPublicRoutes(t *testing.T) {
	ensureInit()
	observed := make(chan bool, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Header.Get(gateway.GatewayTenantIDHeader) == "" &&
			r.Header.Get(gateway.GatewayUserIDHeader) == "" &&
			r.Header.Get(gateway.GatewayMobileHeader) == "" &&
			r.Header.Get(gateway.GatewayAdminIdentityHeader) == "" &&
			r.Header.Get(gateway.GatewayAdminRoleHeader) == "" &&
			r.Header.Get(gateway.GatewayAdminPermissionsHeader) == "" &&
			r.Header.Get("Authorization") == "" &&
			r.Header.Get("X-Admin-Key") == "" &&
			r.Header.Get("X-Admin-Role") == "" &&
			r.Header.Get("X-Admin-Permissions") == ""
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/consumer/login", nil)
	setTestGatewayUserIdentityHeaders(req)
	setGatewayAdminIdentityHeader(req)
	req.Header.Set(gateway.GatewayAdminPermissionsHeader, "config:manage")
	req.Header.Set("Authorization", "Bearer public-token")
	req.Header.Set("X-Admin-Key", "public-admin")
	req.Header.Set("X-Admin-Role", "admin")
	req.Header.Set("X-Admin-Permissions", "config:manage")
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	assertGatewayProxied(t, resp)
	if cleared := <-observed; !cleared {
		t.Fatalf("gateway forwarded caller-supplied identity headers on public route")
	}
}

func gatewayRouteKey(method, path string) string {
	return fmt.Sprintf("%s %s", strings.ToUpper(method), path)
}

func isInternalServiceRoute(method, path string) bool {
	switch gatewayRouteKey(method, path) {
	case "GET /test", "GET /metrics":
		return true
	default:
		return strings.HasPrefix(path, "/internal/") || method == fiber.MethodHead || method == fiber.MethodOptions
	}
}

func TestAPIGatewayEnforcesAdminAuthBeforeProxy(t *testing.T) {
	ensureInit()
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	setAdminKeyForTest(t)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if hits.Load() != 0 {
		t.Fatalf("upstream hits = %d, want 0", hits.Load())
	}
}

func TestAPIGatewayPropagatesVerifiedAdminIdentity(t *testing.T) {
	ensureInit()
	adminKey := setAdminKeyForTest(t)
	type observedHeaders struct {
		identity    string
		role        string
		key         string
		auth        string
		publicRole  string
		permissions string
	}
	observed := make(chan observedHeaders, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- observedHeaders{
			identity:    r.Header.Get(gateway.GatewayAdminIdentityHeader),
			role:        r.Header.Get(gateway.GatewayAdminRoleHeader),
			key:         r.Header.Get("X-Admin-Key"),
			auth:        r.Header.Get("Authorization"),
			publicRole:  r.Header.Get("X-Admin-Role"),
			permissions: r.Header.Get("X-Admin-Permissions"),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	req.Header.Set("X-Admin-Key", adminKey)
	req.Header.Set(gateway.GatewayAdminIdentityHeader, "spoofed")
	req.Header.Set(gateway.GatewayAdminRoleHeader, "viewer")
	req.Header.Set(gateway.GatewayAdminPermissionsHeader, "wallet:view")
	req.Header.Set("X-Admin-Role", "viewer")
	req.Header.Set("X-Admin-Permissions", "wallet:view")
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	assertGatewayProxied(t, resp)
	got := <-observed
	if got.identity != gateway.GatewayAdminIdentityValue {
		t.Fatalf("forwarded admin identity = %q, want %q", got.identity, gateway.GatewayAdminIdentityValue)
	}
	if got.role != gateway.GatewayAdminRoleValue {
		t.Fatalf("forwarded admin role = %q, want %q", got.role, gateway.GatewayAdminRoleValue)
	}
	if got.key != "" || got.auth != "" || got.publicRole != "" || got.permissions != "" {
		t.Fatalf("gateway forwarded public admin credentials or permissions: %+v", got)
	}
}
