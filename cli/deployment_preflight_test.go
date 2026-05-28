package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adonese/noebs/store"
)

func TestValidateDeploymentRootAcceptsExplicitMicroserviceInputs(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})

	if err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret); err != nil {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v", err)
	}
}

func TestValidateDeploymentRootRejectsReservedTenant(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{
		defaultTenantID: "default",
	})

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}

func TestValidateDeploymentRootRejectsMissingEBSRuntimeInput(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{
		omitEBSConsumerEndpoint: true,
	})

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if !errors.Is(err, errMissingEBSConfig) {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want %v", err, errMissingEBSConfig)
	}
}

func TestValidateDeploymentRootRejectsMissingDockerServiceConfig(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	if err := os.Remove(filepath.Join(root, "deploy", "docker", "services", "wallet-api.yaml")); err != nil {
		t.Fatalf("remove wallet-api service config: %v", err)
	}

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "wallet-api.yaml") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want missing wallet-api service config rejection", err)
	}
}

func TestValidateDeploymentRootRejectsUnexpectedDockerServiceSecret(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	writePreflightFile(t, root, "deploy/docker/secrets/monolith.secrets.yaml", `noebs:
  default_tenant_id: tenant_1
`)

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "unexpected Docker Compose release service secret file") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want unexpected Docker service secret rejection", err)
	}
}

func TestValidateDeploymentRootAllowsTrackedDockerSecretExamples(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	writePreflightFile(t, root, "deploy/docker/secrets/README.md", "local secret examples\n")
	writePreflightFile(t, root, "deploy/docker/secrets/monolith.secrets.yaml.example", `noebs:
  default_tenant_id: REPLACE_WITH_TENANT
`)

	if err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret); err != nil {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v", err)
	}
}

func TestValidateDeploymentRootRejectsPlaceholders(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{
		keycloakPassword: "REPLACE_WITH_KEYCLOAK_PASSWORD",
	})

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want placeholder rejection", err)
	}
}

func TestValidateKubernetesDeploymentRootAcceptsMountedInputs(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)

	if err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret); err != nil {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v", err)
	}
}

func TestValidateKubernetesDeploymentRootRejectsMissingPlatformSecret(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	if err := os.Remove(filepath.Join(root, "platform", "postgres-password.txt")); err != nil {
		t.Fatalf("remove postgres password: %v", err)
	}

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "Noebs Postgres password") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want Noebs Postgres password rejection", err)
	}
}

func TestValidateKubernetesDeploymentRootRejectsMissingMigrationServiceConfig(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	if err := os.Remove(filepath.Join(root, "services", "wallet-ledger-migrate.yaml")); err != nil {
		t.Fatalf("remove wallet-ledger-migrate service config: %v", err)
	}

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "wallet-ledger-migrate.yaml") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want missing wallet-ledger-migrate service config rejection", err)
	}
}

func TestValidateKubernetesDeploymentRootRejectsUnexpectedServiceConfig(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	writePreflightFile(t, root, "services/monolith.yaml", `noebs:
  service_role: api-gateway
  otel_service_name: api-gateway
`)

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "unexpected Kubernetes release service config file") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want unexpected service config rejection", err)
	}
}

func TestValidateKubernetesDeploymentRootRejectsUnexpectedPlatformFile(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	writePreflightFile(t, root, "platform/temporal-broadcast-address.txt", "temporal:7233\n")

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "unexpected Kubernetes release platform file") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want unexpected platform file rejection", err)
	}
}

func TestValidateKubernetesDeploymentRootRejectsUnexpectedSOPSFile(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	writePreflightFile(t, root, ".sops/old-age-key.txt", "AGE-SECRET-KEY-1OLD\n")

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "unexpected Kubernetes release SOPS file") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want unexpected SOPS file rejection", err)
	}
}

type preflightRootOptions struct {
	defaultTenantID         string
	omitEBSConsumerEndpoint bool
	keycloakPassword        string
}

func writePreflightRoot(t *testing.T, opts preflightRootOptions) string {
	t.Helper()
	root := t.TempDir()
	defaultTenantID := opts.defaultTenantID
	if defaultTenantID == "" {
		defaultTenantID = "tenant_1"
	}
	keycloakPassword := opts.keycloakPassword
	if keycloakPassword == "" {
		keycloakPassword = "keycloak-postgres-password"
	}

	writePreflightFile(t, root, "docker-compose.yml", "services:\n  api-gateway:\n    image: noebs\n")
	writePreflightFile(t, root, ".sops/age-key.txt", "AGE-SECRET-KEY-1LOCAL")
	configMapData := decodeKubernetesNoebsConfigMapData(t)
	writePreflightFile(t, root, "config.docker.yaml", configMapData["config.yaml"])

	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		configKey := serviceName + ".service.yaml"
		payload := configMapData[configKey]
		if payload == "" {
			t.Fatalf("noebs-config missing %s", configKey)
		}
		writePreflightFile(t, root, filepath.Join("deploy", "docker", "services", serviceName+".yaml"), payload)
	}

	for fileName, payload := range kubernetesSecretTestPayloads() {
		payload = strings.ReplaceAll(payload, "tenant_1", defaultTenantID)
		if fileName == "ebs-adapter.secrets.yaml" && opts.omitEBSConsumerEndpoint {
			payload = strings.ReplaceAll(payload, `  consumer_endpoint: "https://consumer.ebs.example"`+"\n", "")
		}
		writePreflightFile(t, root, filepath.Join("deploy", "docker", "secrets", fileName), payload)
	}
	writePreflightFile(t, root, "deploy/docker/postgres/bootstrap.secrets.yaml", `noebs:
  db_url: "postgres://noebs:postgres-password@db:5432/noebs?sslmode=disable"
`)
	writePreflightFile(t, root, "deploy/docker/temporal/postgres-password.txt", "temporal-postgres-password\n")
	writePreflightFile(t, root, "deploy/docker/keycloak/postgres-password.txt", keycloakPassword+"\n")
	writePreflightFile(t, root, "deploy/docker/keycloak/keycloak.conf", `http-enabled=true
http-port=8080
hostname-strict=false
proxy-headers=xforwarded
health-enabled=true
metrics-enabled=true

db=postgres
db-url=jdbc:postgresql://keycloak-postgres:5432/keycloak
db-username=keycloak
db-password=`+keycloakPassword+`

bootstrap-admin-username=admin
bootstrap-admin-password=admin-password
`)
	return root
}

func writePreflightFile(t *testing.T, root, name, payload string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readPlainPreflightSecret(path, ageKeyFile string) ([]byte, error) {
	_ = ageKeyFile
	return os.ReadFile(path)
}
