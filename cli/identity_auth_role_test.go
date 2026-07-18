package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIdentityRoutesAreProxiedByAPIGateway(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	authorization := testAuthorizationHeader(t)
	adminKey := setAdminKeyForTest(t)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "login", method: http.MethodPost, path: "/consumer/login"},
		{name: "recovery request", method: http.MethodPost, path: "/consumer/recovery/request"},
		{name: "recovery verify", method: http.MethodPost, path: "/consumer/recovery/verify"},
		{name: "recovery reset", method: http.MethodPost, path: "/consumer/recovery/reset"},
		{name: "oauth", method: http.MethodPost, path: "/consumer/auth/google"},
		{name: "profile", method: http.MethodGet, path: "/consumer/auth/me"},
		{name: "device token", method: http.MethodPost, path: "/consumer/user/device"},
		{name: "kyc", method: http.MethodPost, path: "/consumer/kyc"},
		{name: "api key", method: http.MethodPost, path: "/generate_api_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", authorization)
			req.Header.Set("X-Tenant-ID", "test-tenant")
			req.Header.Set("X-Admin-Key", adminKey)
			resp, err := route.Test(req, routeTestTimeout)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			assertGatewayProxied(t, resp)
		})
	}
}

func TestIdentityRoutesAreOwnedByIdentityAuth(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleIdentityAuth)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "login", method: http.MethodPost, path: "/consumer/login"},
		{name: "recovery request", method: http.MethodPost, path: "/consumer/recovery/request"},
		{name: "recovery verify", method: http.MethodPost, path: "/consumer/recovery/verify"},
		{name: "recovery reset", method: http.MethodPost, path: "/consumer/recovery/reset"},
		{name: "oauth", method: http.MethodPost, path: "/consumer/auth/google"},
		{name: "profile", method: http.MethodGet, path: "/consumer/auth/me"},
		{name: "device token", method: http.MethodPost, path: "/consumer/user/device"},
		{name: "kyc", method: http.MethodPost, path: "/consumer/kyc"},
		{name: "api key", method: http.MethodPost, path: "/generate_api_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req, routeTestTimeout)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				t.Fatalf("identity-auth did not register %s", tt.path)
			}
		})
	}
}

func TestIdentityAuthKYCRejectsMissingGatewayIdentity(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleIdentityAuth)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/consumer/kyc", nil)
	resp, err := route.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestLegacyInsecureOTPRouteIsNotExposed(t *testing.T) {
	ensureInit()
	for _, spec := range gatewayProxyRouteSpecs() {
		if spec.method == http.MethodPost && spec.path == "/consumer/otp/generate_insecure" {
			t.Fatalf("%s must not be proxied by api-gateway", spec.path)
		}
	}

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{
			name: "api gateway",
			setup: func(t *testing.T) {
				t.Helper()
				configureGatewayProxyForTest(t)
			},
		},
		{
			name: "identity auth",
			setup: func(t *testing.T) {
				t.Helper()
				setServiceRoleForTest(t, serviceRoleIdentityAuth)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			route := GetMainEngine()
			for _, registered := range route.GetRoutes(true) {
				if registered.Method == http.MethodPost && registered.Path == "/consumer/otp/generate_insecure" {
					t.Fatalf("%s registered by %s", registered.Path, tt.name)
				}
			}
		})
	}
}

func TestIdentityAuthOwnsCardRegistrationInternalCommand(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleIdentityAuth)
	route := GetMainEngine()

	tests := []struct {
		name string
		path string
	}{
		{name: "completed card registration user", path: "/internal/identity-auth/card-registration/users"},
		{name: "register with card identity", path: "/internal/identity-auth/register-with-card/users"},
		{name: "recovery credential", path: "/internal/identity-auth/recovery-credential"},
		{name: "session validation", path: "/internal/identity-auth/sessions/validate"},
		{name: "user by mobile", path: "/internal/identity-auth/users/by-mobile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			setGatewayAdminTenantIdentityHeaders(req, "test-tenant")
			resp, err := route.Test(req, routeTestTimeout)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			assertFiberRouteRegistered(t, resp, http.MethodPost, tt.path)
		})
	}
}

func TestIdentityAuthDoesNotOwnEBSOrCardRoutes(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleIdentityAuth)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "ebs balance", method: http.MethodPost, path: "/consumer/balance"},
		{name: "recovery balance", method: http.MethodPost, path: "/consumer/otp/balance"},
		{name: "register with card", method: http.MethodPost, path: "/consumer/register_with_card"},
		{name: "card list", method: http.MethodGet, path: "/consumer/get_cards"},
		{name: "payment token", method: http.MethodPost, path: "/consumer/payment_token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req, routeTestTimeout)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
			}
		})
	}
}
