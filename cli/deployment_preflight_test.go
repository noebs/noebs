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
	root := writeKubernetesPreflightRoot(t, false)

	if err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret); err != nil {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v", err)
	}
}

func TestValidateKubernetesDeploymentRootRejectsMissingPlatformSecret(t *testing.T) {
	root := writeKubernetesPreflightRoot(t, true)

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "Noebs Postgres password") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want Noebs Postgres password rejection", err)
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

	writePreflightFile(t, root, "docker-compose.yml", "services:\n  ebs-adapter:\n    image: noebs\n")
	writePreflightFile(t, root, ".sops/age-key.txt", "AGE-SECRET-KEY-1LOCAL")
	writePreflightFile(t, root, "config.docker.yaml", `noebs:
  service_discovery:
    card-vault: "http://card-vault:8080"
    identity-auth: "http://identity-auth:8080"
    notification-chat: "http://notification-chat:8080"
    admin-reporting: "http://admin-reporting:8080"
  ebs_dynamic_fees:
    p2p_fees: 30
    custom_fees: 85
    special_payment_fees: 2
`)
	writePreflightFile(t, root, "deploy/docker/services/ebs-adapter.yaml", `noebs:
  service_role: ebs-adapter
  otel_service_name: ebs-adapter
  db_driver: pgx
`)
	consumerEndpoint := `  consumer_endpoint: "https://consumer.ebs.example"` + "\n"
	if opts.omitEBSConsumerEndpoint {
		consumerEndpoint = ""
	}
	writePreflightFile(t, root, "deploy/docker/secrets/ebs-adapter.secrets.yaml", `noebs:
  default_tenant_id: `+defaultTenantID+`
  service_databases:
    ebs-adapter: "postgres://noebs:service-password@postgres:5432/ebs_adapter?sslmode=disable"
`+consumerEndpoint+`  merchant_endpoint: "https://merchant.ebs.example"
  ipin_endpoint: "https://ipin.ebs.example"
  consumer_app_id: "consumer-app"
  merchant_app_id: "merchant-app"
`)
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

func writeKubernetesPreflightRoot(t *testing.T, omitPostgresPassword bool) string {
	t.Helper()
	root := t.TempDir()
	keycloakPassword := "keycloak-postgres-password"

	writePreflightFile(t, root, ".sops/age-key.txt", "AGE-SECRET-KEY-1LOCAL")
	writePreflightFile(t, root, "config.yaml", `noebs:
  sops_age_key_file: /preflight/.sops/age-key.txt
  service_discovery:
    card-vault: "http://card-vault:8080"
    identity-auth: "http://identity-auth:8080"
    notification-chat: "http://notification-chat:8080"
    admin-reporting: "http://admin-reporting:8080"
  ebs_dynamic_fees:
    p2p_fees: 30
    custom_fees: 85
    special_payment_fees: 2
`)
	writePreflightFile(t, root, "services/ebs-adapter.yaml", `noebs:
  service_role: ebs-adapter
  otel_service_name: ebs-adapter
  db_driver: pgx
`)
	writePreflightFile(t, root, "secrets/ebs-adapter.secrets.yaml", `noebs:
  default_tenant_id: tenant_1
  service_databases:
    ebs-adapter: "postgres://noebs:service-password@postgres:5432/ebs_adapter?sslmode=disable"
  consumer_endpoint: "https://consumer.ebs.example"
  merchant_endpoint: "https://merchant.ebs.example"
  ipin_endpoint: "https://ipin.ebs.example"
  consumer_app_id: "consumer-app"
  merchant_app_id: "merchant-app"
`)
	if !omitPostgresPassword {
		writePreflightFile(t, root, "platform/postgres-password.txt", "postgres-password\n")
	}
	writePreflightFile(t, root, "platform/temporal-postgres-password.txt", "temporal-postgres-password\n")
	writePreflightFile(t, root, "platform/keycloak-postgres-password.txt", keycloakPassword+"\n")
	writePreflightFile(t, root, "platform/keycloak.conf", `http-enabled=true
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
