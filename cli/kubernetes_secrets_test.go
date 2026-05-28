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
		"api-gateway-secrets":           false,
		"identity-auth-secrets":         false,
		"card-vault-secrets":            false,
		"ebs-adapter-secrets":           false,
		"psp-webhook-secrets":           false,
		"admin-reporting-secrets":       false,
		"notification-chat-secrets":     false,
		"consumer-beneficiary-secrets":  false,
		"wallet-api-secrets":            false,
		"wallet-ledger-secrets":         false,
		"wallet-worker-secrets":         false,
		"sops-age-key":                  false,
		"postgres-credentials":          false,
		"temporal-postgres-credentials": false,
		"keycloak-postgres-credentials": false,
		"keycloak-secrets":              false,
		"noebs-tls":                     false,
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

	requireRenderedSecretValue(t, secrets, "postgres-credentials", "password", "postgres-password")
	requireRenderedSecretValue(t, secrets, "temporal-postgres-credentials", "password", "temporal-postgres-password")
	requireRenderedSecretValue(t, secrets, "keycloak-postgres-credentials", "password", "keycloak-postgres-password")
	requireRenderedSecretContains(t, secrets, "ebs-adapter-secrets", "secrets.yaml", "consumer_endpoint")
	requireRenderedSecretContains(t, secrets, "sops-age-key", "age-key.txt", "AGE-SECRET-KEY-1LOCAL")
	requireRenderedSecretContains(t, secrets, "keycloak-secrets", "keycloak.conf", "bootstrap-admin-password")
	requireRenderedSecretContains(t, secrets, "noebs-tls", "tls.crt", "BEGIN CERTIFICATE")
	requireRenderedSecretContains(t, secrets, "noebs-tls", "tls.key", "BEGIN RSA PRIVATE KEY")
	if secretByName(t, secrets, "noebs-tls").Type != "kubernetes.io/tls" {
		t.Fatalf("noebs-tls type = %q, want kubernetes.io/tls", secretByName(t, secrets, "noebs-tls").Type)
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
	root := t.TempDir()
	writePreflightFile(t, root, ".sops/age-key.txt", "AGE-SECRET-KEY-1LOCAL\n")
	configMapData := decodeKubernetesNoebsConfigMapData(t)
	writePreflightFile(t, root, "config.yaml", configMapData["config.yaml"])

	for _, source := range kubernetesServiceSecretSources {
		configKey := source.serviceName + ".service.yaml"
		payload := configMapData[configKey]
		if payload == "" {
			t.Fatalf("noebs-config missing %s", configKey)
		}
		writePreflightFile(t, root, filepath.Join("services", source.serviceName+".yaml"), payload)
	}

	writePreflightFile(t, root, "platform/postgres-password.txt", "postgres-password\n")
	writePreflightFile(t, root, "platform/temporal-postgres-password.txt", "temporal-postgres-password\n")
	writePreflightFile(t, root, "platform/keycloak-postgres-password.txt", "keycloak-postgres-password\n")
	writePreflightFile(t, root, "platform/keycloak.conf", `http-enabled=true
http-port=8080
hostname-strict=false
proxy-headers=xforwarded
health-enabled=true
metrics-enabled=true

db=postgres
db-url=jdbc:postgresql://keycloak-postgres:5432/keycloak
db-username=keycloak
db-password=keycloak-postgres-password

bootstrap-admin-username=admin
bootstrap-admin-password=admin-password
`)
	for fileName, payload := range kubernetesSecretTestPayloads() {
		writePreflightFile(t, root, filepath.Join("secrets", fileName), payload)
	}
	return root
}

func kubernetesSecretTestPayloads() map[string]string {
	return map[string]string{
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
  sms_gateway: "https://sms.example"
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
}

func serviceDatabaseSecret(serviceName string) string {
	databaseName := strings.ReplaceAll(serviceName, "-", "_")
	return `noebs:
  default_tenant_id: tenant_1
  service_databases:
    ` + serviceName + `: "postgres://noebs:service-password@postgres:5432/` + databaseName + `?sslmode=disable"
`
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
