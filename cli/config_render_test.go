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
		{name: "blank", in: "   ", want: store.ErrInvalidTenantID},
		{name: "default", in: "default", want: store.ErrInvalidTenantID},
		{name: "case insensitive default", in: "Default", want: store.ErrInvalidTenantID},
		{name: "surrounding whitespace", in: " tenant-cutover ", want: store.ErrInvalidTenantID},
		{name: "underscore", in: "tenant_1", want: store.ErrInvalidTenantID},
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
  db_driver: pgx
`)
	if !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("error = %v, want %v", err, store.ErrMissingTenantID)
	}
}

func TestRenderConfigFilesRejectsDefaultTenantAfterMerge(t *testing.T) {
	err := renderConfigInTempDir(t, `noebs:
  default_tenant_id: default
  db_driver: pgx
`)
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}

func TestRenderConfigFilesRejectsBlankTenantOverrideAfterMerge(t *testing.T) {
	renderConfigTempDirWithService(t, `noebs:
  default_tenant_id: tenant-1
  db_driver: pgx
`, `noebs:
  service_role: api-gateway
  service_databases:
    api-gateway: postgres://noebs:noebs@postgres:5432/gateway_auth?sslmode=disable
  default_tenant_id: ""
`)
	if err := renderConfigFiles(); !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("renderConfigFiles() error = %v, want %v", err, store.ErrMissingTenantID)
	}
}

func TestRenderConfigFilesAcceptsExplicitTenantAfterMerge(t *testing.T) {
	err := renderConfigInTempDir(t, `noebs:
  default_tenant_id: tenant-1
  db_driver: pgx
`)
	if err != nil {
		t.Fatalf("renderConfigFiles() error = %v", err)
	}
}

func TestRenderConfigFilesRejectsMissingServiceConfig(t *testing.T) {
	renderConfigTempDirWithService(t, `noebs:
  default_tenant_id: tenant-1
  db_driver: pgx
`, "")
	if err := renderConfigFiles(); err == nil {
		t.Fatalf("renderConfigFiles() error = nil, want missing service config error")
	}
}

func TestRenderConfigFilesRejectsInvalidServiceRole(t *testing.T) {
	renderConfigTempDirWithService(t, `noebs:
  default_tenant_id: tenant-1
  db_driver: pgx
`, `noebs:
  service_role: no-such-service
`)
	if err := renderConfigFiles(); !errors.Is(err, errInvalidServiceRole) {
		t.Fatalf("renderConfigFiles() error = %v, want %v", err, errInvalidServiceRole)
	}
}

func TestRenderConfigFilesValidatesRoleDatabaseConfig(t *testing.T) {
	renderConfigTempDirWithService(t, `noebs:
  default_tenant_id: tenant-1
  db_driver: pgx
`, `noebs:
  service_role: identity-auth
`)
	if err := renderConfigFiles(); !errors.Is(err, errMissingDatabaseURL) {
		t.Fatalf("renderConfigFiles() error = %v, want %v", err, errMissingDatabaseURL)
	}
}

func TestRenderConfigFilesRejectsLegacyDatabasePath(t *testing.T) {
	err := renderConfigInTempDir(t, `noebs:
  default_tenant_id: tenant-1
  db_driver: pgx
  db_path: /tmp/noebs.db
`)
	if !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("renderConfigFiles() error = %v, want %v", err, errDatabaseNotAllowed)
	}
}

func TestRenderDatabasePasswordFileDoesNotRunRuntimeValidation(t *testing.T) {
	tmp := renderConfigTempDir(t, `noebs:
  render_db_password_file: password
  db_url: postgres://noebs:postgres-secret@db:5432/noebs?sslmode=disable
  db_path: /tmp/legacy.db
`)
	if err := renderDatabasePasswordFile(); err != nil {
		t.Fatalf("renderDatabasePasswordFile() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(tmp, "password"))
	if err != nil {
		t.Fatalf("read rendered password: %v", err)
	}
	if string(got) != "postgres-secret" {
		t.Fatalf("rendered password = %q, want postgres-secret", got)
	}
}

func TestRenderConfigFilesDoesNotRenderLegacyLitestreamArtifacts(t *testing.T) {
	tmp := renderConfigTempDir(t, `noebs:
  default_tenant_id: tenant-1
  db_driver: pgx
litestream:
  dbs:
    - path: /tmp/noebs.db
      replicas:
        - type: s3
          bucket: litestream-dbs
`)
	if err := renderConfigFiles(); err != nil {
		t.Fatalf("renderConfigFiles() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".db_path")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".db_path stat error = %v, want %v", err, os.ErrNotExist)
	}
	if _, err := os.Stat(filepath.Join(tmp, "litestream.yml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("litestream.yml stat error = %v, want %v", err, os.ErrNotExist)
	}
}

func TestMergeConfigDoesNotIgnoreExplicitEmptyValues(t *testing.T) {
	base := map[string]interface{}{
		"noebs": map[string]interface{}{
			"data_key": "existing-key",
			"cors":     []interface{}{"*"},
		},
	}
	override := map[string]interface{}{
		"noebs": map[string]interface{}{
			"data_key": "",
			"cors":     []interface{}{},
		},
	}

	merged := mergeConfig(base, override).(map[string]interface{})
	noebs := merged["noebs"].(map[string]interface{})
	if got, ok := noebs["data_key"].(string); !ok || got != "" {
		t.Fatalf("data_key = %#v, want explicit empty string", noebs["data_key"])
	}
	cors, ok := noebs["cors"].([]interface{})
	if !ok {
		t.Fatalf("cors = %#v, want []interface{}", noebs["cors"])
	}
	if len(cors) != 0 {
		t.Fatalf("cors = %#v, want explicit empty list", cors)
	}
}

func TestDecryptSopsFileRequiresExplicitAgeKeyFile(t *testing.T) {
	t.Setenv("SOPS_AGE_KEY_FILE", "/ambient/key.txt")

	_, err := decryptSopsFile("secrets.yaml", "")
	if !errors.Is(err, errMissingSopsAgeKeyFile) {
		t.Fatalf("decryptSopsFile() error = %v, want %v", err, errMissingSopsAgeKeyFile)
	}
}

func renderConfigInTempDir(t *testing.T, payload string) error {
	t.Helper()
	renderConfigTempDir(t, payload)
	return renderConfigFiles()
}

func renderConfigTempDir(t *testing.T, payload string) string {
	t.Helper()
	return renderConfigTempDirWithService(t, payload, `noebs:
  service_role: api-gateway
  service_databases:
    api-gateway: postgres://noebs:noebs@postgres:5432/gateway_auth?sslmode=disable
`)
}

func renderConfigTempDirWithService(t *testing.T, payload, servicePayload string) string {
	t.Helper()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.yaml"), []byte(payload), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if servicePayload != "" {
		if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(servicePayload), 0o600); err != nil {
			t.Fatalf("write service config: %v", err)
		}
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})
	return tmp
}

func TestValidateTenantIDAcceptsExplicitTenant(t *testing.T) {
	got, err := validateTenantID("tenant-cutover")
	if err != nil {
		t.Fatalf("validateTenantID() error = %v", err)
	}
	if got != "tenant-cutover" {
		t.Fatalf("tenantID = %q, want tenant-cutover", got)
	}
}
