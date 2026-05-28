package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	chat "github.com/tutipay/ws"
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

func TestNotificationRoutesAreProxiedByAPIGateway(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	authorization := testAuthorizationHeader(t)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "websocket", method: http.MethodGet, path: "/ws"},
		{name: "notifications", method: http.MethodGet, path: "/consumer/notifications"},
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
			assertGatewayProxied(t, resp)
		})
	}
}

func TestNotificationRoutesAreOwnedByNotificationChat(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleNotification)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "notifications", method: http.MethodGet, path: "/consumer/notifications"},
		{name: "contacts", method: http.MethodPost, path: "/consumer/submit_contacts"},
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
				t.Fatalf("notification-chat did not register %s", tt.path)
			}
		})
	}
}

func TestNotificationChatOwnsInternalCommands(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleNotification)
	route := GetMainEngine()

	tests := []string{
		"/internal/notification-chat/push-data",
		"/internal/notification-chat/biller-hook",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			setGatewayAdminTenantIdentityHeaders(req, "test-tenant")
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			assertFiberRouteRegistered(t, resp, http.MethodPost, path)
		})
	}
}

func TestNotificationWebsocketRejectsBearerWithoutGatewayIdentity(t *testing.T) {
	ensureInit()
	authorization := testAuthorizationHeader(t)
	setServiceRoleForTest(t, serviceRoleNotification)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", authorization)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestChatClientIDUsesGatewayIdentity(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", testAuthorizationHeader(t))
	if _, err := chatClientIDFromGatewayIdentity(req); !errors.Is(err, chat.ErrUnauthorized) {
		t.Fatalf("bearer-only chat identity error = %v, want %v", err, chat.ErrUnauthorized)
	}

	setGatewayUserIdentityHeaders(req, 1, "test-tenant", "0912345678")
	if _, err := chatClientIDFromGatewayIdentity(req); !errors.Is(err, chat.ErrUnauthorized) {
		t.Fatalf("header-only chat identity error = %v, want %v", err, chat.ErrUnauthorized)
	}
	req = req.WithContext(context.WithValue(req.Context(), chatGatewayIdentityContextKey{}, gateway.UserIdentity{
		TenantID: "test-tenant",
		UserID:   1,
		Mobile:   "0912345678",
	}))
	got, err := chatClientIDFromGatewayIdentity(req)
	if err != nil {
		t.Fatalf("chatClientIDFromGatewayIdentity() error = %v", err)
	}
	if got != "0912345678" {
		t.Fatalf("client id = %q, want %q", got, "0912345678")
	}
}

func TestDeviceTokenRouteIsNotOwnedByNotificationChat(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleNotification)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/consumer/user/device", nil)
	setTestGatewayUserIdentityHeaders(req)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}
