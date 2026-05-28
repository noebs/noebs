package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
)

func TestPSPWebhookRouteIsProxiedByAPIGateway(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/noop?tenant_id=test-tenant", nil)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	assertGatewayProxied(t, resp)
}

func TestPSPWebhookRouteIsOwnedByPSPWebhookService(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRolePSPWebhook)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/noop", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, "test-tenant")
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("psp webhook route was not registered")
	}
	_ = resp.Body.Close()
}
