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
		{name: "oauth", method: http.MethodPost, path: "/consumer/auth/google"},
		{name: "profile", method: http.MethodGet, path: "/consumer/auth/me"},
		{name: "device token", method: http.MethodPost, path: "/consumer/user/device"},
		{name: "api key", method: http.MethodPost, path: "/generate_api_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", authorization)
			req.Header.Set("X-Admin-Key", adminKey)
			resp, err := route.Test(req)
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
		{name: "oauth", method: http.MethodPost, path: "/consumer/auth/google"},
		{name: "profile", method: http.MethodGet, path: "/consumer/auth/me"},
		{name: "device token", method: http.MethodPost, path: "/consumer/user/device"},
		{name: "api key", method: http.MethodPost, path: "/generate_api_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req)
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
		{name: "user by mobile", path: "/internal/identity-auth/users/by-mobile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			setGatewayAdminIdentityHeader(req)
			resp, err := route.Test(req)
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
		{name: "register with card", method: http.MethodPost, path: "/consumer/register_with_card"},
		{name: "card list", method: http.MethodGet, path: "/consumer/get_cards"},
		{name: "payment token", method: http.MethodPost, path: "/consumer/payment_token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req)
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
