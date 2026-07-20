package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func adminReportingActiveDownstreamRoutes() []roleRoute {
	return []roleRoute{
		{name: "dashboard", method: http.MethodGet, path: "/dashboard"},
		{name: "dashboard slash", method: http.MethodGet, path: "/dashboard/"},
		{name: "transaction by TID", method: http.MethodGet, path: "/dashboard/get_tid"},
		{name: "transaction lookup", method: http.MethodGet, path: "/dashboard/get"},
		{name: "all transactions", method: http.MethodGet, path: "/dashboard/all"},
		{name: "transaction by ID", method: http.MethodGet, path: "/dashboard/all/:id", requestPath: "/dashboard/all/1"},
		{name: "transaction count", method: http.MethodGet, path: "/dashboard/count"},
		{name: "merchant transactions", method: http.MethodGet, path: "/dashboard/merchant"},
		{name: "QR status", method: http.MethodGet, path: "/dashboard/status"},
		{name: "test browser", method: http.MethodGet, path: "/dashboard/test_browser"},
		{name: "stream", method: http.MethodGet, path: "/dashboard/stream"},
	}
}

func adminReportingRemovedRoutes() []roleRoute {
	return []roleRoute{
		{name: "dummy transaction", method: http.MethodGet, path: "/dashboard/create"},
		{name: "merchant issue", method: http.MethodPost, path: "/dashboard/issues"},
		{name: "projection write", method: http.MethodPost, path: "/internal/admin-reporting/transactions"},
	}
}

func TestAdminReportingGatewayCatalogIsExact(t *testing.T) {
	assertGatewayRoleCatalogExact(t, serviceRoleAdminReporting, []gatewayRouteExpectation{
		{method: http.MethodGet, path: "/backoffice/assets/*", auth: gatewayAuthPublic},
	})
}

func TestAdminReportingDynamicRoutesAreDownstreamOnly(t *testing.T) {
	for _, route := range adminReportingActiveDownstreamRoutes() {
		assertGatewayCatalogAbsent(t, route.method, route.path)
	}

	t.Run("gateway returns 404", func(t *testing.T) {
		ensureInit()
		configureGatewayProxyForTest(t)
		app := GetMainEngine()
		for _, route := range adminReportingActiveDownstreamRoutes() {
			t.Run(route.name, func(t *testing.T) {
				assertFiberRouteAbsent(t, app, route)
			})
		}
	})

	t.Run("service owns route", func(t *testing.T) {
		ensureInit()
		setServiceRoleForTest(t, serviceRoleAdminReporting)
		app := GetMainEngine()
		for _, route := range adminReportingActiveDownstreamRoutes() {
			t.Run(route.name, func(t *testing.T) {
				assertFiberRoutePresent(t, app, route.method, route.path)
			})
		}
	})
}

func TestAdminReportingServesEmbeddedDashboardAssets(t *testing.T) {
	ensureInit()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})
	setServiceRoleForTest(t, serviceRoleAdminReporting)
	app := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/assets/style.css", nil)
	resp, err := app.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer closeResponseBody(t, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "padding-top") {
		t.Fatalf("embedded dashboard asset body = %q", body)
	}
}

func TestAdminReportingRemovedRoutesAreAbsent(t *testing.T) {
	for _, route := range adminReportingRemovedRoutes() {
		assertGatewayCatalogAbsent(t, route.method, route.path)
	}

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "gateway", setup: configureGatewayProxyForTest},
		{name: "service", setup: func(t *testing.T) { setServiceRoleForTest(t, serviceRoleAdminReporting) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ensureInit()
			test.setup(t)
			app := GetMainEngine()
			for _, route := range adminReportingRemovedRoutes() {
				t.Run(route.name, func(t *testing.T) {
					assertFiberRouteAbsent(t, app, route)
				})
			}
		})
	}
}

func TestPaymentServicesDoNotCommandAdminReporting(t *testing.T) {
	forbidden := []string{
		"/internal/admin-reporting/transactions",
		"admin_reporting_command_failed",
		"missing_admin_reporting_service_discovery",
		"invalid_admin_reporting_service_discovery",
	}
	for _, packageDir := range []string{"consumer", "merchant"} {
		dir := filepath.Join("..", packageDir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			for _, needle := range forbidden {
				if strings.Contains(string(data), needle) {
					t.Fatalf("%s contains %q; payment services must not synchronously command admin-reporting", path, needle)
				}
			}
		}
	}
}
