package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
)

func TestDashboardRouteIsProxiedByAPIGateway(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	adminKey := setAdminKeyForTest(t)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	req.Header.Set("X-Admin-Key", adminKey)
	req.Header.Set("X-Tenant-ID", noebsConfig.DefaultTenantID)
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
	req.Header.Set(gateway.GatewayTenantIDHeader, noebsConfig.DefaultTenantID)
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

func TestAdminReportingDoesNotExposeInternalTransactionProjectionWrites(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleAdminReporting)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/internal/admin-reporting/transactions", nil)
	setGatewayAdminIdentityHeader(req)
	req.Header.Set(gateway.GatewayTenantIDHeader, noebsConfig.DefaultTenantID)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
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
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestAdminReportingAcceptsGatewayTenantHeader(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleAdminReporting)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/dashboard/count", nil)
	setGatewayAdminIdentityHeader(req)
	req.Header.Set(gateway.GatewayTenantIDHeader, noebsConfig.DefaultTenantID)
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
}

func TestAdminReportingIgnoresPublicTenantHeader(t *testing.T) {
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
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
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
