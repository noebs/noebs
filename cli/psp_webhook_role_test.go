package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/workloadauth"
)

var pspWebhookDownstreamRoute = roleRoute{
	name:        "signed tenant webhook",
	method:      http.MethodPost,
	path:        "/psp/webhooks/:provider",
	requestPath: "/psp/webhooks/noop",
}

func TestPSPWebhookGatewayCatalogIsCanonical(t *testing.T) {
	assertGatewayRoleCatalogExact(t, serviceRolePSPWebhook, []gatewayRouteExpectation{{
		method: http.MethodPost,
		path:   "/psp/webhooks/:callback_id",
		auth:   gatewayAuthTenantWebhook,
	}})
	assertGatewayCatalogAbsent(t, http.MethodPost, "/psp/webhooks/:provider")
	for _, spec := range gatewayProxyRouteSpecs() {
		if spec.path == "/psp/webhooks/:callback_id" && spec.capabilityPath != "/psp/webhooks/:provider" {
			t.Fatalf("PSP webhook capability path = %q", spec.capabilityPath)
		}
	}
}

func TestPSPWebhookServiceOwnsSignedDownstreamRoute(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRolePSPWebhook)
	assertFiberRoutePresent(t, GetMainEngine(), pspWebhookDownstreamRoute.method, pspWebhookDownstreamRoute.path)
}

func TestPSPWebhookGatewayResolvesOpaqueCallbackBeforeSigning(t *testing.T) {
	ensureInit()
	type observedRequest struct {
		path              string
		body              string
		tenant            string
		source            string
		publicTenant      string
		authorization     string
		adminKey          string
		providerSignature string
	}
	observed := make(chan observedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		observed <- observedRequest{
			path:              r.URL.RequestURI(),
			body:              string(body),
			tenant:            r.Header.Get(gateway.GatewayTenantIDHeader),
			source:            r.Header.Get(gateway.GatewaySourceIPHeader),
			publicTenant:      r.Header.Get("X-Tenant-ID"),
			authorization:     r.Header.Get("Authorization"),
			adminKey:          r.Header.Get("X-Admin-Key"),
			providerSignature: r.Header.Get("X-Webhook-Signature"),
		}
		if r.Header.Get(workloadauth.HeaderSignature) == "" {
			t.Error("workload signature is missing")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
	const payload = `{"status":"ok","tenant_id":"caller-data"}`
	req := httptest.NewRequest(http.MethodPost, "/psp/webhooks/"+testPSPWebhookCallbackID, bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", "provider-signature")
	req.Header.Set("Authorization", "Bearer caller-token")
	req.Header.Set("X-Tenant-ID", "tenant-sandbox")
	req.Header.Set("X-Admin-Key", "caller-admin")
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant-sandbox")
	req.Header.Set(gateway.GatewaySourceIPHeader, "203.0.113.9")
	response, err := GetMainEngine().Test(req, routeTestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	assertGatewayProxied(t, response)

	got := <-observed
	if got.path != "/psp/webhooks/test-provider" {
		t.Fatalf("upstream path = %q", got.path)
	}
	if got.body != payload {
		t.Fatalf("upstream body = %q, want exact payload", got.body)
	}
	if got.tenant != "test-tenant" {
		t.Fatalf("signed tenant = %q, want test-tenant", got.tenant)
	}
	if got.source != "203.0.113.10" {
		t.Fatalf("gateway source = %q, want Caddy-authenticated request source", got.source)
	}
	if got.publicTenant != "" || got.authorization != "" || got.adminKey != "" {
		t.Fatalf("public credentials reached upstream: %#v", got)
	}
	if got.providerSignature != "provider-signature" {
		t.Fatalf("provider signature = %q", got.providerSignature)
	}
}

func TestPSPWebhookGatewayRejectsQueryAndUnknownCallbacks(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	app := GetMainEngine()
	tests := []struct {
		name   string
		path   string
		status int
	}{
		{name: "tenant query", path: "/psp/webhooks/" + testPSPWebhookCallbackID + "?tenant_id=test-tenant", status: http.StatusBadRequest},
		{name: "scope query", path: "/psp/webhooks/" + testPSPWebhookCallbackID + "?region=ae", status: http.StatusBadRequest},
		{name: "unknown callback", path: "/psp/webhooks/BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", status: http.StatusNotFound},
		{name: "old tenant provider path", path: "/psp/webhooks/test-tenant/test-provider", status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(`{"status":"ok"}`))
			response, err := app.Test(req, routeTestTimeout)
			if err != nil {
				t.Fatal(err)
			}
			defer closeResponseBody(t, response.Body)
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
		})
	}
}

func TestPSPWebhookRouteConfigIsExact(t *testing.T) {
	valid := map[string]ebs_fields.PSPWebhookRoute{
		testPSPWebhookCallbackID: {TenantID: "test-tenant", ProviderCode: "test-provider"},
	}
	if err := validatePSPWebhookRoutes(valid); err != nil {
		t.Fatal(err)
	}
	if _, err := newGatewayWebhookResolver(valid, gatewayTestTenantCatalog(t)); err != nil {
		t.Fatal(err)
	}
	unknownTenant := map[string]ebs_fields.PSPWebhookRoute{
		testPSPWebhookCallbackID: {TenantID: "tenant-unknown", ProviderCode: "test-provider"},
	}
	if _, err := newGatewayWebhookResolver(unknownTenant, gatewayTestTenantCatalog(t)); err == nil {
		t.Fatal("gateway webhook resolver accepted a canonical tenant outside the catalog")
	}
	tests := []map[string]ebs_fields.PSPWebhookRoute{
		nil,
		{"short": {TenantID: "test-tenant", ProviderCode: "test-provider"}},
		{testPSPWebhookCallbackID: {TenantID: " test-tenant", ProviderCode: "test-provider"}},
		{testPSPWebhookCallbackID: {TenantID: "test-tenant", ProviderCode: "Test-Provider"}},
		{
			testPSPWebhookCallbackID:                      {TenantID: "test-tenant", ProviderCode: "test-provider"},
			"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB": {TenantID: "test-tenant", ProviderCode: "test-provider"},
		},
	}
	for index, routes := range tests {
		if err := validatePSPWebhookRoutes(routes); err == nil {
			t.Errorf("invalid routes[%d] accepted", index)
		}
	}
}
