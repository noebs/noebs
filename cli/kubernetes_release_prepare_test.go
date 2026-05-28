package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adonese/noebs/store"
)

func TestPrepareKubernetesReleaseTransformsExplicitInputs(t *testing.T) {
	legacyRoot := writeLegacyReleaseRoot(t)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "tenant_1")
	outputRoot := filepath.Join(t.TempDir(), "kubernetes-release")

	err := prepareKubernetesRelease("..", legacyRoot, inputsPath, outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err != nil {
		t.Fatalf("prepareKubernetesRelease() error = %v", err)
	}

	requirePreparedFile(t, outputRoot, "config.yaml")
	requirePreparedFile(t, outputRoot, "services/api-gateway.yaml")
	requirePreparedFile(t, outputRoot, "services/wallet-ledger-migrate.yaml")
	requirePreparedFile(t, outputRoot, "platform/keycloak.conf")
	if got := strings.TrimSpace(readPreparedFile(t, outputRoot, "platform/postgres-password.txt")); got != "legacy-pass" {
		t.Fatalf("postgres password = %q, want legacy-pass", got)
	}
	ebsSecret := readYAMLMapFileMust(t, filepath.Join(outputRoot, "secrets", "ebs-adapter.secrets.yaml"))
	ebsNoebs := getMap(ebsSecret, "noebs")
	if got := firstString(ebsNoebs, "consumer_endpoint"); got != "https://consumer.qa.example" {
		t.Fatalf("consumer_endpoint = %q, want QA endpoint selected from explicit legacy selector", got)
	}
	if got := firstString(ebsNoebs, "merchant_endpoint"); got != "https://merchant.prod.example" {
		t.Fatalf("merchant_endpoint = %q, want prod endpoint selected from explicit legacy selector", got)
	}
	if got := firstString(ebsNoebs, "consumer_app_id"); got != "consumer-app" {
		t.Fatalf("consumer_app_id = %q, want explicit input", got)
	}
	walletWorkerSecret := readYAMLMapFileMust(t, filepath.Join(outputRoot, "secrets", "wallet-worker.secrets.yaml"))
	walletWorkerNoebs := getMap(walletWorkerSecret, "noebs")
	serviceDatabases := getMap(walletWorkerNoebs, "service_databases")
	if _, ok := serviceDatabases["wallet-ledger"]; !ok {
		t.Fatalf("wallet-worker service_databases = %#v, want wallet-ledger owner entry", serviceDatabases)
	}
}

func TestPrepareKubernetesReleaseRejectsReservedTenant(t *testing.T) {
	legacyRoot := writeLegacyReleaseRoot(t)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "default")
	outputRoot := filepath.Join(t.TempDir(), "kubernetes-release")

	err := prepareKubernetesRelease("..", legacyRoot, inputsPath, outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), store.ErrInvalidTenantID.Error()) {
		t.Fatalf("prepareKubernetesRelease() error = %v, want invalid tenant rejection", err)
	}
}

func TestPrepareKubernetesReleaseRejectsMissingExplicitInput(t *testing.T) {
	legacyRoot := writeLegacyReleaseRoot(t)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "tenant_1")
	payload := readPreparedFile(t, filepath.Dir(inputsPath), filepath.Base(inputsPath))
	payload = strings.ReplaceAll(payload, "    ipin_endpoint: \"https://ipin.example\"\n", "")
	writePreflightFile(t, filepath.Dir(inputsPath), filepath.Base(inputsPath), payload)
	outputRoot := filepath.Join(t.TempDir(), "kubernetes-release")

	err := prepareKubernetesRelease("..", legacyRoot, inputsPath, outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "noebs.ebs.ipin_endpoint") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want missing explicit ipin endpoint rejection", err)
	}
}

func TestPrepareKubernetesReleaseRejectsMissingLegacySelector(t *testing.T) {
	legacyRoot := writeLegacyReleaseRoot(t)
	secretPath := filepath.Join(legacyRoot, "secrets.yaml")
	payload := readPreparedFile(t, legacyRoot, "secrets.yaml")
	payload = strings.ReplaceAll(payload, "  is_consumer_prod: false\n", "")
	writePreflightFile(t, legacyRoot, "secrets.yaml", payload)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "tenant_1")
	outputRoot := filepath.Join(t.TempDir(), "kubernetes-release")

	err := prepareKubernetesRelease("..", legacyRoot, inputsPath, outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "legacy noebs.is_consumer_prod") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want missing legacy selector rejection", err)
	}
	if _, statErr := os.Stat(secretPath); statErr != nil {
		t.Fatalf("legacy secret file should remain in place: %v", statErr)
	}
}

func TestPrepareKubernetesReleaseRejectsNonEmptyOutputRoot(t *testing.T) {
	legacyRoot := writeLegacyReleaseRoot(t)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "tenant_1")
	outputRoot := t.TempDir()
	writePreflightFile(t, outputRoot, "stale", "do not overwrite")

	err := prepareKubernetesRelease("..", legacyRoot, inputsPath, outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "output root must be empty") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want non-empty output rejection", err)
	}
}

func writeLegacyReleaseRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writePreflightFile(t, root, ".sops/age-key.txt", "# public key: age1testrecipient\nAGE-SECRET-KEY-1LOCAL\n")
	writePreflightFile(t, root, "config.docker.yaml", "noebs:\n  port: \":8080\"\n")
	writePreflightFile(t, root, "secrets.yaml", `noebs:
  db_url: "postgres://legacy-user:legacy-pass@legacy-db:5432/noebs?sslmode=disable"
  jwt_secret: jwt-secret
  sms_key: sms-key
  sms_sender: noebs
  sms_gateway: "https://sms.example"
  is_consumer_prod: false
  consumer_qa: "https://consumer.qa.example"
  consumer_prod: "https://consumer.prod.example"
  is_merchant_prod: true
  merchant_qa: "https://merchant.qa.example"
  merchant_prod: "https://merchant.prod.example"
`)
	return root
}

func writeKubernetesReleaseInputsFile(t *testing.T, root, tenantID string) string {
	t.Helper()
	writePreflightFile(t, root, "kubernetes-release.inputs.yaml", `noebs:
  default_tenant_id: `+tenantID+`
  admin_key: admin-key
  admin_user: admin
  admin_password: admin-password
  sms_message: "code"
  google_client_id: google-client-id
  google_client_secret: google-client-secret
  google_redirect_url: "https://api.noebs.sd/oauth/callback"
  card_vault_data_key: card-vault-data-key
  temporal_postgres_password: temporal-postgres-password
  keycloak_postgres_password: keycloak-postgres-password
  keycloak_bootstrap_admin_username: keycloak-admin
  keycloak_bootstrap_admin_password: keycloak-admin-password
  ebs:
    ipin_endpoint: "https://ipin.example"
    consumer_app_id: consumer-app
    merchant_app_id: merchant-app
    ipin_username: ipin-user
    ipin_password: ipin-password
    pub_key: consumer-public-key
    ipin_key: ipin-public-key
    pan: "1234567890123456"
    pin: "1234"
    ipin: "123456"
    exp_date: "0129"
  psp:
    `+tenantID+`:
      test-provider:
        api_key: psp-api-key
        api_secret: psp-api-secret
        webhook_secret: psp-webhook-secret
        webhook_public_key: psp-webhook-public-key
`)
	return filepath.Join(root, "kubernetes-release.inputs.yaml")
}

func plainKubernetesSecretEncrypt(_ string, payload []byte, _ string) ([]byte, error) {
	return payload, nil
}

func readPreparedFile(t *testing.T, root, name string) string {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(root, name), err)
	}
	return string(payload)
}

func requirePreparedFile(t *testing.T, root, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatalf("stat %s: %v", filepath.Join(root, name), err)
	}
}

func readYAMLMapFileMust(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	result, err := readYAMLMapFile(path)
	if err != nil {
		t.Fatalf("readYAMLMapFile(%s): %v", path, err)
	}
	return result
}
