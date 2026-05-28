package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func TestAPIGatewayProxyCatalogMatchesSingleServiceOwner(t *testing.T) {
	ensureInit()
	ownedRoutes := map[string]serviceRole{}
	serviceRoles := []serviceRole{
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
		serviceRoleWalletAPI,
	}
	for _, role := range serviceRoles {
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
				recordOwnedExternalRoute(t, ownedRoutes, gatewayRouteKey(owned.Method, owned.Path), role)
			}
			for _, key := range explicitServiceOwnedExternalRoutes(role) {
				recordOwnedExternalRoute(t, ownedRoutes, key, role)
			}
		})
	}

	proxiedRoutes := map[string]serviceRole{}
	for _, spec := range gatewayProxyRouteSpecs() {
		key := gatewayRouteKey(spec.method, spec.path)
		if existing, ok := proxiedRoutes[key]; ok {
			t.Fatalf("%s is proxied to both %s and %s", key, existing, spec.role)
		}
		proxiedRoutes[key] = spec.role
	}

	for key, owner := range ownedRoutes {
		target, ok := proxiedRoutes[key]
		if !ok {
			t.Fatalf("%s owned by %s is missing from the API gateway proxy catalog", key, owner)
		}
		if target != owner {
			t.Fatalf("%s owned by %s is proxied to %s", key, owner, target)
		}
	}
	for key, target := range proxiedRoutes {
		owner, ok := ownedRoutes[key]
		if !ok {
			t.Fatalf("%s is proxied to %s but no HTTP service owns it", key, target)
		}
		if owner != target {
			t.Fatalf("%s is proxied to %s but owned by %s", key, target, owner)
		}
	}
}

func recordOwnedExternalRoute(t *testing.T, routes map[string]serviceRole, key string, role serviceRole) {
	t.Helper()
	if existing, ok := routes[key]; ok {
		t.Fatalf("%s is owned by both %s and %s", key, existing, role)
	}
	routes[key] = role
}

func explicitServiceOwnedExternalRoutes(role serviceRole) []string {
	if role == serviceRoleAdminReporting {
		return []string{gatewayRouteKey(http.MethodGet, "/dashboard/assets/*")}
	}
	return nil
}

func TestAPIGatewayProxyCatalogTargetsRoutableHTTPServices(t *testing.T) {
	config := decodeKubernetesBaseNoebsConfig(t)
	services := map[string]map[string]int{}
	for _, object := range decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base")) {
		if object.Kind != "Service" {
			continue
		}
		ports := map[string]int{}
		for _, port := range object.Spec.Ports {
			ports[port.Name] = port.Port
		}
		services[object.Metadata.Name] = ports
	}

	targetRoles := map[serviceRole]bool{}
	for _, spec := range gatewayProxyRouteSpecs() {
		if spec.role == serviceRoleAPIGateway {
			t.Fatalf("%s %s proxies back to api-gateway", spec.method, spec.path)
		}
		if spec.role.runsMigrations() || spec.role.startsGRPC() || spec.role.startsWalletWorker() || spec.role.startsEBSEventPublisher() || spec.role.startsAdminReportingProjector() || !spec.role.startsHTTP() {
			t.Fatalf("%s %s targets non-routable role %s", spec.method, spec.path, spec.role)
		}
		targetRoles[spec.role] = true
	}
	for role := range targetRoles {
		endpoint := strings.TrimSpace(config.Noebs.ServiceDiscovery[string(role)])
		if endpoint == "" {
			t.Fatalf("gateway route catalog targets %s but noebs-config service_discovery does not declare it", role)
		}
		ports, ok := services[string(role)]
		if !ok {
			t.Fatalf("gateway route catalog targets %s but Kubernetes Service %q is missing", role, role)
		}
		if ports["http"] != 8080 {
			t.Fatalf("Kubernetes Service %s http port = %d, want 8080", role, ports["http"])
		}
	}
}

func TestAPIGatewayProxyCatalogCoversPublicHTTPServiceRoles(t *testing.T) {
	targetRoles := map[serviceRole]bool{}
	for _, spec := range gatewayProxyRouteSpecs() {
		targetRoles[spec.role] = true
	}

	publicHTTPRoles := []serviceRole{
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
		serviceRoleWalletAPI,
	}
	for _, role := range publicHTTPRoles {
		if !role.startsHTTP() || role == serviceRoleAPIGateway {
			t.Fatalf("%s is not a public HTTP service role", role)
		}
		if !targetRoles[role] {
			t.Fatalf("public HTTP service role %s has no API gateway route", role)
		}
	}

	privateRoles := []serviceRole{
		serviceRoleAPIGateway,
		serviceRoleEBSAdapterEvents,
		serviceRoleAdminReportingProjector,
		serviceRoleWalletLedger,
		serviceRoleWalletWorker,
		serviceRoleIdentityAuthMigrate,
		serviceRoleCardVaultMigrate,
		serviceRoleEBSAdapterMigrate,
		serviceRolePSPWebhookMigrate,
		serviceRoleAdminReportingMigrate,
		serviceRoleNotificationMigrate,
		serviceRoleBeneficiaryMigrate,
		serviceRoleWalletLedgerMigrate,
	}
	for _, role := range privateRoles {
		if targetRoles[role] {
			t.Fatalf("private service role %s must not be an API gateway proxy target", role)
		}
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
	defer func() { _ = resp.Body.Close() }()
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
		tenant       string
		userID       string
		mobile       string
		publicTenant string
		auth         string
		adminKey     string
		adminRole    string
		permissions  string
	}
	observed := make(chan observedHeaders, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- observedHeaders{
			tenant:       r.Header.Get(gateway.GatewayTenantIDHeader),
			userID:       r.Header.Get(gateway.GatewayUserIDHeader),
			mobile:       r.Header.Get(gateway.GatewayMobileHeader),
			publicTenant: r.Header.Get("X-Tenant-ID"),
			auth:         r.Header.Get("Authorization"),
			adminKey:     r.Header.Get("X-Admin-Key"),
			adminRole:    r.Header.Get("X-Admin-Role"),
			permissions:  r.Header.Get("X-Admin-Permissions"),
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
	req.Header.Set("X-Tenant-ID", "public-tenant")
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
	if got.publicTenant != "" || got.auth != "" || got.adminKey != "" || got.adminRole != "" || got.permissions != "" {
		t.Fatalf("gateway forwarded public credentials on user route: %+v", got)
	}
}

func TestAPIGatewayRejectsUserTenantQueryBeforeProxy(t *testing.T) {
	ensureInit()
	authorization := testAuthorizationHeader(t)
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/wallet/methods?tenant_id=test-tenant", nil)
	req.Header.Set("Authorization", authorization)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if hits.Load() != 0 {
		t.Fatalf("upstream hits = %d, want 0", hits.Load())
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
			r.Header.Get("X-Tenant-ID") == "" &&
			r.Header.Get("X-Admin-Key") == "" &&
			r.Header.Get("X-Admin-Role") == "" &&
			r.Header.Get("X-Admin-Permissions") == ""
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/assets/app.css", nil)
	setTestGatewayUserIdentityHeaders(req)
	setGatewayAdminIdentityHeader(req)
	req.Header.Set(gateway.GatewayAdminPermissionsHeader, "config:manage")
	req.Header.Set("Authorization", "Bearer public-token")
	req.Header.Set("X-Tenant-ID", "public-tenant")
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

func TestAPIGatewayPropagatesValidatedPublicTenant(t *testing.T) {
	ensureInit()
	type observedHeaders struct {
		internalTenant string
		publicTenant   string
		auth           string
		adminKey       string
	}
	observed := make(chan observedHeaders, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- observedHeaders{
			internalTenant: r.Header.Get(gateway.GatewayTenantIDHeader),
			publicTenant:   r.Header.Get("X-Tenant-ID"),
			auth:           r.Header.Get("Authorization"),
			adminKey:       r.Header.Get("X-Admin-Key"),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/consumer/login", nil)
	req.Header.Set("X-Tenant-ID", " test-tenant ")
	req.Header.Set(gateway.GatewayTenantIDHeader, "spoofed-tenant")
	req.Header.Set("Authorization", "Bearer public-token")
	req.Header.Set("X-Admin-Key", "public-admin")
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	assertGatewayProxied(t, resp)

	got := <-observed
	if got.internalTenant != "test-tenant" {
		t.Fatalf("forwarded tenant = %q, want test-tenant", got.internalTenant)
	}
	if got.publicTenant != "" || got.auth != "" || got.adminKey != "" {
		t.Fatalf("gateway forwarded public tenant or credentials: %+v", got)
	}
}

func TestAPIGatewayRejectsMissingPublicTenantBeforeProxy(t *testing.T) {
	ensureInit()
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/consumer/login", nil)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if hits.Load() != 0 {
		t.Fatalf("upstream hits = %d, want 0", hits.Load())
	}
}

func TestAPIGatewayPropagatesValidatedWebhookQueryTenant(t *testing.T) {
	ensureInit()
	type observedHeaders struct {
		internalTenant string
		publicTenant   string
		auth           string
		adminKey       string
	}
	observed := make(chan observedHeaders, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- observedHeaders{
			internalTenant: r.Header.Get(gateway.GatewayTenantIDHeader),
			publicTenant:   r.Header.Get("X-Tenant-ID"),
			auth:           r.Header.Get("Authorization"),
			adminKey:       r.Header.Get("X-Admin-Key"),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/noop?tenant_id=%20test-tenant%20", nil)
	req.Header.Set("X-Tenant-ID", "ignored-public-tenant")
	req.Header.Set(gateway.GatewayTenantIDHeader, "spoofed-tenant")
	req.Header.Set("Authorization", "Bearer public-token")
	req.Header.Set("X-Admin-Key", "public-admin")
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	assertGatewayProxied(t, resp)

	got := <-observed
	if got.internalTenant != "test-tenant" {
		t.Fatalf("forwarded tenant = %q, want test-tenant", got.internalTenant)
	}
	if got.publicTenant != "" || got.auth != "" || got.adminKey != "" {
		t.Fatalf("gateway forwarded public webhook tenant or credentials: %+v", got)
	}
}

func TestAPIGatewayRejectsWebhookTenantFallbacksBeforeProxy(t *testing.T) {
	ensureInit()
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/noop", strings.NewReader("tenant_id=test-tenant"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Tenant-ID", "test-tenant")
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if hits.Load() != 0 {
		t.Fatalf("upstream hits = %d, want 0", hits.Load())
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
	defer func() { _ = resp.Body.Close() }()
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

	req := httptest.NewRequest(http.MethodPost, "/generate_api_key", nil)
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

func TestAPIGatewayPropagatesValidatedWalletAdminQueryTenant(t *testing.T) {
	ensureInit()
	adminKey := setAdminKeyForTest(t)
	type observedHeaders struct {
		internalTenant string
		publicTenant   string
		adminIdentity  string
		adminRole      string
		adminKey       string
		auth           string
	}
	observed := make(chan observedHeaders, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- observedHeaders{
			internalTenant: r.Header.Get(gateway.GatewayTenantIDHeader),
			publicTenant:   r.Header.Get("X-Tenant-ID"),
			adminIdentity:  r.Header.Get(gateway.GatewayAdminIdentityHeader),
			adminRole:      r.Header.Get(gateway.GatewayAdminRoleHeader),
			adminKey:       r.Header.Get("X-Admin-Key"),
			auth:           r.Header.Get("Authorization"),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/admin/wallet/?tenant_id=%20test-tenant%20", nil)
	req.Header.Set("X-Admin-Key", adminKey)
	req.Header.Set("X-Tenant-ID", "ignored-public-tenant")
	req.Header.Set(gateway.GatewayTenantIDHeader, "spoofed-tenant")
	req.Header.Set(gateway.GatewayAdminIdentityHeader, "spoofed-admin")
	req.Header.Set("Authorization", "Basic public")
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	assertGatewayProxied(t, resp)

	got := <-observed
	if got.internalTenant != "test-tenant" {
		t.Fatalf("forwarded tenant = %q, want test-tenant", got.internalTenant)
	}
	if got.adminIdentity != gateway.GatewayAdminIdentityValue || got.adminRole != gateway.GatewayAdminRoleValue {
		t.Fatalf("forwarded admin identity = %+v", got)
	}
	if got.publicTenant != "" || got.adminKey != "" || got.auth != "" {
		t.Fatalf("gateway forwarded public wallet admin tenant or credentials: %+v", got)
	}
}

func TestAPIGatewayPropagatesValidatedWalletAdminFormTenant(t *testing.T) {
	ensureInit()
	adminKey := setAdminKeyForTest(t)
	observed := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Header.Get(gateway.GatewayTenantIDHeader)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/admin/wallet/manual", strings.NewReader("tenant_id=%20test-tenant%20"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Admin-Key", adminKey)
	req.Header.Set(gateway.GatewayTenantIDHeader, "spoofed-tenant")
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	assertGatewayProxied(t, resp)

	if got := <-observed; got != "test-tenant" {
		t.Fatalf("forwarded tenant = %q, want test-tenant", got)
	}
}

func TestAPIGatewayRejectsWalletAdminTenantFallbacksBeforeProxy(t *testing.T) {
	ensureInit()
	adminKey := setAdminKeyForTest(t)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		header func(*http.Request)
	}{
		{
			name:   "get ignores public tenant header",
			method: http.MethodGet,
			path:   "/admin/wallet/",
			header: func(req *http.Request) { req.Header.Set("X-Tenant-ID", "test-tenant") },
		},
		{
			name:   "post ignores query tenant",
			method: http.MethodPost,
			path:   "/admin/wallet/manual?tenant_id=test-tenant",
			body:   "amount=100",
			header: func(req *http.Request) { req.Header.Set("Content-Type", "application/x-www-form-urlencoded") },
		},
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

			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("X-Admin-Key", adminKey)
			if tt.header != nil {
				tt.header(req)
			}
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
			if hits.Load() != 0 {
				t.Fatalf("upstream hits = %d, want 0", hits.Load())
			}
		})
	}
}

func TestAPIGatewayPropagatesValidatedAdminTenant(t *testing.T) {
	ensureInit()
	adminKey := setAdminKeyForTest(t)
	type observedHeaders struct {
		internalTenant string
		publicTenant   string
		adminIdentity  string
		adminRole      string
		adminKey       string
		auth           string
	}
	observed := make(chan observedHeaders, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- observedHeaders{
			internalTenant: r.Header.Get(gateway.GatewayTenantIDHeader),
			publicTenant:   r.Header.Get("X-Tenant-ID"),
			adminIdentity:  r.Header.Get(gateway.GatewayAdminIdentityHeader),
			adminRole:      r.Header.Get(gateway.GatewayAdminRoleHeader),
			adminKey:       r.Header.Get("X-Admin-Key"),
			auth:           r.Header.Get("Authorization"),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	req.Header.Set("X-Admin-Key", adminKey)
	req.Header.Set("X-Tenant-ID", " test-tenant ")
	req.Header.Set(gateway.GatewayTenantIDHeader, "spoofed-tenant")
	req.Header.Set(gateway.GatewayAdminIdentityHeader, "spoofed-admin")
	req.Header.Set("Authorization", "Basic public")
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	assertGatewayProxied(t, resp)

	got := <-observed
	if got.internalTenant != "test-tenant" {
		t.Fatalf("forwarded tenant = %q, want test-tenant", got.internalTenant)
	}
	if got.adminIdentity != gateway.GatewayAdminIdentityValue || got.adminRole != gateway.GatewayAdminRoleValue {
		t.Fatalf("forwarded admin identity = %+v", got)
	}
	if got.publicTenant != "" || got.adminKey != "" || got.auth != "" {
		t.Fatalf("gateway forwarded public admin tenant or credentials: %+v", got)
	}
}

func TestAPIGatewayRejectsMissingAdminTenantBeforeProxy(t *testing.T) {
	ensureInit()
	adminKey := setAdminKeyForTest(t)
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	req.Header.Set("X-Admin-Key", adminKey)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if hits.Load() != 0 {
		t.Fatalf("upstream hits = %d, want 0", hits.Load())
	}
}
