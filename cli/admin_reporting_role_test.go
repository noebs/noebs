package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashboardRouteIsProxiedByAPIGateway(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	adminKey := setAdminKeyForTest(t)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	req.Header.Set("X-Admin-Key", adminKey)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	assertGatewayProxied(t, resp)
}

func TestDashboardReadRouteIsOwnedByAdminReporting(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleAdminReporting)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("admin-reporting did not register dashboard read route")
	}
}

func TestAdminReportingAppliesDefaultTenantAtHTTPBoundary(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleAdminReporting)
	adminKey := setAdminKeyForTest(t)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	req.Header.Set("X-Admin-Key", adminKey)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestAdminReportingDoesNotOwnDashboardWriteRoutes(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleAdminReporting)
	wasDebug := noebsConfig.IsDebug
	noebsConfig.IsDebug = true
	t.Cleanup(func() {
		noebsConfig.IsDebug = wasDebug
	})
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "dummy transaction", method: http.MethodGet, path: "/dashboard/create"},
		{name: "merchant issue", method: http.MethodPost, path: "/dashboard/issues"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
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
