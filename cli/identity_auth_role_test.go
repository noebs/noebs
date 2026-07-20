package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func identityAuthActiveRoutes() []gatewayRouteExpectation {
	return []gatewayRouteExpectation{
		{method: http.MethodPost, path: "/consumer/auth/profile", auth: gatewayAuthMobilePrincipal},
		{method: http.MethodPost, path: "/consumer/kyc", auth: gatewayAuthMobileUser},
		{method: http.MethodGet, path: "/consumer/user", auth: gatewayAuthMobileUser},
		{method: http.MethodPut, path: "/consumer/user", auth: gatewayAuthMobileUser},
		{method: http.MethodGet, path: "/consumer/user/lang", auth: gatewayAuthMobileUser},
		{method: http.MethodPut, path: "/consumer/user/lang", auth: gatewayAuthMobileUser},
		{method: http.MethodPost, path: "/consumer/user/device", auth: gatewayAuthMobileUser},
	}
}

func identityAuthRemovedRoutes() []roleRoute {
	return []roleRoute{
		{name: "registration", method: http.MethodPost, path: "/consumer/register"},
		{name: "login", method: http.MethodPost, path: "/consumer/login"},
		{name: "refresh", method: http.MethodPost, path: "/consumer/refresh"},
		{name: "OTP generation", method: http.MethodPost, path: "/consumer/otp/generate"},
		{name: "OTP login", method: http.MethodPost, path: "/consumer/otp/login"},
		{name: "OTP verification", method: http.MethodPost, path: "/consumer/otp/verify"},
		{name: "insecure OTP generation", method: http.MethodPost, path: "/consumer/otp/generate_insecure"},
		{name: "recovery request", method: http.MethodPost, path: "/consumer/recovery/request"},
		{name: "recovery verification", method: http.MethodPost, path: "/consumer/recovery/verify"},
		{name: "recovery reset", method: http.MethodPost, path: "/consumer/recovery/reset"},
		{name: "Google auth", method: http.MethodPost, path: "/consumer/auth/google"},
		{name: "mobile user lookup", method: http.MethodPost, path: "/consumer/check_user"},
		{name: "profile completion", method: http.MethodPost, path: "/consumer/auth/complete_profile"},
		{name: "local auth profile", method: http.MethodGet, path: "/consumer/auth/me"},
		{name: "password change", method: http.MethodPost, path: "/consumer/change_password"},
		{name: "API key", method: http.MethodPost, path: "/generate_api_key"},
	}
}

func identityAuthRemovedInternalRoutes() []roleRoute {
	return []roleRoute{
		{name: "completed card registration user", method: http.MethodPost, path: "/internal/identity-auth/card-registration/users"},
		{name: "register-with-card identity", method: http.MethodPost, path: "/internal/identity-auth/register-with-card/users"},
		{name: "recovery credential", method: http.MethodPost, path: "/internal/identity-auth/recovery-credential"},
		{name: "session validation", method: http.MethodPost, path: "/internal/identity-auth/sessions/validate"},
		{name: "user by mobile", method: http.MethodPost, path: "/internal/identity-auth/users/by-mobile"},
		{name: "batch mobile resolution", method: http.MethodPost, path: "/internal/identity-auth/users/resolve-batch"},
	}
}

func TestIdentityAuthGatewayCatalogIsExact(t *testing.T) {
	assertGatewayRoleCatalogExact(t, serviceRoleIdentityAuth, identityAuthActiveRoutes())
}

func TestIdentityAuthActiveRoutesAreOwnedByIdentityAuth(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleIdentityAuth)
	app := GetMainEngine()

	for _, route := range identityAuthActiveRoutes() {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			assertFiberRoutePresent(t, app, route.method, route.path)
		})
	}
	assertFiberRoutePresent(t, app, http.MethodPost, "/internal/identity-auth/principals/resolve")
}

func TestIdentityAuthRemovedRoutesAreAbsent(t *testing.T) {
	for _, route := range identityAuthRemovedRoutes() {
		assertGatewayCatalogAbsent(t, route.method, route.path)
	}

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "gateway", setup: configureGatewayProxyForTest},
		{name: "service", setup: func(t *testing.T) { setServiceRoleForTest(t, serviceRoleIdentityAuth) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ensureInit()
			test.setup(t)
			app := GetMainEngine()
			for _, route := range identityAuthRemovedRoutes() {
				t.Run(route.name, func(t *testing.T) {
					assertFiberRouteAbsent(t, app, route)
				})
			}
		})
	}
}

func TestIdentityAuthRemovedInternalRoutesAreAbsent(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleIdentityAuth)
	app := GetMainEngine()

	for _, route := range identityAuthRemovedInternalRoutes() {
		t.Run(route.name, func(t *testing.T) {
			assertFiberRouteAbsent(t, app, route)
		})
	}
}

func TestIdentityAuthKYCRequiresGatewayUserIdentity(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleIdentityAuth)
	app := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/consumer/kyc", nil)
	resp, err := app.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer closeResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestIdentityAuthDoesNotOwnEBSOrCardRoutes(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleIdentityAuth)
	app := GetMainEngine()

	for _, route := range []roleRoute{
		{name: "EBS balance", method: http.MethodPost, path: "/consumer/balance"},
		{name: "EBS enrollment", method: http.MethodPost, path: "/consumer/cards/enrollment-intents"},
		{name: "card list", method: http.MethodGet, path: "/consumer/cards"},
	} {
		t.Run(route.name, func(t *testing.T) {
			assertFiberRouteAbsent(t, app, route)
		})
	}
}
