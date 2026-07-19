package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
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
	certPath, keyPath := writeTestTLSPair(t)

	var output bytes.Buffer
	if err := renderKubernetesSecrets(&output, root, "noebs", certPath, keyPath, readPlainPreflightSecret); err != nil {
		t.Fatalf("renderKubernetesSecrets() error = %v", err)
	}

	secrets := decodeRenderedKubernetesSecrets(t, output.Bytes())
	expectedNames := map[string]bool{
		"sops-age-key":                  false,
		"postgres-credentials":          false,
		"workload-auth-postgres-roles":  false,
		"internal-transport-platform":   false,
		"temporal-postgres-credentials": false,
		"keycloak-postgres-credentials": false,
		"keycloak-secrets":              false,
		"ghcr-credentials":              false,
		"noebs-tls":                     false,
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

	requireRenderedSecretValue(t, secrets, "postgres-credentials", "password", "legacy-pass")
	requireRenderedSecretContains(t, secrets, "postgres-credentials", "tls.crt", "BEGIN CERTIFICATE")
	requireRenderedSecretContains(t, secrets, "postgres-credentials", "tls.key", "BEGIN EC PRIVATE KEY")
	requireRenderedSecretValue(t, secrets, "temporal-postgres-credentials", "password", "temporal-postgres-password")
	requireRenderedSecretValue(t, secrets, "keycloak-postgres-credentials", "password", "keycloak-postgres-password")
	requireRenderedSecretContains(t, secrets, "ghcr-credentials", ".dockerconfigjson", `"ghcr.io"`)
	requireRenderedSecretContains(t, secrets, "ebs-adapter-secrets", "secrets.yaml", "consumer_endpoint")
	requireRenderedSecretContains(t, secrets, "sops-age-key", "age-key.txt", "AGE-SECRET-KEY-1LOCAL")
	keycloakConfig := secretByName(t, secrets, "keycloak-secrets").StringData["keycloak.conf"]
	for _, forbidden := range []string{"bootstrap-admin-username", "bootstrap-admin-password", "bootstrap-admin-client-id", "bootstrap-admin-client-secret"} {
		if strings.Contains(keycloakConfig, forbidden) {
			t.Fatalf("rendered steady Keycloak Secret contains %s", forbidden)
		}
	}
	requireRenderedSecretContains(t, secrets, "noebs-tls", "tls.crt", "BEGIN CERTIFICATE")
	requireRenderedSecretContains(t, secrets, "noebs-tls", "tls.key", "BEGIN RSA PRIVATE KEY")
	requireRenderedSecretContains(t, secrets, "workload-auth-postgres-roles", "roles.yaml", "migrate_password")
	internalTransportPlatform := secretByName(t, secrets, "internal-transport-platform").StringData["credentials.yaml"]
	if strings.Contains(internalTransportPlatform, "ca_private_key") {
		t.Fatal("rendered internal transport platform Secret contains the signing CA private key")
	}
	if secretByName(t, secrets, "noebs-tls").Type != "kubernetes.io/tls" {
		t.Fatalf("noebs-tls type = %q, want kubernetes.io/tls", secretByName(t, secrets, "noebs-tls").Type)
	}
	if secretByName(t, secrets, "ghcr-credentials").Type != "kubernetes.io/dockerconfigjson" {
		t.Fatalf("ghcr-credentials type = %q, want kubernetes.io/dockerconfigjson", secretByName(t, secrets, "ghcr-credentials").Type)
	}
}

func TestRenderKubernetesSecretsRejectsMissingServiceSecret(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	certPath, keyPath := writeTestTLSPair(t)
	if err := os.Remove(filepath.Join(root, "secrets", "wallet-api.secrets.yaml")); err != nil {
		t.Fatalf("remove wallet-api secret: %v", err)
	}

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", certPath, keyPath, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "wallet-api secrets") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want missing wallet-api secret rejection", err)
	}
}

func TestRenderKubernetesSecretsRejectsMissingServiceConfig(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	certPath, keyPath := writeTestTLSPair(t)
	if err := os.Remove(filepath.Join(root, "services", "wallet-api.yaml")); err != nil {
		t.Fatalf("remove wallet-api service config: %v", err)
	}

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", certPath, keyPath, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "wallet-api.yaml") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want missing wallet-api service config rejection", err)
	}
}

func TestRenderKubernetesSecretsRejectsMissingMigrationServiceConfig(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	certPath, keyPath := writeTestTLSPair(t)
	if err := os.Remove(filepath.Join(root, "services", "wallet-ledger-migrate.yaml")); err != nil {
		t.Fatalf("remove wallet-ledger-migrate service config: %v", err)
	}

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", certPath, keyPath, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "wallet-ledger-migrate.yaml") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want missing wallet-ledger-migrate service config rejection", err)
	}
}

func TestRenderKubernetesSecretsRejectsUnexpectedServiceSecret(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	certPath, keyPath := writeTestTLSPair(t)
	writePreflightFile(t, root, "secrets/monolith.secrets.yaml", `noebs:
  default_tenant_id: tenant_1
`)

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", certPath, keyPath, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "unexpected Kubernetes release service secret file") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want unexpected service secret rejection", err)
	}
}

func TestRenderKubernetesSecretsRejectsUnexpectedRootEntry(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	certPath, keyPath := writeTestTLSPair(t)
	writePreflightFile(t, root, "config.old.yaml", `noebs:
  service_role: api-gateway
`)

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", certPath, keyPath, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "unexpected Kubernetes release root entry") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want unexpected root entry rejection", err)
	}
}

func TestRenderKubernetesSecretsRejectsInvalidTLSPair(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	certPath := filepath.Join(t.TempDir(), "tls.crt")
	keyPath := filepath.Join(t.TempDir(), "tls.key")
	writePreflightFile(t, filepath.Dir(certPath), filepath.Base(certPath), "not a certificate")
	writePreflightFile(t, filepath.Dir(keyPath), filepath.Base(keyPath), "not a private key")

	var output bytes.Buffer
	err := renderKubernetesSecrets(&output, root, "noebs", certPath, keyPath, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "validate TLS certificate and key") {
		t.Fatalf("renderKubernetesSecrets() error = %v, want TLS validation rejection", err)
	}
}

func writeKubernetesSecretReleaseRoot(t *testing.T) string {
	t.Helper()
	legacyRoot := writeLegacyReleaseRoot(t)
	inputsPath := writeKubernetesReleaseInputsFile(t, legacyRoot, "tenant_1")
	root := filepath.Join(t.TempDir(), "release")
	if err := prepareKubernetesRelease("..", legacyRoot, inputsPath, root, readPlainPreflightSecret, plainKubernetesSecretEncrypt); err != nil {
		t.Fatalf("prepare test Kubernetes release: %v", err)
	}
	return root
}

func kubernetesSecretTestPayloads() map[string]string {
	payloads := map[string]string{
		"api-gateway.secrets.yaml": `noebs:
  default_tenant_id: tenant_1
  jwt_secret: jwt-secret
  admin_key: admin-key
  admin_user: admin
  admin_password: admin-password
`,
		"identity-auth.secrets.yaml": serviceDatabaseSecret("identity-auth") + `  jwt_secret: jwt-secret
  google_client_id: google-client-id
  google_client_secret: google-client-secret
  google_redirect_url: "https://api.noebs.sd/oauth/callback"
  sms_key: sms-key
  sms_sender: noebs
  sms_gateway: "https://sms-gateway.noebs.sd/send?"
  sms_message: "code"
`,
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
		"psp-webhook.secrets.yaml":          serviceDatabaseSecret("psp-webhook") + pspSecretMap(),
		"admin-reporting.secrets.yaml":      serviceDatabaseSecret("admin-reporting"),
		"notification-chat.secrets.yaml":    serviceDatabaseSecret("notification-chat"),
		"consumer-beneficiary.secrets.yaml": serviceDatabaseSecret("consumer-beneficiary"),
		"wallet-api.secrets.yaml": `noebs:
  default_tenant_id: tenant_1
`,
		"wallet-ledger.secrets.yaml": serviceDatabaseSecret("wallet-ledger"),
		"wallet-worker.secrets.yaml": serviceDatabaseSecret("wallet-ledger") + pspSecretMap(),
	}
	payloads["ebs-adapter-events.secrets.yaml"] = serviceDatabaseSecret("ebs-adapter")
	payloads["admin-reporting-projector.secrets.yaml"] = serviceDatabaseSecret("admin-reporting")
	for role, owner := range map[string]string{
		"identity-auth-migrate":        "identity-auth",
		"ebs-adapter-migrate":          "ebs-adapter",
		"psp-webhook-migrate":          "psp-webhook",
		"admin-reporting-migrate":      "admin-reporting",
		"notification-chat-migrate":    "notification-chat",
		"consumer-beneficiary-migrate": "consumer-beneficiary",
		"wallet-ledger-migrate":        "wallet-ledger",
	} {
		payloads[role+".secrets.yaml"] = serviceDatabaseSecret(owner)
	}
	payloads["card-vault-migrate.secrets.yaml"] = serviceDatabaseSecret("card-vault") + "  data_key: card-vault-data-key\n"

	random := make([]byte, 2048)
	for index := range random {
		random[index] = byte(index)
	}
	prepared, err := prepareWorkloadAuthRelease(kubernetesReleaseWorkloadAuthInputs{}, bytes.NewReader(random))
	if err != nil {
		panic(err)
	}
	preparedTransport, err := prepareInternalTransportRelease(kubernetesReleaseInternalTransportInputs{}, rand.Reader, time.Now().UTC())
	if err != nil {
		panic(err)
	}
	payloads["workload-auth-migrate.secrets.yaml"] = `noebs:
  default_tenant_id: tenant_1
  database_ca_certificate: |-
` + indentYAMLBlock(preparedTransport.caCertificate, 4) + `
  service_databases:
    workload-auth-migrate: "` + workloadAuthDatabaseURL("workload_auth_migrate", prepared.database.migratePassword) + `"
`
	payloads["workload-auth-cleanup.secrets.yaml"] = `noebs:
  default_tenant_id: tenant_1
  database_ca_certificate: |-
` + indentYAMLBlock(preparedTransport.caCertificate, 4) + `
  service_databases:
    workload-auth-migrate: "` + workloadAuthDatabaseURL("workload_auth_cleanup", prepared.database.cleanupPassword) + `"
`
	for _, role := range []serviceRole{
		serviceRoleAPIGateway,
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
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
  default_tenant_id: tenant_1
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
    tenant_1:
      test-provider:
        api_key: psp-api-key
        api_secret: psp-api-secret
        webhook_secret: psp-webhook-secret
        webhook_public_key: psp-webhook-public-key
`
}

func writeTestTLSPair(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "api.noebs.sd"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"api.noebs.sd", "dsa.adonese.sd"},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	root := t.TempDir()
	certPath := filepath.Join(root, "tls.crt")
	keyPath := filepath.Join(root, "tls.key")
	writePEMFile(t, certPath, "CERTIFICATE", certDER)
	writePEMFile(t, keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	return certPath, keyPath
}

func writePEMFile(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	payload := bytes.Buffer{}
	if err := pem.Encode(&payload, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, payload.Bytes(), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
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
