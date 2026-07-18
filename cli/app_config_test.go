package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	noebsConfig.DefaultTenantID = "tenant_1"
	noebsConfig.WalletEnabled = true
	noebsConfig.WalletDefaultCurrency = "SDG"
	noebsConfig.WalletPINRequired = true
	noebsConfig.OpaqueCardManagementEnabled = true
	noebsConfig.OpaqueBalanceEnabled = false
	noebsConfig.ChatEnabled = true
	noebsConfig.NotificationsEnabled = false
	noebsConfig.AdminKey = "secret-admin-key"
	noebsConfig.JWTKey = "secret-jwt"

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
	if payload.TenantID != "tenant_1" {
		t.Fatalf("tenant_id = %q, want tenant_1", payload.TenantID)
	}
	if !payload.Wallet.Enabled {
		t.Fatalf("wallet.enabled = false, want true")
	}
	if payload.Wallet.DefaultCurrency != "SDG" {
		t.Fatalf("wallet.default_currency = %q, want SDG", payload.Wallet.DefaultCurrency)
	}
	if !payload.Features.OpaqueCardManagement || payload.Features.OpaqueBalance || !payload.Features.Chat || payload.Features.Notifications {
		t.Fatalf("features = %+v, want independently configured gates", payload.Features)
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
	payload, err := publicAppConfig(ebs_fields.NoebsConfig{DefaultTenantID: "tenant_1"})
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

func TestEnsureNoReservedTenantRejectsDefaultTenantRow(t *testing.T) {
	ensureInit()

	ctx := context.Background()
	if _, err := storeSvc.DB.ExecContext(ctx, "ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenant_id_not_reserved"); err != nil {
		t.Fatalf("drop reserved tenant constraint: %v", err)
	}
	stmt := storeSvc.DB.Rebind("INSERT INTO tenants(id, name, created_at) VALUES(?, ?, ?) ON CONFLICT(id) DO NOTHING")
	if _, err := storeSvc.DB.ExecContext(ctx, stmt, "default", "default", time.Now().UTC()); err != nil {
		t.Fatalf("insert reserved tenant: %v", err)
	}
	t.Cleanup(func() {
		if storeSvc != nil && storeSvc.DB != nil && storeSvc.DB.DB != nil {
			_, _ = storeSvc.DB.ExecContext(context.Background(), storeSvc.DB.Rebind("DELETE FROM tenants WHERE id = ?"), "default")
			_, _ = storeSvc.DB.ExecContext(context.Background(), "ALTER TABLE tenants ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(id)) <> 'default')")
		}
	})
	err := ensureNoReservedTenant(ctx, storeSvc)
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}
