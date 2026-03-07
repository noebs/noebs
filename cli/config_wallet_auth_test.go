package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
)

func TestWalletRoutesRequireAuth(t *testing.T) {
	ensureInit()

	originalAuth := auth
	originalCfg := noebsConfig
	t.Cleanup(func() {
		auth = originalAuth
		noebsConfig = originalCfg
	})

	noebsConfig.WalletEnabled = true
	auth = gateway.JWTAuth{Key: []byte("test-key")}
	token, err := auth.GenerateJWT(42, "0990000000", noebsConfig.DefaultTenantID)
	if err != nil {
		t.Fatalf("GenerateJWT() error = %v", err)
	}

	route := GetMainEngine()

	unauthorizedReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"tenant_id":"test-tenant","user_id":42,"currency":"USD"}`))
	unauthorizedReq.Header.Set("Content-Type", "application/json")
	unauthorizedResp, err := route.Test(unauthorizedReq)
	if err != nil {
		t.Fatalf("unauthorized request failed: %v", err)
	}
	if unauthorizedResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorizedResp.StatusCode, http.StatusUnauthorized)
	}

	authorizedReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"tenant_id":"test-tenant","user_id":42,"currency":"USD"}`))
	authorizedReq.Header.Set("Content-Type", "application/json")
	authorizedReq.Header.Set("Authorization", "Bearer "+token)
	authorizedResp, err := route.Test(authorizedReq)
	if err != nil {
		t.Fatalf("authorized request failed: %v", err)
	}
	if authorizedResp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("authorized status = %d, want non-%d", authorizedResp.StatusCode, http.StatusUnauthorized)
	}
}
