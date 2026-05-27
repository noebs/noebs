package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPSPWebhookRouteIsNotOwnedByAPIGateway(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/noop", nil)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	_ = resp.Body.Close()
}

func TestPSPWebhookRouteIsOwnedByPSPWebhookService(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRolePSPWebhook)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/noop", nil)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("psp webhook route was not registered")
	}
	_ = resp.Body.Close()
}
