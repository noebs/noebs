package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func setServiceRoleForTest(t *testing.T, role serviceRole) {
	t.Helper()
	previousRole := noebsConfig.ServiceRole
	noebsConfig.ServiceRole = string(role)
	t.Cleanup(func() {
		noebsConfig.ServiceRole = previousRole
	})
}

func configureGatewayProxyForTest(t *testing.T) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Path", r.URL.RequestURI())
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
}

func setGatewayDiscoveryForTest(t *testing.T, endpoint string) {
	t.Helper()
	previousDiscovery := noebsConfig.ServiceDiscovery
	noebsConfig.ServiceDiscovery = map[string]string{}
	for _, spec := range gatewayProxyRouteSpecs() {
		noebsConfig.ServiceDiscovery[string(spec.role)] = endpoint
	}
	t.Cleanup(func() {
		noebsConfig.ServiceDiscovery = previousDiscovery
	})
}

func setAdminKeyForTest(t *testing.T) string {
	t.Helper()
	previousKey := noebsConfig.AdminKey
	noebsConfig.AdminKey = "test-admin-key"
	t.Cleanup(func() {
		noebsConfig.AdminKey = previousKey
	})
	return noebsConfig.AdminKey
}

func assertGatewayProxied(t *testing.T, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}
