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

func configureTestAuth(t *testing.T) string {
	t.Helper()
	originalAuth := auth
	t.Cleanup(func() {
		auth = originalAuth
	})

	auth = gateway.JWTAuth{Key: []byte("test-key")}
	token, err := auth.GenerateJWT(42, "0990000000", "tenant")
	if err != nil {
		t.Fatalf("GenerateJWT() error = %v", err)
	}
	return token
}

func TestManualTransferMethodRequiresAuth(t *testing.T) {
	tests := []struct {
		name       string
		fullMethod string
		want       bool
	}{
		{
			name:       "public request manual transfer",
			fullMethod: walletv1.WalletPublicService_RequestManualTransfer_FullMethodName,
			want:       true,
		},
		{
			name:       "public signal manual transfer decision",
			fullMethod: walletv1.WalletPublicService_SignalManualTransferDecision_FullMethodName,
			want:       true,
		},
		{
			name:       "internal request manual transfer",
			fullMethod: walletv1.WalletInternalService_RequestManualTransfer_FullMethodName,
			want:       true,
		},
		{
			name:       "internal signal manual transfer decision",
			fullMethod: walletv1.WalletInternalService_SignalManualTransferDecision_FullMethodName,
			want:       true,
		},
		{
			name:       "other public method",
			fullMethod: walletv1.WalletPublicService_RequestWithdrawal_FullMethodName,
			want:       false,
		},
		{
			name:       "other internal method",
			fullMethod: walletv1.WalletInternalService_RequestWithdrawal_FullMethodName,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := manualTransferMethodRequiresAuth(tt.fullMethod); got != tt.want {
				t.Fatalf("manualTransferMethodRequiresAuth(%q) = %v, want %v", tt.fullMethod, got, tt.want)
			}
		})
	}
}

func TestManualTransferPathRequiresAuth(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "manual transfer request route", path: "/wallet/manual_transfers", want: true},
		{name: "manual transfer decision route", path: "/wallet/manual_transfers/workflow-id/decision", want: true},
		{name: "different manual transfer route", path: "/wallet/manual_transfers/workflow-id", want: false},
		{name: "withdrawal route", path: "/wallet/withdrawals", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := manualTransferPathRequiresAuth(tt.path); got != tt.want {
				t.Fatalf("manualTransferPathRequiresAuth(%q) = %v, want %v", tt.path, got, tt.want)
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
}

func TestContextHasValidBearerToken(t *testing.T) {
	token := configureTestAuth(t)

	validCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if !contextHasValidBearerToken(validCtx) {
		t.Fatalf("contextHasValidBearerToken(validCtx) = false, want true")
	}

	invalidCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer invalid"))
	if contextHasValidBearerToken(invalidCtx) {
		t.Fatalf("contextHasValidBearerToken(invalidCtx) = true, want false")
	}
}

func TestRequireAuthForManualTransferMethods(t *testing.T) {
	token := configureTestAuth(t)

	tests := []struct {
		name       string
		ctx        context.Context
		fullMethod string
		wantCode   codes.Code
		wantCalled bool
	}{
		{
			name:       "allows unprotected method without token",
			ctx:        context.Background(),
			fullMethod: walletv1.WalletPublicService_RequestWithdrawal_FullMethodName,
			wantCode:   codes.OK,
			wantCalled: true,
		},
		{
			name:       "rejects protected method without token",
			ctx:        context.Background(),
			fullMethod: walletv1.WalletPublicService_RequestManualTransfer_FullMethodName,
			wantCode:   codes.Unauthenticated,
			wantCalled: false,
		},
		{
			name:       "allows protected method with valid token",
			ctx:        metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token)),
			fullMethod: walletv1.WalletPublicService_RequestManualTransfer_FullMethodName,
			wantCode:   codes.OK,
			wantCalled: true,
		},
		{
			name:       "rejects internal protected method without token",
			ctx:        context.Background(),
			fullMethod: walletv1.WalletInternalService_SignalManualTransferDecision_FullMethodName,
			wantCode:   codes.Unauthenticated,
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
			_, err := requireAuthForManualTransferMethods(tt.ctx, struct{}{}, &grpc.UnaryServerInfo{
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

func TestRequireAuthForManualTransferHTTP(t *testing.T) {
	token := configureTestAuth(t)

	tests := []struct {
		name         string
		path         string
		authHeader   string
		wantStatus   int
		wantNextCall bool
	}{
		{
			name:         "allows unprotected path without token",
			path:         "/wallet/withdrawals",
			wantStatus:   http.StatusNoContent,
			wantNextCall: true,
		},
		{
			name:         "rejects protected request path without token",
			path:         "/wallet/manual_transfers",
			wantStatus:   http.StatusUnauthorized,
			wantNextCall: false,
		},
		{
			name:         "rejects protected decision path with invalid token",
			path:         "/wallet/manual_transfers/workflow-id/decision",
			authHeader:   "Bearer invalid",
			wantStatus:   http.StatusUnauthorized,
			wantNextCall: false,
		},
		{
			name:         "allows protected request path with valid token",
			path:         "/wallet/manual_transfers",
			authHeader:   "Bearer " + token,
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
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			requireAuthForManualTransferHTTP(next).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if called != tt.wantNextCall {
				t.Fatalf("next called = %v, want %v", called, tt.wantNextCall)
			}
		})
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "valid bearer token", raw: "Bearer abc123", want: "abc123"},
		{name: "lowercase bearer token", raw: "bearer abc123", want: "abc123"},
		{name: "trimmed bearer token", raw: "  Bearer abc123  ", want: "abc123"},
		{name: "missing bearer prefix", raw: "abc123", want: ""},
		{name: "empty value", raw: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractBearerToken(tt.raw); got != tt.want {
				t.Fatalf("extractBearerToken(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
