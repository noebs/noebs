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
			name:       "admin method uses operator identity",
			fullMethod: walletv1.WalletAdminService_RenderWalletAdmin_FullMethodName,
			want:       walletAuthAdmin,
		},
		{
			name:       "unknown method is denied",
			fullMethod: "/other.Service/Method",
			want:       walletAuthDeny,
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
		name   string
		method string
		path   string
		want   walletAuthRequirement
	}{
		{name: "ensure wallet", method: http.MethodPost, path: "/wallet", want: walletAuthUserIdentity},
		{name: "get wallet", method: http.MethodGet, path: "/wallet/550e8400-e29b-41d4-a716-446655440000", want: walletAuthUserIdentity},
		{name: "payment methods", method: http.MethodGet, path: "/wallet/methods", want: walletAuthUserIdentity},
		{name: "wallet transactions", method: http.MethodGet, path: "/wallet/550e8400-e29b-41d4-a716-446655440000/transactions", want: walletAuthUserIdentity},
		{name: "deposit", method: http.MethodPost, path: "/wallet/deposits", want: walletAuthUserIdentity},
		{name: "funding sources", method: http.MethodGet, path: "/wallet/550e8400-e29b-41d4-a716-446655440000/funding_sources", want: walletAuthUserIdentity},
		{name: "create destination", method: http.MethodPost, path: "/wallet/destinations", want: walletAuthUserIdentity},
		{name: "list destinations", method: http.MethodGet, path: "/wallet/550e8400-e29b-41d4-a716-446655440000/destinations", want: walletAuthUserIdentity},
		{name: "deactivate destination", method: http.MethodPost, path: "/wallet/destinations/7/deactivate", want: walletAuthUserIdentity},
		{name: "removed manual transfer request", method: http.MethodPost, path: "/wallet/manual_transfers", want: walletAuthDeny},
		{name: "removed manual transfer decision", method: http.MethodPost, path: "/wallet/manual_transfers/workflow-id/decision", want: walletAuthDeny},
		{name: "removed withdrawal approval", method: http.MethodPost, path: "/wallet/withdrawals/workflow-id/approval", want: walletAuthDeny},
		{name: "removed withdrawal verification", method: http.MethodPost, path: "/wallet/withdrawals/workflow-id/verification", want: walletAuthDeny},
		{name: "removed withdrawal request", method: http.MethodPost, path: "/wallet/withdrawals", want: walletAuthDeny},
		{name: "wrong method", method: http.MethodDelete, path: "/wallet", want: walletAuthDeny},
		{name: "other route", method: http.MethodGet, path: "/other", want: walletAuthDeny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := walletPathAuthRequirement(tt.method, tt.path); got != tt.want {
				t.Fatalf("walletPathAuthRequirement(%q, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestWalletGRPCServiceDescriptorsAreCoveredByTheAuthCatalog(t *testing.T) {
	services := []struct {
		description grpc.ServiceDesc
		want        walletAuthRequirement
	}{
		{description: walletv1.WalletPublicService_ServiceDesc, want: walletAuthUserIdentity},
		{description: walletv1.WalletAdminService_ServiceDesc, want: walletAuthAdmin},
	}
	for _, service := range services {
		if len(service.description.Streams) != 0 {
			t.Fatalf("%s adds streaming RPCs without a stream authentication interceptor", service.description.ServiceName)
		}
		for _, method := range service.description.Methods {
			fullMethod := "/" + service.description.ServiceName + "/" + method.MethodName
			if got := walletMethodAuthRequirement(fullMethod); got != service.want {
				t.Errorf("%s auth = %v, want %v", fullMethod, got, service.want)
			}
		}
	}
}

func TestGRPCGatewayIncomingHeaderMatcher(t *testing.T) {
	key, ok := grpcGatewayIncomingHeaderMatcher(gateway.GatewayRolesHeader)
	if !ok {
		t.Fatalf("grpcGatewayIncomingHeaderMatcher(%s) ok = false, want true", gateway.GatewayRolesHeader)
	}
	if key != "x-noebs-roles" {
		t.Fatalf("grpcGatewayIncomingHeaderMatcher(%s) = %q, want %q", gateway.GatewayRolesHeader, key, "x-noebs-roles")
	}
	key, ok = grpcGatewayIncomingHeaderMatcher(gateway.GatewayTenantIDHeader)
	if !ok {
		t.Fatalf("grpcGatewayIncomingHeaderMatcher(%s) ok = false, want true", gateway.GatewayTenantIDHeader)
	}
	if key != "x-noebs-tenant-id" {
		t.Fatalf("grpcGatewayIncomingHeaderMatcher(%s) = %q, want %q", gateway.GatewayTenantIDHeader, key, "x-noebs-tenant-id")
	}

	blockedHeaders := []string{
		"Authorization",
		"X-Admin-Key",
		"X-Admin-Role",
		"X-Admin-Permissions",
	}
	for _, header := range blockedHeaders {
		t.Run(header, func(t *testing.T) {
			if got, ok := grpcGatewayIncomingHeaderMatcher(header); ok || got != "" {
				t.Fatalf("grpcGatewayIncomingHeaderMatcher(%s) = %q, %t; want empty, false", header, got, ok)
			}
		})
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

func TestContextHasGatewayAdminIdentity(t *testing.T) {
	if !contextHasGatewayAdminIdentity(gatewayAdminIdentityContext()) {
		t.Fatalf("contextHasGatewayAdminIdentity(validCtx) = false, want true")
	}
	invalidCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-noebs-admin-identity", "wrong"))
	if contextHasGatewayAdminIdentity(invalidCtx) {
		t.Fatalf("contextHasGatewayAdminIdentity(invalidCtx) = true, want false")
	}
}

func TestRequireAuthForWalletMethods(t *testing.T) {
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
			name:       "rejects admin method without operator identity",
			ctx:        gatewayUserIdentityContext(42, "tenant", "0990000000"),
			fullMethod: walletv1.WalletAdminService_RenderWalletAdmin_FullMethodName,
			wantCode:   codes.PermissionDenied,
			wantCalled: false,
		},
		{
			name:       "allows admin method with operator identity",
			ctx:        gatewayAdminIdentityContext(),
			fullMethod: walletv1.WalletAdminService_RenderWalletAdmin_FullMethodName,
			wantCode:   codes.OK,
			wantCalled: true,
		},
		{
			name:       "denies unknown method even with admin auth",
			ctx:        gatewayAdminIdentityContext(),
			fullMethod: "/other.Service/Method",
			wantCode:   codes.PermissionDenied,
			wantCalled: false,
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
	tests := []struct {
		name         string
		method       string
		path         string
		userIdentity bool
		wantStatus   int
		wantNextCall bool
	}{
		{
			name:         "rejects user path without gateway identity",
			method:       http.MethodPost,
			path:         "/wallet/deposits",
			wantStatus:   http.StatusUnauthorized,
			wantNextCall: false,
		},
		{
			name:         "allows user path with gateway identity",
			method:       http.MethodPost,
			path:         "/wallet/deposits",
			userIdentity: true,
			wantStatus:   http.StatusNoContent,
			wantNextCall: true,
		},
		{
			name:         "removed admin path is outside catalog",
			method:       http.MethodPost,
			path:         "/wallet/manual_transfers",
			wantStatus:   http.StatusNotFound,
			wantNextCall: false,
		},
		{
			name:         "removed approval path is outside catalog",
			method:       http.MethodPost,
			path:         "/wallet/withdrawals/workflow-id/approval",
			userIdentity: true,
			wantStatus:   http.StatusNotFound,
			wantNextCall: false,
		},
		{
			name:         "unknown path remains outside catalog",
			method:       http.MethodPost,
			path:         "/other",
			userIdentity: true,
			wantStatus:   http.StatusNotFound,
			wantNextCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})

			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.userIdentity {
				setGatewayUserIdentityHeaders(req, 42, "tenant", "0990000000")
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
	validReq := httptest.NewRequest(http.MethodPost, "/wallet/deposits", nil)
	setGatewayUserIdentityHeaders(validReq, 42, "tenant", "0990000000")
	if !requestHasGatewayUserIdentity(validReq) {
		t.Fatalf("requestHasGatewayUserIdentity(validReq) = false, want true")
	}

	invalidReq := httptest.NewRequest(http.MethodPost, "/wallet/deposits", nil)
	invalidReq.Header.Set(gateway.GatewayUserIDHeader, "42")
	if requestHasGatewayUserIdentity(invalidReq) {
		t.Fatalf("requestHasGatewayUserIdentity(invalidReq) = true, want false")
	}
}
