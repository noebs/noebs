package main

import (
	"errors"
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
	if got := firstString(ebsNoebs, "consumer_endpoint"); got != "https://consumer.input.example" {
		t.Fatalf("consumer_endpoint = %q, want explicit input", got)
	}
	if got := firstString(ebsNoebs, "merchant_endpoint"); got != "https://merchant.input.example" {
		t.Fatalf("merchant_endpoint = %q, want explicit input", got)
	}
	if got := firstString(ebsNoebs, "consumer_app_id"); got != "consumer-app" {
		t.Fatalf("consumer_app_id = %q, want explicit input", got)
	}
	identitySecret := readYAMLMapFileMust(t, filepath.Join(outputRoot, "secrets", "identity-auth.secrets.yaml"))
	identityNoebs := getMap(identitySecret, "noebs")
	if got := firstString(identityNoebs, "google_client_id"); got != "legacy-google-client-id" {
		t.Fatalf("google_client_id = %q, want legacy secret value", got)
	}
	if got := firstString(identityNoebs, "google_client_secret"); got != "legacy-google-client-secret" {
		t.Fatalf("google_client_secret = %q, want legacy secret value", got)
	}
	if got := firstString(identityNoebs, "google_redirect_url"); got != "https://api.noebs.sd/oauth/callback" {
		t.Fatalf("google_redirect_url = %q, want explicit input", got)
	}
	if got := firstString(identityNoebs, "sms_key"); got != "input-sms-key" {
		t.Fatalf("sms_key = %q, want explicit input", got)
	}
	if got := firstString(identityNoebs, "sms_sender"); got != "input-sms-sender" {
		t.Fatalf("sms_sender = %q, want explicit input", got)
	}
	if got := firstString(identityNoebs, "sms_gateway"); got != "https://input.sms.example" {
		t.Fatalf("sms_gateway = %q, want explicit input", got)
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
	assertPathMissing(t, outputRoot)
}

func TestPrepareKubernetesReleaseRejectsDuplicateCutoverInput(t *testing.T) {
	legacyRoot := writeLegacyReleaseRoot(t)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "tenant_1")
	payload := readPreparedFile(t, filepath.Dir(inputsPath), filepath.Base(inputsPath))
	payload = strings.Replace(payload, "  google_redirect_url: \"https://api.noebs.sd/oauth/callback\"\n", "  google_client_id: stale-google-client-id\n  google_redirect_url: \"https://api.noebs.sd/oauth/callback\"\n", 1)
	writePreflightFile(t, filepath.Dir(inputsPath), filepath.Base(inputsPath), payload)
	outputRoot := filepath.Join(t.TempDir(), "kubernetes-release")

	err := prepareKubernetesRelease("..", legacyRoot, inputsPath, outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "duplicates current secret noebs.google_client_id") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want duplicate current secret rejection", err)
	}
}

func TestPrepareKubernetesReleaseRejectsMissingGoogleCutoverValue(t *testing.T) {
	legacyRoot := writeLegacyReleaseRoot(t)
	secretPath := filepath.Join(legacyRoot, "secrets.yaml")
	payload := readPreparedFile(t, legacyRoot, "secrets.yaml")
	payload = strings.ReplaceAll(payload, "  google_client_id: legacy-google-client-id\n", "")
	writePreflightFile(t, legacyRoot, "secrets.yaml", payload)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "tenant_1")
	outputRoot := filepath.Join(t.TempDir(), "kubernetes-release")

	err := prepareKubernetesRelease("..", legacyRoot, inputsPath, outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "missing kubernetes release input noebs.google_client_id") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want missing google cutover value rejection", err)
	}
	assertPathMissing(t, outputRoot)
	if _, statErr := os.Stat(secretPath); statErr != nil {
		t.Fatalf("legacy secret file should remain in place: %v", statErr)
	}
}

func TestPrepareKubernetesReleaseUsesExplicitGoogleWhenCurrentSecretMissing(t *testing.T) {
	legacyRoot := writeLegacyReleaseRoot(t)
	payload := readPreparedFile(t, legacyRoot, "secrets.yaml")
	payload = strings.ReplaceAll(payload, "  google_client_id: legacy-google-client-id\n", "")
	payload = strings.ReplaceAll(payload, "  google_client_secret: legacy-google-client-secret\n", "")
	writePreflightFile(t, legacyRoot, "secrets.yaml", payload)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "tenant_1")
	inputs := readPreparedFile(t, filepath.Dir(inputsPath), filepath.Base(inputsPath))
	inputs = strings.Replace(inputs, "  google_redirect_url: \"https://api.noebs.sd/oauth/callback\"\n", "  google_client_id: input-google-client-id\n  google_client_secret: input-google-client-secret\n  google_redirect_url: \"https://api.noebs.sd/oauth/callback\"\n", 1)
	writePreflightFile(t, filepath.Dir(inputsPath), filepath.Base(inputsPath), inputs)
	outputRoot := filepath.Join(t.TempDir(), "kubernetes-release")

	err := prepareKubernetesRelease("..", legacyRoot, inputsPath, outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err != nil {
		t.Fatalf("prepareKubernetesRelease() error = %v", err)
	}
	identitySecret := readYAMLMapFileMust(t, filepath.Join(outputRoot, "secrets", "identity-auth.secrets.yaml"))
	identityNoebs := getMap(identitySecret, "noebs")
	if got := firstString(identityNoebs, "google_client_id"); got != "input-google-client-id" {
		t.Fatalf("google_client_id = %q, want explicit input", got)
	}
	if got := firstString(identityNoebs, "google_client_secret"); got != "input-google-client-secret" {
		t.Fatalf("google_client_secret = %q, want explicit input", got)
	}
}

func TestPrepareKubernetesReleaseTransformsCurrentSecretValues(t *testing.T) {
	legacyRoot := writeCompleteLegacyReleaseRoot(t)
	writePreflightFile(t, legacyRoot, "kubernetes-release.inputs.yaml", "noebs: {}\n")
	outputRoot := filepath.Join(t.TempDir(), "kubernetes-release")

	err := prepareKubernetesRelease("..", legacyRoot, filepath.Join(legacyRoot, "kubernetes-release.inputs.yaml"), outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err != nil {
		t.Fatalf("prepareKubernetesRelease() error = %v", err)
	}
	apiSecret := readYAMLMapFileMust(t, filepath.Join(outputRoot, "secrets", "api-gateway.secrets.yaml"))
	apiNoebs := getMap(apiSecret, "noebs")
	if got := firstString(apiNoebs, "admin_key"); got != "legacy-admin-key" {
		t.Fatalf("admin_key = %q, want current secret value", got)
	}
	identitySecret := readYAMLMapFileMust(t, filepath.Join(outputRoot, "secrets", "identity-auth.secrets.yaml"))
	identityNoebs := getMap(identitySecret, "noebs")
	if got := firstString(identityNoebs, "sms_key"); got != "legacy-sms-key" {
		t.Fatalf("sms_key = %q, want current secret value", got)
	}
	ebsSecret := readYAMLMapFileMust(t, filepath.Join(outputRoot, "secrets", "ebs-adapter.secrets.yaml"))
	ebsNoebs := getMap(ebsSecret, "noebs")
	if got := firstString(ebsNoebs, "consumer_endpoint"); got != "https://legacy-consumer.example" {
		t.Fatalf("consumer_endpoint = %q, want current secret value", got)
	}
	pspSecret := readYAMLMapFileMust(t, filepath.Join(outputRoot, "secrets", "psp-webhook.secrets.yaml"))
	pspNoebs := getMap(pspSecret, "noebs")
	psp := getMap(pspNoebs, "psp")
	if _, ok := psp["tenant_1"]; !ok {
		t.Fatalf("psp tenants = %#v, want current secret PSP map", psp)
	}
	if got := strings.TrimSpace(readPreparedFile(t, outputRoot, "platform/temporal-postgres-password.txt")); got != "legacy-temporal-postgres-password" {
		t.Fatalf("temporal postgres password = %q, want current secret value", got)
	}
	keycloakConfig := readPreparedFile(t, outputRoot, "platform/keycloak.conf")
	if !strings.Contains(keycloakConfig, "bootstrap-admin-username=legacy-keycloak-admin") {
		t.Fatalf("keycloak config did not use current secret bootstrap admin username:\n%s", keycloakConfig)
	}
}

func TestPrepareKubernetesReleaseRejectsMissingExplicitEBSRuntimeEndpoint(t *testing.T) {
	legacyRoot := writeLegacyReleaseRoot(t)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "tenant_1")
	payload := readPreparedFile(t, filepath.Dir(inputsPath), filepath.Base(inputsPath))
	payload = strings.ReplaceAll(payload, "    consumer_endpoint: \"https://consumer.input.example\"\n", "")
	writePreflightFile(t, filepath.Dir(inputsPath), filepath.Base(inputsPath), payload)
	outputRoot := filepath.Join(t.TempDir(), "kubernetes-release")

	err := prepareKubernetesRelease("..", legacyRoot, inputsPath, outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "noebs.ebs.consumer_endpoint") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want missing explicit EBS endpoint rejection", err)
	}
	assertPathMissing(t, outputRoot)
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

func TestKubernetesReleaseInputsExampleMatchesStrictSchema(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host", "kubernetes-release.inputs.yaml.example"))
	if err != nil {
		t.Fatalf("read kubernetes release inputs example: %v", err)
	}
	inputs := replaceKubernetesReleaseInputPlaceholders(t, string(payload))
	legacyRoot := writeLegacyReleaseRoot(t)
	legacyPayload := readPreparedFile(t, legacyRoot, "secrets.yaml")
	legacyPayload = strings.ReplaceAll(legacyPayload, "  google_client_id: legacy-google-client-id\n", "")
	legacyPayload = strings.ReplaceAll(legacyPayload, "  google_client_secret: legacy-google-client-secret\n", "")
	writePreflightFile(t, legacyRoot, "secrets.yaml", legacyPayload)
	writePreflightFile(t, legacyRoot, "kubernetes-release.inputs.yaml", inputs)
	outputRoot := filepath.Join(t.TempDir(), "kubernetes-release")

	err = prepareKubernetesRelease("..", legacyRoot, filepath.Join(legacyRoot, "kubernetes-release.inputs.yaml"), outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err != nil {
		t.Fatalf("prepareKubernetesRelease() with example-derived inputs error = %v", err)
	}
	requirePreparedFile(t, outputRoot, "secrets/identity-auth.secrets.yaml")
	requirePreparedFile(t, outputRoot, "secrets/ebs-adapter.secrets.yaml")
}

func TestAuditKubernetesReleaseInputsReportsMissingCurrentAndCutoverValues(t *testing.T) {
	legacyRoot := writeLegacyReleaseRoot(t)

	audit, err := auditKubernetesReleaseInputs(legacyRoot, "", readPlainPreflightSecret)
	if err != nil {
		t.Fatalf("auditKubernetesReleaseInputs() error = %v", err)
	}
	if audit.Ready {
		t.Fatalf("audit ready = true, want false")
	}
	requireStringEntry(t, audit.CurrentSecret, "noebs.db_url from current secret")
	requireStringEntry(t, audit.CurrentSecret, "noebs.jwt_secret from current secret")
	requireStringEntry(t, audit.CurrentSecret, "noebs.google_client_id from current secret noebs.google_client_id")
	requireStringEntry(t, audit.Missing, "noebs.default_tenant_id")
	requireStringEntry(t, audit.Missing, "noebs.admin_key")
	requireStringEntry(t, audit.Missing, "noebs.ebs.consumer_endpoint")
	requireStringEntry(t, audit.Missing, "noebs.psp")
}

func TestAuditKubernetesReleaseInputsReportsReadyCompleteCurrentSecret(t *testing.T) {
	legacyRoot := writeCompleteLegacyReleaseRoot(t)

	audit, err := auditKubernetesReleaseInputs(legacyRoot, "", readPlainPreflightSecret)
	if err != nil {
		t.Fatalf("auditKubernetesReleaseInputs() error = %v", err)
	}
	if !audit.Ready {
		t.Fatalf("audit ready = false, missing=%v duplicate=%v invalid=%v", audit.Missing, audit.Duplicate, audit.Invalid)
	}
	requireStringEntry(t, audit.CurrentSecret, "noebs.default_tenant_id from current secret noebs.default_tenant_id")
	requireStringEntry(t, audit.CurrentSecret, "noebs.psp from current secret")
	if len(audit.Missing) != 0 || len(audit.Duplicate) != 0 || len(audit.Invalid) != 0 {
		t.Fatalf("audit has failures: missing=%v duplicate=%v invalid=%v", audit.Missing, audit.Duplicate, audit.Invalid)
	}
}

func TestAuditKubernetesReleaseInputsReportsDuplicatesAndDoesNotPrintSecretValues(t *testing.T) {
	legacyRoot := writeCompleteLegacyReleaseRoot(t)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "tenant_1")

	audit, err := auditKubernetesReleaseInputs(legacyRoot, inputsPath, readPlainPreflightSecret)
	if err != nil {
		t.Fatalf("auditKubernetesReleaseInputs() error = %v", err)
	}
	if audit.Ready {
		t.Fatalf("audit ready = true, want false")
	}
	requireStringEntry(t, audit.Duplicate, "noebs.admin_key duplicates current secret noebs.admin_key")
	requireStringEntry(t, audit.Duplicate, "noebs.ebs.consumer_endpoint duplicates current secret noebs.consumer_endpoint")
	requireStringEntry(t, audit.Duplicate, "noebs.psp duplicates current secret noebs.psp")

	var out strings.Builder
	if err := writeKubernetesReleaseInputAudit(&out, audit); err != nil {
		t.Fatalf("writeKubernetesReleaseInputAudit() error = %v", err)
	}
	for _, secretValue := range []string{
		"legacy-admin-key",
		"admin-key",
		"legacy-ipin-password",
		"ipin-password",
		"legacy-psp-api-key",
		"psp-api-key",
	} {
		if strings.Contains(out.String(), secretValue) {
			t.Fatalf("audit output leaked secret value %q:\n%s", secretValue, out.String())
		}
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
  google_client_id: legacy-google-client-id
  google_client_secret: legacy-google-client-secret
`)
	return root
}

func writeCompleteLegacyReleaseRoot(t *testing.T) string {
	t.Helper()
	root := writeLegacyReleaseRoot(t)
	writePreflightFile(t, root, "secrets.yaml", `noebs:
  db_url: "postgres://legacy-user:legacy-pass@legacy-db:5432/noebs?sslmode=disable"
  jwt_secret: jwt-secret
  default_tenant_id: tenant_1
  admin_key: legacy-admin-key
  admin_user: legacy-admin
  admin_password: legacy-admin-password
  sms_key: legacy-sms-key
  sms_sender: legacy-sms-sender
  sms_gateway: "https://legacy.sms.example"
  sms_message: "legacy-code"
  google_client_id: legacy-google-client-id
  google_client_secret: legacy-google-client-secret
  google_redirect_url: "https://legacy.example/oauth/callback"
  data_key: legacy-card-vault-data-key
  temporal_postgres_password: legacy-temporal-postgres-password
  keycloak_postgres_password: legacy-keycloak-postgres-password
  keycloak_bootstrap_admin_username: legacy-keycloak-admin
  keycloak_bootstrap_admin_password: legacy-keycloak-admin-password
  consumer_endpoint: "https://legacy-consumer.example"
  merchant_endpoint: "https://legacy-merchant.example"
  ipin_endpoint: "https://legacy-ipin.example"
  consumer_app_id: legacy-consumer-app
  merchant_app_id: legacy-merchant-app
  ipin_username: legacy-ipin-user
  ipin_password: legacy-ipin-password
  pub_key: legacy-consumer-public-key
  ipin_key: legacy-ipin-public-key
  pan: "1234567890123456"
  pin: "1234"
  ipin: "123456"
  exp_date: "0129"
  psp:
    tenant_1:
      test-provider:
        api_key: legacy-psp-api-key
        api_secret: legacy-psp-api-secret
        webhook_secret: legacy-psp-webhook-secret
        webhook_public_key: legacy-psp-webhook-public-key
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
  sms_key: input-sms-key
  sms_sender: input-sms-sender
  sms_gateway: "https://input.sms.example"
  sms_message: "code"
  google_redirect_url: "https://api.noebs.sd/oauth/callback"
  card_vault_data_key: card-vault-data-key
  temporal_postgres_password: temporal-postgres-password
  keycloak_postgres_password: keycloak-postgres-password
  keycloak_bootstrap_admin_username: keycloak-admin
  keycloak_bootstrap_admin_password: keycloak-admin-password
  ebs:
    consumer_endpoint: "https://consumer.input.example"
    merchant_endpoint: "https://merchant.input.example"
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

func replaceKubernetesReleaseInputPlaceholders(t *testing.T, payload string) string {
	t.Helper()
	replacements := map[string]string{
		"REPLACE_WITH_TENANT_ID":                         "tenant_1",
		"REPLACE_WITH_GATEWAY_ADMIN_KEY":                 "admin-key",
		"REPLACE_WITH_GATEWAY_ADMIN_USER":                "admin",
		"REPLACE_WITH_GATEWAY_ADMIN_PASSWORD":            "admin-password",
		"REPLACE_WITH_SMS_API_KEY":                       "input-sms-key",
		"REPLACE_WITH_SMS_SENDER":                        "input-sms-sender",
		"REPLACE_WITH_SMS_GATEWAY":                       "https://input.sms.example",
		"REPLACE_WITH_SMS_MESSAGE":                       "code",
		"REPLACE_WITH_GOOGLE_CLIENT_ID":                  "input-google-client-id",
		"REPLACE_WITH_GOOGLE_CLIENT_SECRET":              "input-google-client-secret",
		"REPLACE_WITH_GOOGLE_REDIRECT_URL":               "https://api.noebs.sd/oauth/callback",
		"REPLACE_WITH_CARD_VAULT_DATA_KEY":               "card-vault-data-key",
		"REPLACE_WITH_TEMPORAL_POSTGRES_PASSWORD":        "temporal-postgres-password",
		"REPLACE_WITH_KEYCLOAK_POSTGRES_PASSWORD":        "keycloak-postgres-password",
		"REPLACE_WITH_KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME": "keycloak-admin",
		"REPLACE_WITH_KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD": "keycloak-admin-password",
		"REPLACE_WITH_EBS_CONSUMER_ENDPOINT":             "https://consumer.input.example",
		"REPLACE_WITH_EBS_MERCHANT_ENDPOINT":             "https://merchant.input.example",
		"REPLACE_WITH_EBS_IPIN_ENDPOINT":                 "https://ipin.example",
		"REPLACE_WITH_EBS_CONSUMER_APP_ID":               "consumer-app",
		"REPLACE_WITH_EBS_MERCHANT_APP_ID":               "merchant-app",
		"REPLACE_WITH_EBS_IPIN_USERNAME":                 "ipin-user",
		"REPLACE_WITH_EBS_IPIN_PASSWORD":                 "ipin-password",
		"REPLACE_WITH_EBS_CONSUMER_PUBLIC_KEY":           "consumer-public-key",
		"REPLACE_WITH_EBS_IPIN_PUBLIC_KEY":               "ipin-public-key",
		"REPLACE_WITH_BILL_INQUIRY_PAN":                  "1234567890123456",
		"REPLACE_WITH_BILL_INQUIRY_PIN":                  "1234",
		"REPLACE_WITH_BILL_INQUIRY_IPIN":                 "123456",
		"REPLACE_WITH_BILL_INQUIRY_EXPIRY":               "0129",
		"REPLACE_WITH_PROVIDER_CODE":                     "test-provider",
		"REPLACE_WITH_PSP_API_KEY":                       "psp-api-key",
		"REPLACE_WITH_PSP_API_SECRET":                    "psp-api-secret",
		"REPLACE_WITH_PSP_WEBHOOK_SECRET":                "psp-webhook-secret",
		"REPLACE_WITH_PSP_WEBHOOK_PUBLIC_KEY":            "psp-webhook-public-key",
	}
	for placeholder, value := range replacements {
		payload = strings.ReplaceAll(payload, placeholder, value)
	}
	if strings.Contains(payload, "REPLACE_WITH_") {
		t.Fatalf("kubernetes release inputs example contains unreplaced placeholder:\n%s", payload)
	}
	return payload
}

func plainKubernetesSecretEncrypt(_ string, payload []byte, _ string) ([]byte, error) {
	return payload, nil
}

func requireStringEntry(t *testing.T, entries []string, want string) {
	t.Helper()
	for _, entry := range entries {
		if entry == want {
			return
		}
	}
	t.Fatalf("entries missing %q: %v", want, entries)
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

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s error = %v, want not exist", path, err)
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
