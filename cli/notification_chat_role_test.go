package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	chat "github.com/tutipay/ws"
)

func notificationRemovedRoutes() []roleRoute {
	return []roleRoute{
		{name: "notification pull", method: http.MethodGet, path: "/consumer/notifications"},
		{name: "mobile contact discovery", method: http.MethodPost, path: "/consumer/submit_contacts"},
	}
}

func TestNotificationChatGatewayCatalogIsExact(t *testing.T) {
	assertGatewayRoleCatalogExact(t, serviceRoleNotification, []gatewayRouteExpectation{
		{method: http.MethodGet, path: "/ws", auth: gatewayAuthMobileUser, websocket: true},
	})
}

func TestNotificationChatOwnsWebsocketRoute(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleNotification)
	app := GetMainEngine()
	assertFiberRoutePresent(t, app, http.MethodGet, "/ws")
}

func TestNotificationRemovedRoutesAreAbsent(t *testing.T) {
	for _, route := range notificationRemovedRoutes() {
		assertGatewayCatalogAbsent(t, route.method, route.path)
	}

	ensureInit()
	setServiceRoleForTest(t, serviceRoleNotification)
	app := GetMainEngine()
	for _, route := range notificationRemovedRoutes() {
		t.Run(route.name, func(t *testing.T) {
			assertFiberRouteAbsent(t, app, route)
		})
	}
}

func TestNotificationChatOwnsInternalCommands(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleNotification)
	app := GetMainEngine()

	for _, path := range []string{"/internal/notification-chat/push-data"} {
		t.Run(path, func(t *testing.T) {
			assertFiberRoutePresent(t, app, http.MethodPost, path)
		})
	}
}

func TestNotificationWebsocketRequiresGatewayUserIdentity(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleNotification)
	app := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	resp, err := app.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestChatClientIdentityUsesGatewayIdentity(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Authorization", "Bearer caller-controlled-token")
	if _, err := chatClientIdentityFromGatewayIdentity(req); !errors.Is(err, chat.ErrUnauthorized) {
		t.Fatalf("bearer-only chat identity error = %v, want %v", err, chat.ErrUnauthorized)
	}

	setGatewayUserIdentityHeaders(req, 1, "test-tenant", "")
	if _, err := chatClientIdentityFromGatewayIdentity(req); !errors.Is(err, chat.ErrUnauthorized) {
		t.Fatalf("header-only chat identity error = %v, want %v", err, chat.ErrUnauthorized)
	}
	req = req.WithContext(context.WithValue(req.Context(), chatGatewayIdentityContextKey{}, chatGatewayIdentity{
		PrincipalIdentity: testGatewayPrincipalIdentity("test-tenant", "user", "", 1),
	}))
	got, err := chatClientIdentityFromGatewayIdentity(req)
	if err != nil {
		t.Fatalf("chatClientIdentityFromGatewayIdentity() error = %v", err)
	}
	want := (chat.ClientIdentity{TenantID: "test-tenant", UserID: 1})
	if got != want {
		t.Fatalf("client identity = %+v, want %+v", got, want)
	}
}

func TestDeviceTokenRouteIsNotOwnedByNotificationChat(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleNotification)
	app := GetMainEngine()
	assertFiberRouteAbsent(t, app, roleRoute{
		name:   "identity device token",
		method: http.MethodPost,
		path:   "/consumer/user/device",
	})
}
