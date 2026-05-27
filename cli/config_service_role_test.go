package main

import (
	"encoding/json"
	"errors"
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

func TestLoadConfigReturnsExplicitSecretDecryptErrorDuringTests(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_role: api-gateway
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "secrets.yaml"), []byte(`noebs:
  jwt_secret: not-sops-encrypted
`), 0o600); err != nil {
		t.Fatalf("write secrets: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	if _, err := loadConfig(); err == nil {
		t.Fatalf("loadConfig() error = nil, want explicit secrets decrypt error")
	}
}

func TestLoadConfigDoesNotReadParentConfigDuringTests(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: parent-tenant
  db_driver: postgres
  service_role: api-gateway
`), 0o600); err != nil {
		t.Fatalf("write parent config: %v", err)
	}
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	if err := os.Chdir(child); err != nil {
		t.Fatalf("chdir child: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	if _, err := loadConfig(); err == nil {
		t.Fatalf("loadConfig() error = nil, want missing local config.test.yaml")
	}
}

func TestLoadConfigDoesNotReadParentSecretsDuringTests(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "secrets.yaml"), []byte(`noebs:
  jwt_secret: not-sops-encrypted
`), 0o600); err != nil {
		t.Fatalf("write parent secrets: %v", err)
	}
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	if err := os.WriteFile(filepath.Join(child, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_role: api-gateway
`), 0o600); err != nil {
		t.Fatalf("write child config: %v", err)
	}
	if err := os.Chdir(child); err != nil {
		t.Fatalf("chdir child: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	if _, err := loadConfig(); err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
}

func TestLoadConfigSelectsServiceDatabaseURL(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_databases:
    identity-auth: postgres://noebs:noebs@postgres:5432/identity_auth?sslmode=disable
    card-vault: postgres://noebs:noebs@postgres:5432/card_vault?sslmode=disable
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: card-vault
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
	if cfg.DatabaseURL != "postgres://noebs:noebs@postgres:5432/card_vault?sslmode=disable" {
		t.Fatalf("db_url = %q", cfg.DatabaseURL)
	}
}

func TestLoadConfigSelectsOwnerDatabaseURLForMigrationRole(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_databases:
    identity-auth: postgres://noebs:noebs@postgres:5432/identity_auth?sslmode=disable
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: identity-auth-migrate
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
	if cfg.DatabaseURL != "postgres://noebs:noebs@postgres:5432/identity_auth?sslmode=disable" {
		t.Fatalf("db_url = %q", cfg.DatabaseURL)
	}
}

func TestLoadConfigSelectsWalletLedgerDatabaseURLForWalletWorker(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_databases:
    wallet-ledger: postgres://noebs:noebs@postgres:5432/wallet_ledger?sslmode=disable
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: wallet-worker
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
	if cfg.DatabaseURL != "postgres://noebs:noebs@postgres:5432/wallet_ledger?sslmode=disable" {
		t.Fatalf("db_url = %q", cfg.DatabaseURL)
	}
}

func TestLoadConfigDoesNotRequireServiceDatabaseURLForAPIGateway(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_databases:
    identity-auth: postgres://noebs:noebs@postgres:5432/identity_auth?sslmode=disable
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: api-gateway
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
	if cfg.DatabaseURL != "" {
		t.Fatalf("api-gateway db_url = %q, want empty", cfg.DatabaseURL)
	}
}

func TestLoadConfigDoesNotRequireServiceDatabaseURLForWalletAPI(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_databases:
    wallet-ledger: postgres://noebs:noebs@postgres:5432/wallet_ledger?sslmode=disable
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: wallet-api
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
	if cfg.DatabaseURL != "" {
		t.Fatalf("wallet-api db_url = %q, want empty", cfg.DatabaseURL)
	}
}

func TestLoadConfigRejectsServiceDatabaseURLForAPIGateway(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_databases:
    api-gateway: postgres://noebs:noebs@postgres:5432/api_gateway?sslmode=disable
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: api-gateway
`), 0o600); err != nil {
		t.Fatalf("write service config: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	_, err = loadConfig()
	if !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("loadConfig() error = %v, want %v", err, errDatabaseNotAllowed)
	}
}

func TestLoadConfigRejectsLegacyDatabasePath(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  db_path: /tmp/noebs.db
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: identity-auth
`), 0o600); err != nil {
		t.Fatalf("write service config: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	_, err = loadConfig()
	if !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("loadConfig() error = %v, want %v", err, errDatabaseNotAllowed)
	}
}

func TestLoadConfigRejectsServiceDatabaseURLForMigrationRoleKey(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_databases:
    identity-auth-migrate: postgres://noebs:noebs@postgres:5432/identity_auth?sslmode=disable
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: identity-auth-migrate
`), 0o600); err != nil {
		t.Fatalf("write service config: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	_, err = loadConfig()
	if !errors.Is(err, errDatabaseOwnerKey) {
		t.Fatalf("loadConfig() error = %v, want %v", err, errDatabaseOwnerKey)
	}
}

func TestLoadConfigRejectsServiceDatabaseURLForWalletWorkerKey(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_databases:
    wallet-worker: postgres://noebs:noebs@postgres:5432/wallet_ledger?sslmode=disable
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: wallet-worker
`), 0o600); err != nil {
		t.Fatalf("write service config: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	_, err = loadConfig()
	if !errors.Is(err, errDatabaseOwnerKey) {
		t.Fatalf("loadConfig() error = %v, want %v", err, errDatabaseOwnerKey)
	}
}

func TestLoadConfigRejectsServiceDatabaseURLForWalletAPI(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.test.yaml"), []byte(`noebs:
  default_tenant_id: test-tenant
  db_driver: postgres
  service_databases:
    wallet-api: postgres://noebs:noebs@postgres:5432/wallet_ledger?sslmode=disable
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "service.yaml"), []byte(`noebs:
  service_role: wallet-api
`), 0o600); err != nil {
		t.Fatalf("write service config: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	_, err = loadConfig()
	if !errors.Is(err, errDatabaseNotAllowed) {
		t.Fatalf("loadConfig() error = %v, want %v", err, errDatabaseNotAllowed)
	}
}
