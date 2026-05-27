package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func testAuthorizationHeader(t *testing.T) string {
	t.Helper()
	previousConfig := noebsConfig
	previousAuth := auth
	noebsConfig.JWTKey = "test-key"
	auth.NoebsConfig = noebsConfig
	auth.Init()
	t.Cleanup(func() {
		noebsConfig = previousConfig
		auth = previousAuth
	})
	token, err := auth.GenerateJWT(1, "0912345678", "test-tenant")
	if err != nil {
		t.Fatalf("GenerateJWT() error = %v", err)
	}
	return "Bearer " + token
}

func TestNotificationRoutesAreNotOwnedByAPIGateway(t *testing.T) {
	ensureInit()
	t.Setenv("NOEBS_SERVICE", string(serviceRoleAPIGateway))
	authorization := testAuthorizationHeader(t)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "websocket", method: http.MethodGet, path: "/ws"},
		{name: "notifications", method: http.MethodGet, path: "/consumer/notifications"},
		{name: "device token", method: http.MethodPost, path: "/consumer/user/device"},
		{name: "contacts", method: http.MethodPost, path: "/consumer/submit_contacts"},
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

func TestNotificationRoutesAreOwnedByNotificationChat(t *testing.T) {
	ensureInit()
	t.Setenv("NOEBS_SERVICE", string(serviceRoleNotification))
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "notifications", method: http.MethodGet, path: "/consumer/notifications"},
		{name: "device token", method: http.MethodPost, path: "/consumer/user/device"},
		{name: "contacts", method: http.MethodPost, path: "/consumer/submit_contacts"},
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
				t.Fatalf("notification-chat did not register %s", tt.path)
			}
		})
	}
}
