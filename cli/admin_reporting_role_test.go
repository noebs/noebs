package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	setGatewayAdminIdentityHeader(req)
	req.Header.Set("X-Tenant-ID", noebsConfig.DefaultTenantID)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("admin-reporting did not register dashboard read route")
	}
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
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/assets/style.css", nil)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
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

func TestAdminReportingRequiresExplicitTenantAtHTTPBoundary(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleAdminReporting)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	setGatewayAdminIdentityHeader(req)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestAdminReportingAcceptsExplicitTenantHeader(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleAdminReporting)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	setGatewayAdminIdentityHeader(req)
	req.Header.Set("X-Tenant-ID", noebsConfig.DefaultTenantID)
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
			setGatewayAdminIdentityHeader(req)
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
