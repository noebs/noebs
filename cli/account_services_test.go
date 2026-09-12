package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func TestAccountServicesUseIndependentBackendCapabilities(t *testing.T) {
	for _, cfg := range []ebs_fields.NoebsConfig{
		{},
		{WalletEnabled: true},
		{OpaqueCardManagementEnabled: true},
		{ChatEnabled: true},
	} {
		services := accountServices(cfg)
		seen := make(map[string]bool)
		for _, service := range services {
			if seen[service.ID] || service.Action != service.ID {
				t.Fatalf("duplicate or inconsistent action: %+v", service)
			}
			seen[service.ID] = true
			want := false
			switch service.ID {
			case "send_money", "add_money", "activity":
				want = cfg.WalletEnabled
			case "identity", "profile":
				want = true
			case "cards":
				want = cfg.OpaqueCardManagementEnabled
			case "chat":
				want = cfg.ChatEnabled
			default:
				t.Fatalf("unsupported customer action: %s", service.ID)
			}
			if service.Available != want {
				t.Fatalf("service %s availability = %v, want %v", service.ID, service.Available, want)
			}
		}
	}
}

func TestAccountServicesRouteUsesAuthenticatedCustomerBoundary(t *testing.T) {
	handler, _ := newWalletAuthorizationHTTPTestHandler(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/identity-auth/principals/resolve" {
			t.Errorf("unexpected upstream request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":42}`))
	}))
	t.Cleanup(upstream.Close)
	discovery := make(map[string]string)
	for _, spec := range gatewayProxyRouteSpecs() {
		discovery[string(spec.role)] = upstream.URL
	}
	previousSigners, previousVerifier := workloadSigners, oidcVerifier
	workloadSigners = newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleIdentityAuth))
	verifier, token := walletAuthorizationOIDCVerifierAndTokenForTest(t)
	oidcVerifier = verifier
	t.Cleanup(func() { workloadSigners = previousSigners; oidcVerifier = previousVerifier })
	app := fiber.New(fiber.Config{DisableStartupMessage: true, ErrorHandler: gateway.JSONErrorHandler})
	app.Use(gateway.RequestID())
	if err := registerAPIGatewayProxyRoutes(app, ebs_fields.NoebsConfig{
		DefaultTenantID: walletAuthorizationTestTenant, WalletEnabled: true,
		ServiceDiscovery: discovery,
		PSPWebhookRoutes: map[string]ebs_fields.PSPWebhookRoute{
			testPSPWebhookCallbackID: {TenantID: walletAuthorizationTestTenant, ProviderCode: "test-provider"},
		},
	}, gatewayTestTenantCatalog(t), handler); err != nil {
		t.Fatal(err)
	}
	for _, authenticated := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/consumer/services", nil)
		request.Header.Set("X-Active-Tenant", walletAuthorizationTestTenant)
		request.Header.Set("X-Forwarded-For", "203.0.113.9")
		if authenticated {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		if !authenticated {
			closeResponseBody(t, response.Body)
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("anonymous status: %d", response.StatusCode)
			}
			continue
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("authenticated status: %d", response.StatusCode)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("account services may be cached across accounts")
		}
		var payload struct {
			Services []accountService `json:"services"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		closeResponseBody(t, response.Body)
		if len(payload.Services) != 7 {
			t.Fatalf("services: %+v", payload.Services)
		}
	}
}
