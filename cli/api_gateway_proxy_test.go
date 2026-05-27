package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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

func TestAPIGatewayEnforcesUserAuthBeforeProxy(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/consumer/get_cards", nil)
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

func gatewayRouteKey(method, path string) string {
	return fmt.Sprintf("%s %s", strings.ToUpper(method), path)
}

func isInternalServiceRoute(method, path string) bool {
	switch gatewayRouteKey(method, path) {
	case "GET /test", "GET /metrics":
		return true
	default:
		return method == fiber.MethodHead || method == fiber.MethodOptions
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
