package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestLoadConfigMergesServiceConfigRole(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: psp-webhook
`), 0o600); err != nil {
		t.Fatalf("write service config: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	payload, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	var cfg ebs_fields.NoebsConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg.ServiceRole != string(serviceRolePSPWebhook) {
		t.Fatalf("service_role = %q, want %q", cfg.ServiceRole, serviceRolePSPWebhook)
	}
}
