package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestAppConfigEndpointReturnsPublicConfig(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)

	originalCfg := noebsConfig
	t.Cleanup(func() {
		noebsConfig = originalCfg
	})
	noebsConfig.DefaultTenantID = "tenant-1"
	noebsConfig.WalletEnabled = true
	noebsConfig.WalletDefaultCurrency = "SDG"
	noebsConfig.WalletPINRequired = true
	noebsConfig.OpaqueCardManagementEnabled = true
	noebsConfig.OpaqueBalanceEnabled = false
	noebsConfig.ChatEnabled = true
	noebsConfig.OIDC.Issuer = "https://identity.example/realms/noebs"
	noebsConfig.OIDC.Audience = "noebs-api"

	route := GetMainEngine()
	req := httptest.NewRequest(http.MethodGet, "/app/config", nil)
	resp, err := route.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload appConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.TenantID != "tenant-1" {
		t.Fatalf("tenant_id = %q, want tenant-1", payload.TenantID)
	}
	if !payload.Wallet.Enabled {
		t.Fatalf("wallet.enabled = false, want true")
	}
	if payload.Wallet.DefaultCurrency != "SDG" {
		t.Fatalf("wallet.default_currency = %q, want SDG", payload.Wallet.DefaultCurrency)
	}
	if !payload.Features.OpaqueCardManagement || payload.Features.OpaqueBalance || !payload.Features.Chat {
		t.Fatalf("features = %+v, want independently configured gates", payload.Features)
	}
	if payload.OAuth.Issuer != noebsConfig.OIDC.Issuer || payload.OAuth.ClientID != "noebs-mobile" || payload.OAuth.Audience != "noebs-api" ||
		!slices.Equal(payload.OAuth.Scopes, []string{"openid", "organization:*"}) ||
		payload.OAuth.RedirectURI != "https://api.noebs.sd/mobile/oauth/callback" {
		t.Fatalf("oauth = %+v, want Keycloak mobile client metadata", payload.OAuth)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(body), "secret") {
		t.Fatalf("app config leaked sensitive data: %s", body)
	}
}

func TestPublicAppConfigCapabilitiesDefaultOff(t *testing.T) {
	payload, err := publicAppConfig(ebs_fields.NoebsConfig{DefaultTenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("publicAppConfig(): %v", err)
	}
	if payload.Features != (appFeatureConfig{}) {
		t.Fatalf("features = %+v, want every capability disabled", payload.Features)
	}
}

func TestPublicAppConfigRequiresTenant(t *testing.T) {
	_, err := publicAppConfig(ebs_fields.NoebsConfig{})
	if !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("error = %v, want %v", err, store.ErrMissingTenantID)
	}
}

func TestPublicAppConfigRejectsDefaultTenant(t *testing.T) {
	_, err := publicAppConfig(ebs_fields.NoebsConfig{DefaultTenantID: "default"})
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}
