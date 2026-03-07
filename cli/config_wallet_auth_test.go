package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
)

type walletRouteResponse struct {
	ID     string `json:"id"`
	UserID *int64 `json:"user_id"`
}

func configureWalletRouteTest(t *testing.T) {
	t.Helper()
	ensureInit()
	if walletService == nil {
		t.Fatal("wallet service not initialized")
	}

	originalAuth := auth
	originalCfg := noebsConfig
	originalWalletCfg := walletService.Config
	t.Cleanup(func() {
		auth = originalAuth
		noebsConfig = originalCfg
		walletService.Config = originalWalletCfg
	})

	noebsConfig.WalletEnabled = true
	noebsConfig.WalletDefaultCurrency = "USD"
	auth = gateway.JWTAuth{Key: []byte("test-key")}
	walletService.Config = noebsConfig
}

func walletToken(t *testing.T, userID int64) string {
	t.Helper()
	token, err := auth.GenerateJWT(userID, "0990000000", noebsConfig.DefaultTenantID)
	if err != nil {
		t.Fatalf("GenerateJWT() error = %v", err)
	}
	return token
}

func decodeWalletRouteResponse(t *testing.T, resp *http.Response) walletRouteResponse {
	t.Helper()
	defer resp.Body.Close()

	var payload walletRouteResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func TestWalletRoutesRequireAuth(t *testing.T) {
	configureWalletRouteTest(t)

	token := walletToken(t, 42)
	route := GetMainEngine()

	unauthorizedReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	unauthorizedReq.Header.Set("Content-Type", "application/json")
	unauthorizedResp, err := route.Test(unauthorizedReq)
	if err != nil {
		t.Fatalf("unauthorized request failed: %v", err)
	}
	if unauthorizedResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorizedResp.StatusCode, http.StatusUnauthorized)
	}
	_ = unauthorizedResp.Body.Close()

	authorizedReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	authorizedReq.Header.Set("Content-Type", "application/json")
	authorizedReq.Header.Set("Authorization", "Bearer "+token)
	authorizedResp, err := route.Test(authorizedReq)
	if err != nil {
		t.Fatalf("authorized request failed: %v", err)
	}
	if authorizedResp.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", authorizedResp.StatusCode, http.StatusOK)
	}
	_ = authorizedResp.Body.Close()
}

func TestWalletRoutesUseJWTIdentity(t *testing.T) {
	configureWalletRouteTest(t)

	ownerToken := walletToken(t, 42)
	otherToken := walletToken(t, 7)
	route := GetMainEngine()

	mismatchUserReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"user_id":99,"currency":"USD"}`))
	mismatchUserReq.Header.Set("Content-Type", "application/json")
	mismatchUserReq.Header.Set("Authorization", "Bearer "+ownerToken)
	mismatchUserResp, err := route.Test(mismatchUserReq)
	if err != nil {
		t.Fatalf("mismatch user request failed: %v", err)
	}
	if mismatchUserResp.StatusCode != http.StatusForbidden {
		t.Fatalf("mismatch user status = %d, want %d", mismatchUserResp.StatusCode, http.StatusForbidden)
	}
	_ = mismatchUserResp.Body.Close()

	mismatchTenantReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"tenant_id":"other-tenant","currency":"USD"}`))
	mismatchTenantReq.Header.Set("Content-Type", "application/json")
	mismatchTenantReq.Header.Set("Authorization", "Bearer "+ownerToken)
	mismatchTenantResp, err := route.Test(mismatchTenantReq)
	if err != nil {
		t.Fatalf("mismatch tenant request failed: %v", err)
	}
	if mismatchTenantResp.StatusCode != http.StatusForbidden {
		t.Fatalf("mismatch tenant status = %d, want %d", mismatchTenantResp.StatusCode, http.StatusForbidden)
	}
	_ = mismatchTenantResp.Body.Close()

	ownerWalletReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	ownerWalletReq.Header.Set("Content-Type", "application/json")
	ownerWalletReq.Header.Set("Authorization", "Bearer "+ownerToken)
	ownerWalletResp, err := route.Test(ownerWalletReq)
	if err != nil {
		t.Fatalf("owner wallet request failed: %v", err)
	}
	if ownerWalletResp.StatusCode != http.StatusOK {
		t.Fatalf("owner wallet status = %d, want %d", ownerWalletResp.StatusCode, http.StatusOK)
	}
	ownerWallet := decodeWalletRouteResponse(t, ownerWalletResp)
	if ownerWallet.ID == "" {
		t.Fatalf("expected wallet id")
	}
	if ownerWallet.UserID == nil || *ownerWallet.UserID != 42 {
		t.Fatalf("wallet user_id = %v, want %d", ownerWallet.UserID, 42)
	}

	foreignWalletReq := httptest.NewRequest(http.MethodGet, "/wallet/wallets/"+ownerWallet.ID, nil)
	foreignWalletReq.Header.Set("Authorization", "Bearer "+otherToken)
	foreignWalletResp, err := route.Test(foreignWalletReq)
	if err != nil {
		t.Fatalf("foreign wallet request failed: %v", err)
	}
	if foreignWalletResp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign wallet status = %d, want %d", foreignWalletResp.StatusCode, http.StatusNotFound)
	}
	_ = foreignWalletResp.Body.Close()

	ownerGetReq := httptest.NewRequest(http.MethodGet, "/wallet/wallets/"+ownerWallet.ID, nil)
	ownerGetReq.Header.Set("Authorization", "Bearer "+ownerToken)
	ownerGetResp, err := route.Test(ownerGetReq)
	if err != nil {
		t.Fatalf("owner get wallet request failed: %v", err)
	}
	if ownerGetResp.StatusCode != http.StatusOK {
		t.Fatalf("owner get wallet status = %d, want %d", ownerGetResp.StatusCode, http.StatusOK)
	}
	_ = ownerGetResp.Body.Close()
}
