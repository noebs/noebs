package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/keycloakadmin"
	"github.com/adonese/noebs/internal/tenantcatalog"
)

func TestRuntimeTenantCatalogInitializesEnrollmentAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant-catalog.yaml")
	if err := os.WriteFile(path, []byte("api_version: noebs.sd/tenants/v1\ntenants:\n  - id: noebs\n    name: Noebs\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := ebs_fields.NoebsConfig{
		DefaultTenantID: "noebs",
		AccountEnrollment: accountenrollment.RuntimeConfig{
			Enabled: true, Issuer: "https://example.test/auth/realms/noebs",
			TenantIDs: []string{"noebs"}, AllowVerifiedAccounts: true,
			KeycloakBaseURL:  "https://keycloak.example.test/auth",
			KeycloakClientID: "noebs-account-enroller", KeycloakClientSecret: "test-only-secret",
		},
	}
	catalog, err := loadRuntimeTenantCatalog(serviceRoleIdentityAuth, cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the same authority construction that previously stopped the
	// identity-auth process when startup supplied an empty catalog.
	if _, err := keycloakadmin.NewEnrollmentAuthority(cfg.AccountEnrollment, catalog, http.DefaultClient); err != nil {
		t.Fatalf("enabled identity-auth enrollment cannot initialize: %v", err)
	}
	cfg.AccountEnrollment.TenantIDs = []string{"unknown"}
	if _, err := keycloakadmin.NewEnrollmentAuthority(cfg.AccountEnrollment, catalog, http.DefaultClient); !errors.Is(err, accountenrollment.ErrInvalidConfig) {
		t.Fatalf("unknown enrollment destination must remain rejected: %v", err)
	}

	cfg.AccountEnrollment.Enabled = false
	if catalog, err := loadRuntimeTenantCatalog(serviceRoleAPIGateway, cfg, path); err != nil {
		t.Fatal(err)
	} else if _, err := catalog.Require("noebs"); err != nil {
		t.Fatalf("gateway lost its tenant catalog: %v", err)
	}
	cfg.DefaultTenantID = "unknown"
	if _, err := loadRuntimeTenantCatalog(serviceRoleAPIGateway, cfg, path); !errors.Is(err, tenantcatalog.ErrUnknownTenant) {
		t.Fatalf("unknown configured tenant must remain rejected: %v", err)
	}
}

func TestRuntimeTenantCatalogRequiresFileOnlyForCatalogConsumers(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for _, tc := range []struct {
		name    string
		role    serviceRole
		enabled bool
		wantErr bool
	}{
		{"gateway", serviceRoleAPIGateway, false, true},
		{"enrollment", serviceRoleIdentityAuth, true, true},
		{"disabled enrollment", serviceRoleIdentityAuth, false, false},
		{"wallet worker", serviceRoleWalletWorker, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ebs_fields.NoebsConfig{DefaultTenantID: "noebs", AccountEnrollment: accountenrollment.RuntimeConfig{Enabled: tc.enabled}}
			_, err := loadRuntimeTenantCatalog(tc.role, cfg, missing)
			if tc.wantErr && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing required catalog must fail startup: %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("role unnecessarily required a catalog: %v", err)
			}
		})
	}
}
