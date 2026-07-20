package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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

type walletRouteLedger struct {
	walletv1.UnimplementedWalletPublicServiceServer
	walletv1.UnimplementedWalletAdminServiceServer

	mu      sync.RWMutex
	wallets map[string]*walletv1.Wallet
}

func newWalletRouteLedger() *walletRouteLedger {
	return &walletRouteLedger{wallets: make(map[string]*walletv1.Wallet)}
}

func (s *walletRouteLedger) EnsureWalletPublic(ctx context.Context, req *walletv1.EnsureWalletPublicRequest) (*walletv1.EnsureWalletPublicResponse, error) {
	principal, err := walletRoutePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetTenantId() != principal.TenantID || req.GetUserId() != principal.UserID {
		return nil, status.Error(codes.PermissionDenied, "wallet owner mismatch")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	wallet := &walletv1.Wallet{
		Id:        uuid.NewString(),
		TenantId:  req.GetTenantId(),
		OwnerType: "user",
		OwnerId:   strconv.FormatInt(req.GetUserId(), 10),
		Currency:  req.GetCurrency(),
		Status:    "active",
		KycTier:   "basic",
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	s.wallets[wallet.GetId()] = wallet
	s.mu.Unlock()
	return &walletv1.EnsureWalletPublicResponse{Wallet: wallet}, nil
}

func (s *walletRouteLedger) GetWalletPublic(ctx context.Context, req *walletv1.GetWalletPublicRequest) (*walletv1.GetWalletPublicResponse, error) {
	principal, err := walletRoutePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	wallet := s.wallets[req.GetWalletId()]
	s.mu.RUnlock()
	if wallet == nil || wallet.GetTenantId() != principal.TenantID || wallet.GetOwnerId() != strconv.FormatInt(principal.UserID, 10) {
		return nil, status.Error(codes.NotFound, "wallet not found")
	}
	return &walletv1.GetWalletPublicResponse{Wallet: wallet}, nil
}

func (s *walletRouteLedger) ListWalletTransactionsPublic(ctx context.Context, req *walletv1.ListWalletTransactionsPublicRequest) (*walletv1.ListWalletTransactionsPublicResponse, error) {
	if _, err := s.GetWalletPublic(ctx, &walletv1.GetWalletPublicRequest{TenantId: req.GetTenantId(), WalletId: req.GetWalletId()}); err != nil {
		return nil, err
	}
	return &walletv1.ListWalletTransactionsPublicResponse{Transactions: []*walletv1.WalletLedgerEntry{}}, nil
}

func (s *walletRouteLedger) ListPaymentMethodsPublic(context.Context, *walletv1.ListPaymentMethodsPublicRequest) (*walletv1.ListPaymentMethodsPublicResponse, error) {
	return &walletv1.ListPaymentMethodsPublicResponse{Methods: []*walletv1.PaymentMethod{}}, nil
}

func (s *walletRouteLedger) RenderWalletAdmin(context.Context, *walletv1.RenderWalletAdminRequest) (*walletv1.RenderWalletAdminResponse, error) {
	return &walletv1.RenderWalletAdminResponse{
		StatusCode:  200,
		ContentType: "text/html; charset=utf-8",
		Body:        []byte("<!doctype html><title>Wallet</title>"),
	}, nil
}

func walletRoutePrincipal(ctx context.Context) (gateway.PrincipalIdentity, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return gateway.PrincipalIdentity{}, status.Error(codes.Unauthenticated, "missing gateway identity")
	}
	principal, ok := gatewayPrincipalFromMetadata(md)
	if !ok {
		return gateway.PrincipalIdentity{}, status.Error(codes.Unauthenticated, "invalid gateway identity")
	}
	return principal, nil
}

func configureWalletRouteTest(t *testing.T) {
	t.Helper()
	ensureInit()
	previousRoleServices := captureRoleServices()
	t.Cleanup(previousRoleServices.restore)

	originalCfg := noebsConfig
	originalWalletPublicClient := walletPublicClient
	originalWalletAdminClient := walletAdminClient
	originalWalletLedgerConn := walletLedgerGRPCConn
	originalWorkloadVerifier := workloadVerifier
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
		noebsConfig = originalCfg
		walletPublicClient = originalWalletPublicClient
		walletAdminClient = originalWalletAdminClient
		walletLedgerGRPCConn = originalWalletLedgerConn
		workloadVerifier = originalWorkloadVerifier
	})

	noebsConfig.WalletEnabled = true
	noebsConfig.WalletDefaultCurrency = "USD"
	noebsConfig.ServiceRole = string(serviceRoleWalletAPI)
	workloadVerifier = roleTestWorkloadVerifier{}

	testGRPCListener = bufconn.Listen(1024 * 1024)
	testGRPCServer = grpc.NewServer(grpc.UnaryInterceptor(requireAuthForWalletMethods))
	walletSrv := newWalletRouteLedger()
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
	unauthorizedResp, err := route.Test(unauthorizedReq, routeTestTimeout)
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
	authorizedResp, err := route.Test(authorizedReq, routeTestTimeout)
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

	resp, err := route.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	_ = resp.Body.Close()
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

	resp, err := route.Test(req, routeTestTimeout)
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
	mismatchUserResp, err := route.Test(mismatchUserReq, routeTestTimeout)
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
	mismatchTenantResp, err := route.Test(tenantOverrideReq, routeTestTimeout)
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
	ownerWalletResp, err := route.Test(ownerWalletReq, routeTestTimeout)
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
	foreignWalletResp, err := route.Test(foreignWalletReq, routeTestTimeout)
	if err != nil {
		t.Fatalf("foreign wallet request failed: %v", err)
	}
	if foreignWalletResp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign wallet status = %d, want %d", foreignWalletResp.StatusCode, http.StatusNotFound)
	}
	_ = foreignWalletResp.Body.Close()

	ownerGetReq := httptest.NewRequest(http.MethodGet, "/wallet/wallets/"+ownerWallet.ID, nil)
	setWalletGatewayIdentity(ownerGetReq, 42)
	ownerGetResp, err := route.Test(ownerGetReq, routeTestTimeout)
	if err != nil {
		t.Fatalf("owner get wallet request failed: %v", err)
	}
	if ownerGetResp.StatusCode != http.StatusOK {
		t.Fatalf("owner get wallet status = %d, want %d", ownerGetResp.StatusCode, http.StatusOK)
	}
	_ = ownerGetResp.Body.Close()

	ownerHistoryReq := httptest.NewRequest(http.MethodGet, "/wallet/wallets/"+ownerWallet.ID+"/transactions", nil)
	setWalletGatewayIdentity(ownerHistoryReq, 42)
	ownerHistoryResp, err := route.Test(ownerHistoryReq, routeTestTimeout)
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
	foreignHistoryResp, err := route.Test(foreignHistoryReq, routeTestTimeout)
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

			resp, err := route.Test(req, routeTestTimeout)
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
	ownerWalletResp, err := route.Test(ownerWalletReq, routeTestTimeout)
	if err != nil {
		t.Fatalf("owner wallet request failed: %v", err)
	}
	if ownerWalletResp.StatusCode != http.StatusOK {
		t.Fatalf("owner wallet status = %d, want %d", ownerWalletResp.StatusCode, http.StatusOK)
	}
	ownerWallet := decodeWalletRouteResponse(t, ownerWalletResp)

	req := httptest.NewRequest(http.MethodGet, "/wallet/wallets/"+ownerWallet.ID+"?tenant_id="+url.QueryEscape(noebsConfig.DefaultTenantID), nil)
	setWalletGatewayIdentity(req, 42)

	resp, err := route.Test(req, routeTestTimeout)
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

	resp, err := route.Test(req, routeTestTimeout)
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
	for _, path := range []string{"/admin/wallet", "/admin/wallet/"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path+"?tenant_id="+url.QueryEscape(noebsConfig.DefaultTenantID), nil)
			setGatewayAdminIdentityHeader(req)
			req.Header.Set(gateway.GatewayTenantIDHeader, noebsConfig.DefaultTenantID)

			resp, err := route.Test(req, routeTestTimeout)
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
		})
	}
}

func TestWalletAdminRouteRequiresGatewayTenantIdentity(t *testing.T) {
	configureWalletRouteTest(t)

	route := GetMainEngine()
	req := httptest.NewRequest(http.MethodGet, "/admin/wallet/", nil)
	setGatewayAdminIdentityHeader(req)
	req.Header.Del(gateway.GatewayTenantIDHeader)

	resp, err := route.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("wallet admin request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wallet admin status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	_ = resp.Body.Close()
}

func TestWalletRoutesRequireGatewayTenantIdentity(t *testing.T) {
	configureWalletRouteTest(t)

	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/wallet/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gateway.GatewayUserIDHeader, "42")

	resp, err := route.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	_ = resp.Body.Close()
}
