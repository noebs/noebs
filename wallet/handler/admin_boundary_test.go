package handler

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestWalletAdminHTTPSurfaceUsesGRPCBridgeOnly(t *testing.T) {
	forbidden := []string{
		"type AdminHandler",
		"func NewAdminHandler",
		"func RegisterAdminRoutes",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list wallet handler files: %v", err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(data), token) {
				t.Fatalf("%s contains %q; wallet admin HTTP must go through GRPCAdminHandler only", path, token)
			}
		}
	}
}

func TestWalletHTTPSurfaceDoesNotReadTenantFromPublicInputs(t *testing.T) {
	forbidden := []string{
		`c.Query("tenant_id")`,
		"requestedTenantIDFromQuery",
		`c.Get(gateway.GatewayTenantIDHeader)`,
	}
	for _, path := range []string{"user.go", "grpc_user.go", "grpc_admin.go", "psp_webhook.go"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(data), token) {
				t.Fatalf("%s contains %q; wallet user tenant must come only from gateway identity", path, token)
			}
		}
	}
}

func TestResolveTenantIDRejectsMissingAndReservedTenant(t *testing.T) {
	cases := []struct {
		name     string
		tenantID string
		wantErr  error
	}{
		{"missing", "", walletstore.ErrMissingTenantID},
		{"invalid", "default", walletstore.ErrInvalidTenantID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveTenantID(tc.tenantID)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("resolveTenantID() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
