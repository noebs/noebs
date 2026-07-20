package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/gofiber/fiber/v2"
)

type roleRoute struct {
	name        string
	method      string
	path        string
	requestPath string
}

func (r roleRoute) target() string {
	if r.requestPath != "" {
		return r.requestPath
	}
	return r.path
}

type gatewayRouteExpectation struct {
	method    string
	path      string
	auth      gatewayAuthMode
	websocket bool
}

func assertGatewayRoleCatalogExact(t *testing.T, role serviceRole, expected []gatewayRouteExpectation) {
	t.Helper()
	want := make(map[string]gatewayRouteExpectation, len(expected))
	for _, spec := range expected {
		key := spec.method + " " + spec.path
		if _, exists := want[key]; exists {
			t.Fatalf("duplicate expected gateway route %s", key)
		}
		want[key] = spec
	}

	for _, spec := range gatewayProxyRouteSpecs() {
		if spec.role != role {
			continue
		}
		key := spec.method + " " + spec.path
		expectedSpec, ok := want[key]
		if !ok {
			t.Errorf("unexpected %s gateway route %s", role, key)
			continue
		}
		if spec.auth != expectedSpec.auth {
			t.Errorf("%s auth = %d, want %d", key, spec.auth, expectedSpec.auth)
		}
		if spec.websocket != expectedSpec.websocket {
			t.Errorf("%s websocket = %t, want %t", key, spec.websocket, expectedSpec.websocket)
		}
		delete(want, key)
	}
	for key := range want {
		t.Errorf("missing %s gateway route %s", role, key)
	}
}

func assertGatewayCatalogAbsent(t *testing.T, method, path string) {
	t.Helper()
	for _, spec := range gatewayProxyRouteSpecs() {
		if spec.method == method && spec.path == path {
			t.Fatalf("gateway registered retired route %s %s for %s", method, path, spec.role)
		}
	}
}

func assertFiberRoutePresent(t *testing.T, app *fiber.App, method, path string) {
	t.Helper()
	for _, route := range app.GetRoutes(true) {
		if route.Method == method && route.Path == path {
			return
		}
	}
	t.Fatalf("route not registered: %s %s", method, path)
}

func assertFiberRouteAbsent(t *testing.T, app *fiber.App, route roleRoute) {
	t.Helper()
	for _, registered := range app.GetRoutes(true) {
		if registered.Method == route.method && registered.Path == route.path {
			t.Fatalf("retired route remains registered: %s %s", route.method, route.path)
		}
	}

	req := httptest.NewRequest(route.method, route.target(), nil)
	resp, err := app.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer closeResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want an unregistered route response", resp.StatusCode)
	}
}

func ebsAdapterActiveConsumerRoutes() []roleRoute {
	return []roleRoute{
		{name: "opaque enrollment intent", method: http.MethodPost, path: "/consumer/cards/enrollment-intents"},
		{name: "opaque enrollment confirmation", method: http.MethodPost, path: "/consumer/cards/enrollment-intents/:enrollment_id/confirm", requestPath: "/consumer/cards/enrollment-intents/enrollment-1/confirm"},
		{name: "balance", method: http.MethodPost, path: "/consumer/balance"},
		{name: "status", method: http.MethodPost, path: "/consumer/status"},
		{name: "alive", method: http.MethodPost, path: "/consumer/is_alive"},
		{name: "cached biller", method: http.MethodGet, path: "/consumer/biller"},
		{name: "notification status", method: http.MethodPost, path: "/consumer/n/status"},
		{name: "meter lookup", method: http.MethodGet, path: "/consumer/nec2name"},
		{name: "qr registration", method: http.MethodPost, path: "/consumer/generate_qr"},
		{name: "qr status", method: http.MethodPost, path: "/consumer/qr_status"},
		{name: "qr refund", method: http.MethodPost, path: "/consumer/qr_refund"},
		{name: "qr completion", method: http.MethodPost, path: "/consumer/qr_complete"},
		{name: "transaction", method: http.MethodGet, path: "/consumer/transaction"},
		{name: "transactions", method: http.MethodGet, path: "/consumer/transactions"},
	}
}

func ebsAdapterRemovedConsumerRoutes() []roleRoute {
	return []roleRoute{
		{name: "card info", method: http.MethodPost, path: "/consumer/card_info"},
		{name: "recovery balance", method: http.MethodPost, path: "/consumer/otp/balance"},
		{name: "register with card", method: http.MethodPost, path: "/consumer/register_with_card"},
		{name: "card registration start", method: http.MethodPost, path: "/consumer/cards/new"},
		{name: "card registration completion", method: http.MethodPost, path: "/consumer/cards/complete"},
		{name: "working key", method: http.MethodPost, path: "/consumer/key"},
		{name: "ipin key", method: http.MethodPost, path: "/consumer/ipin_key"},
		{name: "bill payment", method: http.MethodPost, path: "/consumer/bill_payment"},
		{name: "bills", method: http.MethodPost, path: "/consumer/bills"},
		{name: "bill inquiry", method: http.MethodPost, path: "/consumer/bill_inquiry"},
		{name: "p2p", method: http.MethodPost, path: "/consumer/p2p"},
		{name: "mobile p2p", method: http.MethodPost, path: "/consumer/p2p_mobile"},
		{name: "cash in", method: http.MethodPost, path: "/consumer/cashIn"},
		{name: "cash out", method: http.MethodPost, path: "/consumer/cashOut"},
		{name: "account transfer", method: http.MethodPost, path: "/consumer/account"},
		{name: "ipin change", method: http.MethodPost, path: "/consumer/ipin"},
		{name: "qr payment", method: http.MethodPost, path: "/consumer/qr_payment"},
		{name: "generate ipin", method: http.MethodPost, path: "/consumer/generate_ipin"},
		{name: "complete ipin", method: http.MethodPost, path: "/consumer/complete_ipin"},
		{name: "voucher", method: http.MethodPost, path: "/consumer/vouchers/generate"},
	}
}

func legacyMerchantRoutes() []roleRoute {
	return []roleRoute{
		{name: "merchant proxy", method: http.MethodPost, path: "/ebs/balance"},
		{name: "merchant working key", method: http.MethodPost, path: "/workingKey"},
		{name: "merchant card transfer", method: http.MethodPost, path: "/cardTransfer"},
		{name: "merchant voucher", method: http.MethodPost, path: "/voucher"},
		{name: "merchant voucher cash in", method: http.MethodPost, path: "/voucher/cash_in"},
		{name: "merchant cashout", method: http.MethodPost, path: "/cashout"},
		{name: "merchant voucher cash out", method: http.MethodPost, path: "/voucher/cash_out"},
		{name: "merchant purchase", method: http.MethodPost, path: "/purchase"},
		{name: "merchant cash in", method: http.MethodPost, path: "/cashIn"},
		{name: "merchant cash out", method: http.MethodPost, path: "/cashOut"},
		{name: "merchant bill inquiry", method: http.MethodPost, path: "/billInquiry"},
		{name: "merchant bill payment", method: http.MethodPost, path: "/billPayment"},
		{name: "merchant bills", method: http.MethodPost, path: "/bills"},
		{name: "merchant change pin", method: http.MethodPost, path: "/changePin"},
		{name: "merchant mini statement", method: http.MethodPost, path: "/miniStatement"},
		{name: "merchant alive", method: http.MethodPost, path: "/isAlive"},
		{name: "merchant balance", method: http.MethodPost, path: "/balance"},
		{name: "merchant refund", method: http.MethodPost, path: "/refund"},
		{name: "merchant account transfer", method: http.MethodPost, path: "/toAccount"},
		{name: "merchant statement", method: http.MethodPost, path: "/statement"},
	}
}

func TestEBSAdapterGatewayCatalogIsExact(t *testing.T) {
	expected := make([]gatewayRouteExpectation, 0, len(ebsAdapterActiveConsumerRoutes()))
	for _, route := range ebsAdapterActiveConsumerRoutes() {
		expected = append(expected, gatewayRouteExpectation{
			method: route.method,
			path:   route.path,
			auth:   gatewayAuthMobileUser,
		})
	}
	assertGatewayRoleCatalogExact(t, serviceRoleEBSAdapter, expected)
}

func TestEBSAdapterActiveRoutesAreOwnedByEBSAdapter(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleEBSAdapter)
	app := GetMainEngine()

	for _, route := range ebsAdapterActiveConsumerRoutes() {
		t.Run(route.name, func(t *testing.T) {
			assertFiberRoutePresent(t, app, route.method, route.path)
		})
	}
}

func TestEBSAdapterRemovedRoutesAreAbsent(t *testing.T) {
	for _, route := range ebsAdapterRemovedConsumerRoutes() {
		assertGatewayCatalogAbsent(t, route.method, route.path)
	}

	t.Run("gateway", func(t *testing.T) {
		ensureInit()
		configureGatewayProxyForTest(t)
		app := GetMainEngine()
		for _, route := range ebsAdapterRemovedConsumerRoutes() {
			t.Run(route.name, func(t *testing.T) {
				assertFiberRouteAbsent(t, app, route)
			})
		}
	})

	t.Run("service", func(t *testing.T) {
		ensureInit()
		setServiceRoleForTest(t, serviceRoleEBSAdapter)
		app := GetMainEngine()
		for _, route := range ebsAdapterRemovedConsumerRoutes() {
			t.Run(route.name, func(t *testing.T) {
				assertFiberRouteAbsent(t, app, route)
			})
		}
	})
}

func TestEBSAdapterActiveRoutesRequireGatewayUserIdentity(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleEBSAdapter)
	app := GetMainEngine()

	for _, route := range ebsAdapterActiveConsumerRoutes() {
		t.Run(route.name, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.target(), nil)
			req.Header.Set(gateway.GatewayTenantIDHeader, "test-tenant")
			resp, err := app.Test(req, routeTestTimeout)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}
}

func TestLegacyMerchantRoutesStayAbsent(t *testing.T) {
	for _, route := range legacyMerchantRoutes() {
		catalogPath := route.path
		if strings.HasPrefix(catalogPath, "/ebs/") {
			catalogPath = "/ebs/*"
		}
		assertGatewayCatalogAbsent(t, route.method, catalogPath)
	}

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "gateway", setup: configureGatewayProxyForTest},
		{name: "service", setup: func(t *testing.T) { setServiceRoleForTest(t, serviceRoleEBSAdapter) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ensureInit()
			test.setup(t)
			app := GetMainEngine()
			for _, route := range legacyMerchantRoutes() {
				t.Run(route.name, func(t *testing.T) {
					assertFiberRouteAbsent(t, app, route)
				})
			}
		})
	}
}

func TestEBSAdapterDoesNotOwnOtherServiceRoutes(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleEBSAdapter)
	app := GetMainEngine()

	for _, route := range []roleRoute{
		{name: "identity profile", method: http.MethodGet, path: "/consumer/user"},
		{name: "card list", method: http.MethodGet, path: "/consumer/cards"},
		{name: "notification websocket", method: http.MethodGet, path: "/ws"},
		{name: "wallet", method: http.MethodPost, path: "/wallet/wallets"},
	} {
		t.Run(route.name, func(t *testing.T) {
			assertFiberRouteAbsent(t, app, route)
		})
	}
}

func TestEBSAdapterNeverExposesUnownedCompatibilityRoutes(t *testing.T) {
	routes := []roleRoute{
		{name: "guess biller", method: http.MethodGet, path: "/consumer/guess_biller"},
		{name: "mobile PAN lookup", method: http.MethodPost, path: "/consumer/pan_from_mobile"},
		{name: "manual test", method: http.MethodGet, path: "/wrk"},
	}
	for _, route := range routes {
		assertGatewayCatalogAbsent(t, route.method, route.path)
	}

	ensureInit()
	setServiceRoleForTest(t, serviceRoleEBSAdapter)
	app := GetMainEngine()
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			assertFiberRouteAbsent(t, app, route)
		})
	}
}
