package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletgrpc "github.com/adonese/noebs/wallet/grpc"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type walletRouteResponse struct {
	ID     string `json:"id"`
	UserID *int64 `json:"user_id"`
}

type walletTransactionsRouteResponse struct {
	Transactions []any `json:"transactions"`
}

type walletMethodsRouteResponse struct {
	Methods []any `json:"methods"`
}

type walletRouteTemporalClient struct{}

func (walletRouteTemporalClient) SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg interface{}) error {
	return nil
}

func (walletRouteTemporalClient) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
	return nil, nil
}

func configureWalletRouteTest(t *testing.T) {
	t.Helper()
	ensureInit()
	previousRoleServices := captureRoleServices()
	t.Cleanup(previousRoleServices.restore)
	if err := initRoleServices(serviceRoleWalletLedger); err != nil {
		t.Fatalf("init wallet-ledger services: %v", err)
	}
	if walletService == nil {
		t.Fatal("wallet service not initialized")
	}

	originalAuth := auth
	originalCfg := noebsConfig
	originalWalletCfg := walletService.Config
	originalWorkflowClient := walletWorkflowClient
	originalWorkflowCloser := walletWorkflowCloser
	originalWalletPublicClient := walletPublicClient
	originalWalletAdminClient := walletAdminClient
	originalWalletLedgerConn := walletLedgerGRPCConn
	var testGRPCServer *grpc.Server
	var testGRPCListener *bufconn.Listener
	var testGRPCConn *grpc.ClientConn
	t.Cleanup(func() {
		if testGRPCConn != nil {
			_ = testGRPCConn.Close()
		}
		if testGRPCServer != nil {
			testGRPCServer.Stop()
		}
		if testGRPCListener != nil {
			_ = testGRPCListener.Close()
		}
		auth = originalAuth
		noebsConfig = originalCfg
		walletService.Config = originalWalletCfg
		walletWorkflowClient = originalWorkflowClient
		walletWorkflowCloser = originalWorkflowCloser
		walletPublicClient = originalWalletPublicClient
		walletAdminClient = originalWalletAdminClient
		walletLedgerGRPCConn = originalWalletLedgerConn
	})

	noebsConfig.WalletEnabled = true
	noebsConfig.WalletDefaultCurrency = "USD"
	noebsConfig.JWTKey = "test-key"
	noebsConfig.ServiceRole = string(serviceRoleWalletAPI)
	auth = gateway.JWTAuth{NoebsConfig: noebsConfig}
	auth.Init()
	walletService.Config = noebsConfig
	walletWorkflowClient = walletRouteTemporalClient{}
	walletWorkflowCloser = nil

	testGRPCListener = bufconn.Listen(1024 * 1024)
	testGRPCServer = grpc.NewServer(grpc.UnaryInterceptor(requireAuthForWalletMethods))
	walletSrv := walletgrpc.NewServer(walletService)
	walletSrv.TemporalClient = walletRouteTemporalClient{}
	walletv1.RegisterWalletPublicServiceServer(testGRPCServer, walletSrv)
	walletv1.RegisterWalletAdminServiceServer(testGRPCServer, walletSrv)
	go func() {
		_ = testGRPCServer.Serve(testGRPCListener)
	}()
	conn, err := grpc.NewClient("passthrough:///wallet-ledger-test",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return testGRPCListener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("create wallet-ledger grpc test client: %v", err)
	}
	testGRPCConn = conn
	walletLedgerGRPCConn = conn
	walletPublicClient = walletv1.NewWalletPublicServiceClient(conn)
	walletAdminClient = walletv1.NewWalletAdminServiceClient(conn)
}

func walletToken(t *testing.T, userID int64) string {
	t.Helper()
	token, err := auth.GenerateJWT(userID, "0990000000", noebsConfig.DefaultTenantID)
	if err != nil {
		t.Fatalf("GenerateJWT() error = %v", err)
	}
	return token
}

func setWalletGatewayIdentity(req *http.Request, userID int64) {
	setGatewayUserIdentityHeaders(req, userID, noebsConfig.DefaultTenantID, "0990000000")
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
	setWalletGatewayIdentity(authorizedReq, 42)
	authorizedResp, err := route.Test(authorizedReq)
	if err != nil {
		t.Fatalf("authorized request failed: %v", err)
	}
	if authorizedResp.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", authorizedResp.StatusCode, http.StatusOK)
	}
	_ = authorizedResp.Body.Close()
}

func TestWalletRoutesRequireExplicitCurrency(t *testing.T) {
	configureWalletRouteTest(t)

	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	setWalletGatewayIdentity(req, 42)

	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	_ = resp.Body.Close()
}

func TestWalletRoutesAreProxiedByAPIGateway(t *testing.T) {
	configureWalletRouteTest(t)
	configureGatewayProxyForTest(t)
	adminKey := setAdminKeyForTest(t)

	token := walletToken(t, 42)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "user wallet", method: http.MethodPost, path: "/wallet/wallets"},
		{name: "wallet methods", method: http.MethodGet, path: "/wallet/methods"},
		{name: "wallet admin", method: http.MethodGet, path: "/admin/wallet?tenant_id=" + url.QueryEscape(noebsConfig.DefaultTenantID)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Admin-Key", adminKey)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			assertGatewayProxied(t, resp)
		})
	}
}

func TestWalletRoutesTakePrecedenceOverGRPCGateway(t *testing.T) {
	configureWalletRouteTest(t)

	originalGatewayHandler := grpcGatewayHandler
	t.Cleanup(func() {
		grpcGatewayHandler = originalGatewayHandler
	})
	grpcGatewayHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	req.Header.Set("Content-Type", "application/json")
	setWalletGatewayIdentity(req, 42)

	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	_ = resp.Body.Close()
}

func TestWalletRoutesUseGatewayIdentity(t *testing.T) {
	configureWalletRouteTest(t)

	route := GetMainEngine()

	mismatchUserReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"user_id":99,"currency":"USD"}`))
	mismatchUserReq.Header.Set("Content-Type", "application/json")
	setWalletGatewayIdentity(mismatchUserReq, 42)
	mismatchUserResp, err := route.Test(mismatchUserReq, 5_000)
	if err != nil {
		t.Fatalf("mismatch user request failed: %v", err)
	}
	if mismatchUserResp.StatusCode != http.StatusForbidden {
		t.Fatalf("mismatch user status = %d, want %d", mismatchUserResp.StatusCode, http.StatusForbidden)
	}
	_ = mismatchUserResp.Body.Close()

	tenantOverrideReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"tenant_id":"other-tenant","currency":"USD"}`))
	tenantOverrideReq.Header.Set("Content-Type", "application/json")
	setWalletGatewayIdentity(tenantOverrideReq, 42)
	mismatchTenantResp, err := route.Test(tenantOverrideReq, 5_000)
	if err != nil {
		t.Fatalf("tenant override request failed: %v", err)
	}
	if mismatchTenantResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("tenant override status = %d, want %d", mismatchTenantResp.StatusCode, http.StatusBadRequest)
	}
	_ = mismatchTenantResp.Body.Close()

	ownerWalletReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	ownerWalletReq.Header.Set("Content-Type", "application/json")
	setWalletGatewayIdentity(ownerWalletReq, 42)
	ownerWalletResp, err := route.Test(ownerWalletReq, 5_000)
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
	setWalletGatewayIdentity(foreignWalletReq, 7)
	foreignWalletResp, err := route.Test(foreignWalletReq, 5_000)
	if err != nil {
		t.Fatalf("foreign wallet request failed: %v", err)
	}
	if foreignWalletResp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign wallet status = %d, want %d", foreignWalletResp.StatusCode, http.StatusNotFound)
	}
	_ = foreignWalletResp.Body.Close()

	ownerGetReq := httptest.NewRequest(http.MethodGet, "/wallet/wallets/"+ownerWallet.ID, nil)
	setWalletGatewayIdentity(ownerGetReq, 42)
	ownerGetResp, err := route.Test(ownerGetReq, 5_000)
	if err != nil {
		t.Fatalf("owner get wallet request failed: %v", err)
	}
	if ownerGetResp.StatusCode != http.StatusOK {
		t.Fatalf("owner get wallet status = %d, want %d", ownerGetResp.StatusCode, http.StatusOK)
	}
	_ = ownerGetResp.Body.Close()

	ownerHistoryReq := httptest.NewRequest(http.MethodGet, "/wallet/wallets/"+ownerWallet.ID+"/transactions", nil)
	setWalletGatewayIdentity(ownerHistoryReq, 42)
	ownerHistoryResp, err := route.Test(ownerHistoryReq, 5_000)
	if err != nil {
		t.Fatalf("owner wallet history request failed: %v", err)
	}
	if ownerHistoryResp.StatusCode != http.StatusOK {
		t.Fatalf("owner wallet history status = %d, want %d", ownerHistoryResp.StatusCode, http.StatusOK)
	}
	var history walletTransactionsRouteResponse
	if err := json.NewDecoder(ownerHistoryResp.Body).Decode(&history); err != nil {
		t.Fatalf("decode wallet history: %v", err)
	}
	_ = ownerHistoryResp.Body.Close()
	if history.Transactions == nil {
		t.Fatalf("wallet history transactions is nil")
	}

	foreignHistoryReq := httptest.NewRequest(http.MethodGet, "/wallet/wallets/"+ownerWallet.ID+"/transactions", nil)
	setWalletGatewayIdentity(foreignHistoryReq, 7)
	foreignHistoryResp, err := route.Test(foreignHistoryReq, 5_000)
	if err != nil {
		t.Fatalf("foreign wallet history request failed: %v", err)
	}
	if foreignHistoryResp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign wallet history status = %d, want %d", foreignHistoryResp.StatusCode, http.StatusNotFound)
	}
	_ = foreignHistoryResp.Body.Close()
}

func TestWalletRoutesRejectMalformedIdentityOverrides(t *testing.T) {
	configureWalletRouteTest(t)

	route := GetMainEngine()

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{
			name:       "zero user id override",
			method:     http.MethodPost,
			path:       "/wallet/wallets",
			body:       `{"user_id":0,"currency":"USD"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "negative user id override",
			method:     http.MethodPost,
			path:       "/wallet/wallets",
			body:       `{"user_id":-1,"currency":"USD"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "blank tenant override",
			method:     http.MethodPost,
			path:       "/wallet/wallets",
			body:       `{"tenant_id":"   ","currency":"USD"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "null tenant override",
			method:     http.MethodPost,
			path:       "/wallet/wallets",
			body:       `{"tenant_id":null,"currency":"USD"}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			setWalletGatewayIdentity(req, 42)

			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			_ = resp.Body.Close()
		})
	}
}

func TestWalletGetRouteRejectsTenantQuery(t *testing.T) {
	configureWalletRouteTest(t)

	route := GetMainEngine()

	ownerWalletReq := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	ownerWalletReq.Header.Set("Content-Type", "application/json")
	setWalletGatewayIdentity(ownerWalletReq, 42)
	ownerWalletResp, err := route.Test(ownerWalletReq)
	if err != nil {
		t.Fatalf("owner wallet request failed: %v", err)
	}
	if ownerWalletResp.StatusCode != http.StatusOK {
		t.Fatalf("owner wallet status = %d, want %d", ownerWalletResp.StatusCode, http.StatusOK)
	}
	ownerWallet := decodeWalletRouteResponse(t, ownerWalletResp)

	req := httptest.NewRequest(http.MethodGet, "/wallet/wallets/"+ownerWallet.ID+"?tenant_id="+url.QueryEscape(noebsConfig.DefaultTenantID), nil)
	setWalletGatewayIdentity(req, 42)

	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("tenant query request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("tenant query status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	_ = resp.Body.Close()
}

func TestWalletMethodsRouteUsesLedgerGRPC(t *testing.T) {
	configureWalletRouteTest(t)

	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/wallet/methods?direction=deposit", nil)
	setWalletGatewayIdentity(req, 42)

	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("wallet methods request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wallet methods status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var methods walletMethodsRouteResponse
	if err := json.NewDecoder(resp.Body).Decode(&methods); err != nil {
		t.Fatalf("decode wallet methods: %v", err)
	}
	_ = resp.Body.Close()
	if methods.Methods == nil {
		t.Fatalf("wallet methods is nil")
	}
}

func TestWalletAdminRouteUsesLedgerGRPC(t *testing.T) {
	configureWalletRouteTest(t)

	route := GetMainEngine()
	req := httptest.NewRequest(http.MethodGet, "/admin/wallet/?tenant_id="+url.QueryEscape(noebsConfig.DefaultTenantID), nil)
	setGatewayAdminIdentityHeader(req)
	req.Header.Set(gateway.GatewayTenantIDHeader, noebsConfig.DefaultTenantID)

	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("wallet admin request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wallet admin status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q, want text/html; charset=utf-8", contentType)
	}
	_ = resp.Body.Close()
}

func TestWalletAdminRouteRequiresGatewayTenantIdentity(t *testing.T) {
	configureWalletRouteTest(t)

	route := GetMainEngine()
	req := httptest.NewRequest(http.MethodGet, "/admin/wallet/", nil)
	setGatewayAdminIdentityHeader(req)

	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("wallet admin request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wallet admin status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	_ = resp.Body.Close()
}

func TestWalletRoutesRequireGatewayTenantIdentity(t *testing.T) {
	configureWalletRouteTest(t)

	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gateway.GatewayUserIDHeader, "42")

	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	_ = resp.Body.Close()
}
