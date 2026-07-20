package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func cardVaultActiveRoutes() []roleRoute {
	return []roleRoute{
		{name: "card list", method: http.MethodGet, path: "/consumer/cards"},
		{name: "rename card", method: http.MethodPatch, path: "/consumer/cards/:card_id", requestPath: "/consumer/cards/card-1"},
		{name: "retire card", method: http.MethodDelete, path: "/consumer/cards/:card_id", requestPath: "/consumer/cards/card-1"},
		{name: "main card", method: http.MethodPut, path: "/consumer/cards/:card_id/main", requestPath: "/consumer/cards/card-1/main"},
	}
}

func cardVaultInternalRoutes() []roleRoute {
	return []roleRoute{
		{name: "create enrollment intent", method: http.MethodPost, path: "/internal/card-vault/enrollment-intents"},
		{name: "begin enrollment", method: http.MethodPost, path: "/internal/card-vault/enrollment-intents/begin"},
		{name: "claim enrollment rail", method: http.MethodPost, path: "/internal/card-vault/enrollment-intents/claim-rail"},
		{name: "complete enrollment", method: http.MethodPost, path: "/internal/card-vault/enrollment-intents/complete"},
		{name: "fail enrollment", method: http.MethodPost, path: "/internal/card-vault/enrollment-intents/fail"},
		{name: "claim funded operation", method: http.MethodPost, path: "/internal/card-vault/funded-operations/claim"},
	}
}

func TestCardVaultGatewayCatalogIsExact(t *testing.T) {
	expected := make([]gatewayRouteExpectation, 0, len(cardVaultActiveRoutes()))
	for _, route := range cardVaultActiveRoutes() {
		expected = append(expected, gatewayRouteExpectation{
			method: route.method,
			path:   route.path,
			auth:   gatewayAuthMobileUser,
		})
	}
	assertGatewayRoleCatalogExact(t, serviceRoleCardVault, expected)
}

func TestCardVaultActiveRoutesAreOwnedByCardVault(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	app := GetMainEngine()

	for _, route := range cardVaultActiveRoutes() {
		t.Run(route.name, func(t *testing.T) {
			assertFiberRoutePresent(t, app, route.method, route.path)
		})
	}
}

func TestCardVaultInternalCatalogIsExact(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	app := GetMainEngine()

	for _, route := range cardVaultInternalRoutes() {
		t.Run("active/"+route.name, func(t *testing.T) {
			assertFiberRoutePresent(t, app, route.method, route.path)
		})
	}
}

func TestCardVaultFundedOperationClaimRequiresGatewayTenantIdentity(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	app := GetMainEngine()
	const path = "/internal/card-vault/funded-operations/claim"

	req := httptest.NewRequest(http.MethodPost, path, nil)
	resp, err := app.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer closeResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestCardVaultDoesNotOwnOtherServiceRoutes(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	app := GetMainEngine()

	for _, route := range []roleRoute{
		{name: "identity profile", method: http.MethodGet, path: "/consumer/user"},
		{name: "EBS balance", method: http.MethodPost, path: "/consumer/balance"},
		{name: "EBS transactions", method: http.MethodGet, path: "/consumer/transactions"},
		{name: "notification websocket", method: http.MethodGet, path: "/ws"},
	} {
		t.Run(route.name, func(t *testing.T) {
			assertFiberRouteAbsent(t, app, route)
		})
	}
}
