package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/adonese/noebs/store"
)

func TestValidateTenantIDRejectsMissingAndDefault(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want error
	}{
		{name: "missing", in: "", want: store.ErrMissingTenantID},
		{name: "blank", in: "   ", want: store.ErrMissingTenantID},
		{name: "default", in: "default", want: store.ErrInvalidTenantID},
		{name: "case insensitive default", in: "Default", want: store.ErrInvalidTenantID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateTenantID(tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRenderConfigFilesRejectsMissingTenantAfterMerge(t *testing.T) {
	err := renderConfigInTempDir(t, `noebs:
  db_path: /tmp/noebs.db
`)
	if !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("error = %v, want %v", err, store.ErrMissingTenantID)
	}
}

func TestRenderConfigFilesRejectsDefaultTenantAfterMerge(t *testing.T) {
	err := renderConfigInTempDir(t, `noebs:
  default_tenant_id: default
  db_path: /tmp/noebs.db
`)
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}

func TestRenderConfigFilesAcceptsExplicitTenantAfterMerge(t *testing.T) {
	err := renderConfigInTempDir(t, `noebs:
  default_tenant_id: tenant_1
  db_path: /tmp/noebs.db
`)
	if err != nil {
		t.Fatalf("renderConfigFiles() error = %v", err)
	}
}

func renderConfigInTempDir(t *testing.T, payload string) error {
	t.Helper()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.yaml"), []byte(payload), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})
	return renderConfigFiles()
}

func TestValidateTenantIDAcceptsExplicitTenant(t *testing.T) {
	got, err := validateTenantID(" tenant_1 ")
	if err != nil {
		t.Fatalf("validateTenantID() error = %v", err)
	}
	if got != "tenant_1" {
		t.Fatalf("tenantID = %q, want tenant_1", got)
	}
}
