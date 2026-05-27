package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func configureAdminAuth(t *testing.T) string {
	t.Helper()
	originalCfg := noebsConfig
	t.Cleanup(func() {
		noebsConfig = originalCfg
	})
	noebsConfig.AdminKey = "test-admin-key"
	noebsConfig.AdminUser = ""
	noebsConfig.AdminPassword = ""
	return noebsConfig.AdminKey
}

func TestWalletMethodAuthRequirement(t *testing.T) {
	tests := []struct {
		name       string
		fullMethod string
		want       walletAuthRequirement
	}{
		{
			name:       "public user method uses gateway identity",
			fullMethod: walletv1.WalletPublicService_RequestWithdrawal_FullMethodName,
			want:       walletAuthUserIdentity,
		},
		{
			name:       "public wallet query method uses gateway identity",
			fullMethod: walletv1.WalletPublicService_ListWalletTransactionsPublic_FullMethodName,
			want:       walletAuthUserIdentity,
		},
		{
			name:       "public admin method uses admin auth",
			fullMethod: walletv1.WalletPublicService_SignalManualTransferDecision_FullMethodName,
			want:       walletAuthAdmin,
		},
		{
			name:       "internal method uses admin auth",
			fullMethod: walletv1.WalletInternalService_RequestWithdrawal_FullMethodName,
			want:       walletAuthAdmin,
		},
		{
			name:       "unknown method has no auth requirement",
			fullMethod: "/other.Service/Method",
			want:       walletAuthNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := walletMethodAuthRequirement(tt.fullMethod); got != tt.want {
				t.Fatalf("walletMethodAuthRequirement(%q) = %v, want %v", tt.fullMethod, got, tt.want)
			}
		})
	}
}

func TestWalletPathAuthRequirement(t *testing.T) {
	tests := []struct {
		name string
		path string
		want walletAuthRequirement
	}{
		{name: "manual transfer request route", path: "/wallet/manual_transfers", want: walletAuthAdmin},
		{name: "manual transfer decision route", path: "/wallet/manual_transfers/workflow-id/decision", want: walletAuthAdmin},
		{name: "withdrawal approval route", path: "/wallet/withdrawals/workflow-id/approval", want: walletAuthAdmin},
		{name: "user withdrawal route", path: "/wallet/withdrawals", want: walletAuthUserIdentity},
		{name: "other route", path: "/other", want: walletAuthNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := walletPathAuthRequirement(tt.path); got != tt.want {
				t.Fatalf("walletPathAuthRequirement(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestGRPCGatewayIncomingHeaderMatcher(t *testing.T) {
	key, ok := grpcGatewayIncomingHeaderMatcher("X-Admin-Key")
	if !ok {
		t.Fatalf("grpcGatewayIncomingHeaderMatcher(X-Admin-Key) ok = false, want true")
	}
	if key != "x-admin-key" {
		t.Fatalf("grpcGatewayIncomingHeaderMatcher(X-Admin-Key) = %q, want %q", key, "x-admin-key")
	}
	key, ok = grpcGatewayIncomingHeaderMatcher(gateway.GatewayTenantIDHeader)
	if !ok {
		t.Fatalf("grpcGatewayIncomingHeaderMatcher(%s) ok = false, want true", gateway.GatewayTenantIDHeader)
	}
	if key != "x-noebs-tenant-id" {
		t.Fatalf("grpcGatewayIncomingHeaderMatcher(%s) = %q, want %q", gateway.GatewayTenantIDHeader, key, "x-noebs-tenant-id")
	}
}

func TestContextHasGatewayUserIdentity(t *testing.T) {
	validCtx := gatewayUserIdentityContext(42, "tenant", "0990000000")
	if !contextHasGatewayUserIdentity(validCtx) {
		t.Fatalf("contextHasGatewayUserIdentity(validCtx) = false, want true")
	}

	invalidCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-noebs-user-id", "42"))
	if contextHasGatewayUserIdentity(invalidCtx) {
		t.Fatalf("contextHasGatewayUserIdentity(invalidCtx) = true, want false")
	}
}

func TestRequireAuthForWalletMethods(t *testing.T) {
	adminKey := configureAdminAuth(t)

	tests := []struct {
		name       string
		ctx        context.Context
		fullMethod string
		wantCode   codes.Code
		wantCalled bool
	}{
		{
			name:       "rejects public user method without gateway identity",
			ctx:        context.Background(),
			fullMethod: walletv1.WalletPublicService_RequestWithdrawal_FullMethodName,
			wantCode:   codes.Unauthenticated,
			wantCalled: false,
		},
		{
			name:       "allows public user method with gateway identity",
			ctx:        gatewayUserIdentityContext(42, "tenant", "0990000000"),
			fullMethod: walletv1.WalletPublicService_RequestWithdrawal_FullMethodName,
			wantCode:   codes.OK,
			wantCalled: true,
		},
		{
			name:       "rejects public admin method without admin auth",
			ctx:        gatewayUserIdentityContext(42, "tenant", "0990000000"),
			fullMethod: walletv1.WalletPublicService_RequestManualTransfer_FullMethodName,
			wantCode:   codes.PermissionDenied,
			wantCalled: false,
		},
		{
			name:       "allows public admin method with admin key",
			ctx:        metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-admin-key", adminKey)),
			fullMethod: walletv1.WalletPublicService_RequestManualTransfer_FullMethodName,
			wantCode:   codes.OK,
			wantCalled: true,
		},
		{
			name:       "rejects internal method without admin auth",
			ctx:        gatewayUserIdentityContext(42, "tenant", "0990000000"),
			fullMethod: walletv1.WalletInternalService_RequestWithdrawal_FullMethodName,
			wantCode:   codes.PermissionDenied,
			wantCalled: false,
		},
		{
			name:       "allows internal method with admin auth",
			ctx:        metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-admin-key", adminKey)),
			fullMethod: walletv1.WalletInternalService_RequestWithdrawal_FullMethodName,
			wantCode:   codes.OK,
			wantCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := func(ctx context.Context, req any) (any, error) {
				called = true
				return "ok", nil
			}
			_, err := requireAuthForWalletMethods(tt.ctx, struct{}{}, &grpc.UnaryServerInfo{
				FullMethod: tt.fullMethod,
			}, handler)

			if status.Code(err) != tt.wantCode {
				t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), tt.wantCode)
			}
			if called != tt.wantCalled {
				t.Fatalf("handler called = %v, want %v", called, tt.wantCalled)
			}
		})
	}
}

func TestRequireAuthForWalletHTTP(t *testing.T) {
	adminKey := configureAdminAuth(t)

	tests := []struct {
		name         string
		path         string
		userIdentity bool
		adminKey     string
		wantStatus   int
		wantNextCall bool
	}{
		{
			name:         "rejects user path without gateway identity",
			path:         "/wallet/withdrawals",
			wantStatus:   http.StatusUnauthorized,
			wantNextCall: false,
		},
		{
			name:         "allows user path with gateway identity",
			path:         "/wallet/withdrawals",
			userIdentity: true,
			wantStatus:   http.StatusNoContent,
			wantNextCall: true,
		},
		{
			name:         "rejects admin path without admin auth",
			path:         "/wallet/manual_transfers",
			wantStatus:   http.StatusUnauthorized,
			wantNextCall: false,
		},
		{
			name:         "allows admin path with admin key",
			path:         "/wallet/manual_transfers",
			adminKey:     adminKey,
			wantStatus:   http.StatusNoContent,
			wantNextCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			if tt.userIdentity {
				setGatewayUserIdentityHeaders(req, 42, "tenant", "0990000000")
			}
			if tt.adminKey != "" {
				req.Header.Set("X-Admin-Key", tt.adminKey)
			}
			rec := httptest.NewRecorder()

			requireAuthForWalletHTTP(next).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if called != tt.wantNextCall {
				t.Fatalf("next called = %v, want %v", called, tt.wantNextCall)
			}
		})
	}
}

func TestRequestHasGatewayUserIdentity(t *testing.T) {
	validReq := httptest.NewRequest(http.MethodPost, "/wallet/withdrawals", nil)
	setGatewayUserIdentityHeaders(validReq, 42, "tenant", "0990000000")
	if !requestHasGatewayUserIdentity(validReq) {
		t.Fatalf("requestHasGatewayUserIdentity(validReq) = false, want true")
	}

	invalidReq := httptest.NewRequest(http.MethodPost, "/wallet/withdrawals", nil)
	invalidReq.Header.Set(gateway.GatewayUserIDHeader, "42")
	if requestHasGatewayUserIdentity(invalidReq) {
		t.Fatalf("requestHasGatewayUserIdentity(invalidReq) = true, want false")
	}
}
