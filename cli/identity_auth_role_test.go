package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIdentityRoutesAreNotOwnedByAPIGateway(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	authorization := testAuthorizationHeader(t)
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

func TestIdentityAuthDoesNotOwnEBSOrCardRoutes(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleIdentityAuth)
	authorization := testAuthorizationHeader(t)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "ebs balance", method: http.MethodPost, path: "/consumer/balance"},
		{name: "card list", method: http.MethodGet, path: "/consumer/get_cards"},
		{name: "payment token", method: http.MethodPost, path: "/consumer/payment_token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", authorization)
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
