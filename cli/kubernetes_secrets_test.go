package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type renderedKubernetesSecret struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Type       string            `yaml:"type"`
	Metadata   kubernetesMeta    `yaml:"metadata"`
	StringData map[string]string `yaml:"stringData"`
}

func TestRenderKubernetesSecretsFromExplicitRelease(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)

	var output bytes.Buffer
	if err := renderKubernetesSecrets(&output, root, "noebs", readPlainPreflightSecret); err != nil {
		t.Fatalf("renderKubernetesSecrets() error = %v", err)
	}

	secrets := decodeRenderedKubernetesSecrets(t, output.Bytes())
	expectedNames := map[string]bool{
		"noebs-release-manifest":          false,
		"sops-age-key":                    false,
		"postgres-credentials":            false,
		"workload-auth-postgres-roles":    false,
		"gateway-auth-postgres-roles":     false,
		"internal-transport-platform":     false,
		"temporal-postgres-credentials":   false,
		"keycloak-postgres-credentials":   false,
		"keycloak-secrets":                false,
		"keycloak-transport-ca":           false,
		"keycloak-reconciler-credentials": false,
		"ghcr-credentials":                false,
	}
	for _, source := range kubernetesServiceSecretSources {
		expectedNames[source.secretName] = false
	}
	for _, secret := range secrets {
		if secret.APIVersion != "v1" || secret.Kind != "Secret" {
			t.Fatalf("%s is %s/%s, want v1/Secret", secret.Metadata.Name, secret.APIVersion, secret.Kind)
		}
		if secret.Metadata.Namespace != "noebs" {
			t.Fatalf("%s namespace = %q, want noebs", secret.Metadata.Name, secret.Metadata.Namespace)
		}
		found, ok := expectedNames[secret.Metadata.Name]
		if !ok {
			t.Fatalf("unexpected Secret %q", secret.Metadata.Name)
		}
		if found {
			t.Fatalf("duplicate Secret %q", secret.Metadata.Name)
		}
		expectedNames[secret.Metadata.Name] = true
	}
	for name, found := range expectedNames {
		if !found {
			t.Fatalf("Secret %q not rendered", name)
		}
	}

	requireRenderedSecretValue(t, secrets, "postgres-credentials", "password", "noebs-postgres-password")
	requireRenderedSecretContains(t, secrets, "postgres-credentials", "tls.crt", "BEGIN CERTIFICATE")
	requireRenderedSecretContains(t, secrets, "postgres-credentials", "tls.key", "BEGIN EC PRIVATE KEY")
	requireRenderedSecretValue(t, secrets, "temporal-postgres-credentials", "password", "temporal-postgres-password")
	requireRenderedSecretValue(t, secrets, "keycloak-postgres-credentials", "password", "keycloak-postgres-password")
	requireRenderedSecretContains(t, secrets, "keycloak-postgres-credentials", "tls.crt", "BEGIN CERTIFICATE")
	requireRenderedSecretContains(t, secrets, "keycloak-postgres-credentials", "tls.key", "BEGIN EC PRIVATE KEY")
	requireRenderedSecretContains(t, secrets, "keycloak-secrets", "db-ca.pem", "BEGIN CERTIFICATE")
	requireRenderedSecretContains(t, secrets, "keycloak-secrets", "tls.crt", "BEGIN CERTIFICATE")
	requireRenderedSecretContains(t, secrets, "keycloak-secrets", "tls.key", "BEGIN EC PRIVATE KEY")
	requireRenderedSecretContains(t, secrets, "keycloak-transport-ca", "ca.pem", "BEGIN CERTIFICATE")
	if keys := secretByName(t, secrets, "keycloak-transport-ca").StringData; len(keys) != 1 {
		t.Fatalf("Keycloak transport CA Secret keys = %v", keys)
	}
	requireRenderedSecretContains(t, secrets, "ghcr-credentials", ".dockerconfigjson", `"ghcr.io"`)
	requireRenderedSecretContains(t, secrets, "ebs-adapter-secrets", "secrets.yaml", "consumer_endpoint")
	requireRenderedSecretContains(t, secrets, "sops-age-key", "age-key.txt", "AGE-SECRET-KEY-1LOCAL")
	keycloakConfig := secretByName(t, secrets, "keycloak-secrets").StringData["keycloak.conf"]
	for _, forbidden := range []string{"bootstrap-admin-username", "bootstrap-admin-password", "bootstrap-admin-client-id", "bootstrap-admin-client-secret"} {
		if strings.Contains(keycloakConfig, forbidden) {
			t.Fatalf("rendered steady Keycloak Secret contains %s", forbidden)
		}
	}
	requireRenderedSecretContains(t, secrets, "workload-auth-postgres-roles", "roles.yaml", "migrate_password")
	requireRenderedSecretContains(t, secrets, "gateway-auth-postgres-roles", "roles.yaml", "migrate_password")
	reconcilerConfig := secretByName(t, secrets, "keycloak-reconciler-credentials").StringData["config.yaml"]
	for _, required := range []string{"admin_realm: noebs", "client_id: noebs-keycloak-reconciler", "noebs-backoffice:", "google:"} {
		if !strings.Contains(reconcilerConfig, required) {
			t.Fatalf("rendered Keycloak reconciler config missing %q", required)
		}
	}
	internalTransportPlatform := secretByName(t, secrets, "internal-transport-platform").StringData["credentials.yaml"]
	if strings.Contains(internalTransportPlatform, "ca_private_key") {
		t.Fatal("rendered internal transport platform Secret contains the signing CA private key")
	}
	if secretByName(t, secrets, "ghcr-credentials").Type != "kubernetes.io/dockerconfigjson" {
		t.Fatalf("ghcr-credentials type = %q, want kubernetes.io/dockerconfigjson", secretByName(t, secrets, "ghcr-credentials").Type)
	}
}

func TestRenderKeycloakTransportCAContainsOnlyPublicTrustMaterial(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	var output bytes.Buffer
	if err := renderKeycloakTransportCA(&output, root, "edge", readPlainPreflightSecret); err != nil {
		t.Fatal(err)
	}
	secrets := decodeRenderedKubernetesSecrets(t, output.Bytes())
	if len(secrets) != 1 {
		t.Fatalf("rendered Secrets = %d, want 1", len(secrets))
	}
	secret := secrets[0]
	if secret.Metadata.Name != "keycloak-transport-ca" || secret.Metadata.Namespace != "edge" || len(secret.StringData) != 1 {
		t.Fatalf("rendered Keycloak transport CA = %#v", secret)
	}
	if !strings.Contains(secret.StringData["ca.pem"], "BEGIN CERTIFICATE") || strings.Contains(output.String(), "PRIVATE KEY") {
		t.Fatal("edge Keycloak trust render is not CA-only")
	}
	if secret.Metadata.Labels["app.kubernetes.io/managed-by"] != "noebs-release-renderer" {
		t.Fatalf("renderer ownership label = %q", secret.Metadata.Labels["app.kubernetes.io/managed-by"])
	}
}

func TestRenderKubernetesSecretsRejectsMissingServiceSecret(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	if err := os.Remove(filepath.Join(root, "secrets", "wallet-api.secrets.yaml")); err != nil {
		t.Fatalf("remove wallet-api secret: %v", err)
	}

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "wallet-api secrets") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want missing wallet-api secret rejection", err)
	}
}

func TestRenderKubernetesSecretsRejectsMissingServiceConfig(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	if err := os.Remove(filepath.Join(root, "services", "wallet-api.yaml")); err != nil {
		t.Fatalf("remove wallet-api service config: %v", err)
	}

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "wallet-api.yaml") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want missing wallet-api service config rejection", err)
	}
}

func TestRenderKubernetesSecretsRejectsMissingMigrationServiceConfig(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	if err := os.Remove(filepath.Join(root, "services", "wallet-ledger-migrate.yaml")); err != nil {
		t.Fatalf("remove wallet-ledger-migrate service config: %v", err)
	}

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "wallet-ledger-migrate.yaml") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want missing wallet-ledger-migrate service config rejection", err)
	}
}

func TestRenderKubernetesSecretsRejectsUnexpectedServiceSecret(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	writePreflightFile(t, root, "secrets/monolith.secrets.yaml", `noebs:
  default_tenant_id: tenant-cutover
`)

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "unexpected Kubernetes release service secret file") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want unexpected service secret rejection", err)
	}
}

func TestRenderKubernetesSecretsRejectsUnexpectedRootEntry(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	writePreflightFile(t, root, "config.old.yaml", `noebs:
  service_role: api-gateway
`)

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "unexpected Kubernetes release root entry") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want unexpected root entry rejection", err)
	}
}

func TestPreparedWorkloadAuthDoesNotGrantNotificationCallerAuthority(t *testing.T) {
	prepared, err := prepareWorkloadAuthRelease(kubernetesReleaseWorkloadAuthInputs{}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	config := prepared.configForRole(serviceRoleNotification)
	if _, present := config["signing_key_id"]; present {
		t.Fatal("notification-chat received a workload signing key id")
	}
	if _, present := config["signing_private_key"]; present {
		t.Fatal("notification-chat received a workload signing private key")
	}
	if _, present := prepared.callers[serviceRoleNotification]; present {
		t.Fatal("notification-chat exists in the prepared caller registry")
	}
}

func writeKubernetesSecretReleaseRoot(t *testing.T) string {
	t.Helper()
	inputRoot := t.TempDir()
	inputsPath := writeKubernetesReleaseInputsFile(t, inputRoot, "tenant-cutover")
	root := filepath.Join(t.TempDir(), "release")
	if err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), root, readPlainPreflightSecret, plainKubernetesSecretEncrypt); err != nil {
		t.Fatalf("prepare test Kubernetes release: %v", err)
	}
	return root
}

func kubernetesSecretTestPayloads() map[string]string {
	random := make([]byte, 4096)
	for index := range random {
		random[index] = byte(index)
	}
	randomReader := bytes.NewReader(random)
	gatewayAuth := preparedGatewayAuthRelease{
		migratePassword: testCanonicalReleaseSecret(3),
		runtimePassword: testCanonicalReleaseSecret(4),
		cleanupPassword: testCanonicalReleaseSecret(5),
		encryptionKeyID: "gateway-key-1",
		encryptionKeys: map[string]string{
			"gateway-key-1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32)),
		},
	}
	payloads := map[string]string{
		"api-gateway.secrets.yaml": `noebs:
  default_tenant_id: tenant-cutover
  backoffice_client_secret: backoffice-client-secret
  backoffice_encryption_key_id: ` + gatewayAuth.encryptionKeyID + `
  backoffice_encryption_keys:
    ` + gatewayAuth.encryptionKeyID + `: ` + gatewayAuth.encryptionKeys[gatewayAuth.encryptionKeyID] + `
  psp_webhook_routes:
    ` + testCanonicalReleaseSecret(11) + `:
      tenant_id: tenant-cutover
      provider_code: test-provider
  service_databases:
    api-gateway: "` + gatewayAuthDatabaseURL("gateway_auth_runtime", gatewayAuth.runtimePassword) + `"
`,
		"identity-auth.secrets.yaml": serviceDatabaseSecret("identity-auth"),
		"card-vault.secrets.yaml": serviceDatabaseSecret("card-vault") + `  data_key: card-vault-data-key
`,
		"ebs-adapter.secrets.yaml": serviceDatabaseSecret("ebs-adapter") + `  consumer_endpoint: "https://consumer.ebs.example"
  merchant_endpoint: "https://merchant.ebs.example"
  ipin_endpoint: "https://ipin.ebs.example"
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
`,
		"psp-webhook.secrets.yaml":       serviceDatabaseSecret("psp-webhook") + pspSecretMap(),
		"admin-reporting.secrets.yaml":   serviceDatabaseSecret("admin-reporting"),
		"notification-chat.secrets.yaml": serviceDatabaseSecret("notification-chat"),
		"wallet-api.secrets.yaml": `noebs:
  default_tenant_id: tenant-cutover
`,
		"wallet-ledger.secrets.yaml": serviceDatabaseSecret("wallet-ledger"),
		"wallet-worker.secrets.yaml": serviceDatabaseSecret("wallet-ledger") + pspSecretMap(),
	}
	payloads["ebs-adapter-events.secrets.yaml"] = serviceDatabaseSecret("ebs-adapter")
	payloads["admin-reporting-projector.secrets.yaml"] = serviceDatabaseSecret("admin-reporting")
	for role, owner := range map[string]string{
		"identity-auth-migrate":     "identity-auth",
		"ebs-adapter-migrate":       "ebs-adapter",
		"psp-webhook-migrate":       "psp-webhook",
		"admin-reporting-migrate":   "admin-reporting",
		"notification-chat-migrate": "notification-chat",
		"wallet-ledger-migrate":     "wallet-ledger",
	} {
		payloads[role+".secrets.yaml"] = serviceDatabaseSecret(owner)
	}
	payloads["card-vault-migrate.secrets.yaml"] = serviceDatabaseSecret("card-vault") + "  data_key: card-vault-data-key\n"

	prepared, err := prepareWorkloadAuthRelease(kubernetesReleaseWorkloadAuthInputs{}, randomReader)
	if err != nil {
		panic(err)
	}
	now := time.Now().UTC()
	transportInputs, err := generateTestInternalTransportInputs(now)
	if err != nil {
		panic(err)
	}
	preparedTransport, err := prepareInternalTransportRelease(transportInputs, rand.Reader, now)
	if err != nil {
		panic(err)
	}
	payloads["workload-auth-migrate.secrets.yaml"] = `noebs:
  default_tenant_id: tenant-cutover
  database_ca_certificate: |-
` + indentYAMLBlock(preparedTransport.caCertificate, 4) + `
  service_databases:
    workload-auth-migrate: "` + workloadAuthDatabaseURL("workload_auth_migrate", prepared.database.migratePassword) + `"
`
	payloads["workload-auth-cleanup.secrets.yaml"] = `noebs:
  default_tenant_id: tenant-cutover
  database_ca_certificate: |-
` + indentYAMLBlock(preparedTransport.caCertificate, 4) + `
  service_databases:
    workload-auth-migrate: "` + workloadAuthDatabaseURL("workload_auth_cleanup", prepared.database.cleanupPassword) + `"
`
	payloads["gateway-auth-migrate.secrets.yaml"] = `noebs:
  default_tenant_id: tenant-cutover
  database_ca_certificate: |-
` + indentYAMLBlock(preparedTransport.caCertificate, 4) + `
  service_databases:
    api-gateway: "` + gatewayAuthDatabaseURL("gateway_auth_migrate", gatewayAuth.migratePassword) + `"
`
	payloads["gateway-auth-cleanup.secrets.yaml"] = `noebs:
  default_tenant_id: tenant-cutover
  database_ca_certificate: |-
` + indentYAMLBlock(preparedTransport.caCertificate, 4) + `
  service_databases:
    api-gateway: "` + gatewayAuthDatabaseURL("gateway_auth_cleanup", gatewayAuth.cleanupPassword) + `"
`
	for _, role := range []serviceRole{
		serviceRoleAPIGateway,
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleWalletAPI,
		serviceRoleWalletLedger,
	} {
		fileName := string(role) + ".secrets.yaml"
		var document map[string]interface{}
		if err := yaml.Unmarshal([]byte(payloads[fileName]), &document); err != nil {
			panic(err)
		}
		getMap(document, "noebs")["workload_auth"] = prepared.configForRole(role)
		getMap(document, "noebs")["internal_transport"] = preparedTransport.configForRole(role)
		if role == serviceRoleAPIGateway {
			getMap(document, "noebs")["keycloak_ca_certificate"] = preparedTransport.caCertificate
		}
		if len(getMap(getMap(document, "noebs"), "service_databases")) != 0 || roleReceivesSignedHTTP(role) {
			getMap(document, "noebs")["database_ca_certificate"] = preparedTransport.caCertificate
		}
		encoded, err := yaml.Marshal(document)
		if err != nil {
			panic(err)
		}
		payloads[fileName] = string(encoded)
	}
	for fileName, payload := range payloads {
		var document map[string]interface{}
		if err := yaml.Unmarshal([]byte(payload), &document); err != nil {
			panic(err)
		}
		noebs := getMap(document, "noebs")
		if len(getMap(noebs, "service_databases")) == 0 {
			continue
		}
		noebs["database_ca_certificate"] = preparedTransport.caCertificate
		encoded, err := yaml.Marshal(document)
		if err != nil {
			panic(err)
		}
		payloads[fileName] = string(encoded)
	}
	return payloads
}

func serviceDatabaseSecret(serviceName string) string {
	databaseName := strings.ReplaceAll(serviceName, "-", "_")
	return `noebs:
  default_tenant_id: tenant-cutover
  service_databases:
    ` + serviceName + `: "postgres://noebs:service-password@postgres:5432/` + databaseName + `?sslmode=verify-full"
`
}

func indentYAMLBlock(value string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	return prefix + strings.ReplaceAll(strings.TrimSuffix(value, "\n"), "\n", "\n"+prefix)
}

func pspSecretMap() string {
	return `  psp:
    tenant-cutover:
      test-provider:
        api_key: psp-api-key
        api_secret: psp-api-secret
        webhook_secret: psp-webhook-secret
        webhook_public_key: psp-webhook-public-key
`
}

func decodeRenderedKubernetesSecrets(t *testing.T, payload []byte) []renderedKubernetesSecret {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(payload))
	var secrets []renderedKubernetesSecret
	for {
		var secret renderedKubernetesSecret
		if err := decoder.Decode(&secret); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered secrets: %v", err)
		}
		if secret.Kind != "" {
			secrets = append(secrets, secret)
		}
	}
	return secrets
}

func secretByName(t *testing.T, secrets []renderedKubernetesSecret, name string) renderedKubernetesSecret {
	t.Helper()
	for _, secret := range secrets {
		if secret.Metadata.Name == name {
			return secret
		}
	}
	t.Fatalf("Secret %q not found", name)
	return renderedKubernetesSecret{}
}

func requireRenderedSecretValue(t *testing.T, secrets []renderedKubernetesSecret, name, key, value string) {
	t.Helper()
	secret := secretByName(t, secrets, name)
	if secret.StringData[key] != value {
		t.Fatalf("%s stringData[%s] = %q, want %q", name, key, secret.StringData[key], value)
	}
}

func requireRenderedSecretContains(t *testing.T, secrets []renderedKubernetesSecret, name, key, value string) {
	t.Helper()
	secret := secretByName(t, secrets, name)
	if !strings.Contains(secret.StringData[key], value) {
		t.Fatalf("%s stringData[%s] missing %q", name, key, value)
	}
}
