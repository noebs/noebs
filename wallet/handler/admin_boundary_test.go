package handler

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-h/templ"
	walletstore "github.com/adonese/noebs/wallet/store"
)

const walletAdminBoundaryCSRF = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

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

func TestWalletAdminPagesRenderCanonicalTenantPathsAndCSRF(t *testing.T) {
	tests := []struct {
		name       string
		component  templ.Component
		formTokens int
	}{
		{
			name:       "manual transfer",
			component:  ManualTransferFormPage(ManualTransferFormView{TenantID: "tenant-a"}),
			formTokens: 2,
		},
		{
			name:       "fees",
			component:  FeesPage(FeeConfigView{TenantID: "tenant-a"}),
			formTokens: 2,
		},
		{
			name:       "rates",
			component:  RatesPage(RateView{TenantID: "tenant-a"}),
			formTokens: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var rendered bytes.Buffer
			ctx := WithAdminCSRFToken(context.Background(), walletAdminBoundaryCSRF)
			if err := test.component.Render(ctx, &rendered); err != nil {
				t.Fatal(err)
			}
			html := rendered.String()
			if strings.Count(html, `name="_csrf" value="`+walletAdminBoundaryCSRF+`"`) != test.formTokens {
				t.Fatalf("rendered CSRF fields = %d, want %d", strings.Count(html, `name="_csrf" value="`+walletAdminBoundaryCSRF+`"`), test.formTokens)
			}
			for _, path := range []string{
				`href="/backoffice/t/tenant-a/wallet"`,
				`href="/backoffice/t/tenant-a/wallet/wallets"`,
				`href="/backoffice/t/tenant-a/wallet/pending"`,
			} {
				if !strings.Contains(html, path) {
					t.Errorf("rendered page missing %s", path)
				}
			}
			if strings.Contains(html, "/admin/wallet") || strings.Contains(html, `name="tenant_id"`) {
				t.Fatalf("rendered page exposed a retired path or caller-selected tenant")
			}
		})
	}
}

func TestAdminWalletPathDoesNotAcceptAuthorityOutsideCanonicalPath(t *testing.T) {
	if got := adminWalletPath("tenant-a", "/audit"); got != "/backoffice/t/tenant-a/wallet/audit" {
		t.Fatalf("adminWalletPath() = %q", got)
	}
	if got := adminWalletPath("tenant/a", "/audit"); got == "/backoffice/t/tenant/a/wallet/audit" {
		t.Fatalf("adminWalletPath() failed to escape tenant path segment: %q", got)
	}
}
