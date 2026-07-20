package main

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adonese/noebs/store"
	"gopkg.in/yaml.v3"
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

func TestValidateDeploymentRootRejectsMissingEBSCredentialInput(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{
		omitEBSIPINKey: true,
	})

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if !errors.Is(err, errMissingEBSConfig) {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want %v", err, errMissingEBSConfig)
	}
}

func TestValidateDeploymentRootRejectsMissingCardVaultDataKey(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{
		omitCardVaultDataKey: true,
	})

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if !errors.Is(err, store.ErrMissingDataKey) {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want %v", err, store.ErrMissingDataKey)
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
  default_tenant_id: tenant-cutover
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

func TestValidateKubernetesDeploymentRootRejectsMissingCardVaultDataKey(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	path := filepath.Join(root, "secrets", "card-vault.secrets.yaml")
	removeNoebsSecretField(t, path, "data_key")

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if !errors.Is(err, store.ErrMissingDataKey) {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want %v", err, store.ErrMissingDataKey)
	}
}

func TestValidateKubernetesDeploymentRootRejectsMissingEBSCredentialInput(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	path := filepath.Join(root, "secrets", "ebs-adapter.secrets.yaml")
	removeNoebsSecretField(t, path, "ipin_key")

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if !errors.Is(err, errMissingEBSConfig) {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want %v", err, errMissingEBSConfig)
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

func TestValidateKubernetesDeploymentRootRejectsInvalidGHCRDockerConfig(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "missing ghcr", payload: `{"auths":{"docker.io":{"auth":"bm9lYnM6dG9rZW4="}}}`, want: "missing auths.ghcr.io"},
		{name: "bad base64", payload: `{"auths":{"ghcr.io":{"auth":"not-base64"}}}`, want: "must be base64 username:token"},
		{name: "missing token", payload: `{"auths":{"ghcr.io":{"auth":"bm9lYnM6"}}}`, want: "must decode to username:token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeKubernetesSecretReleaseRoot(t)
			writePreflightFile(t, root, "platform/ghcr-dockerconfigjson", tt.payload+"\n")

			err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want %q", err, tt.want)
			}
		})
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

func TestValidateKubernetesDeploymentRootRejectsSplicedKeycloakDatabaseCredential(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	path := filepath.Join(root, "platform", "keycloak.conf")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payload = []byte(strings.Replace(string(payload), "db-password=keycloak-postgres-password", "db-password=another-valid-password", 1))
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeKubernetesReleaseManifest(root); err != nil {
		t.Fatal(err)
	}

	err = validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "does not match the release credential") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want credential coherence rejection", err)
	}
}

func TestValidateKubernetesDeploymentRootRejectsSplicedKeycloakPostgresIdentity(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	path := filepath.Join(root, "platform", "internal-transport.secrets.yaml")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var credentials internalTransportPlatformCredentials
	if err := yaml.Unmarshal(payload, &credentials); err != nil {
		t.Fatal(err)
	}
	credentials.KeycloakPostgresCertificate = credentials.PostgresCertificate
	credentials.KeycloakPostgresPrivateKey = credentials.PostgresPrivateKey
	payload, err = yaml.Marshal(credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	err = validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "validate Keycloak Postgres transport identity") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want Keycloak Postgres identity rejection", err)
	}
}

func TestValidateKubernetesDeploymentRootRejectsSplicedBackofficeCredential(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	setNoebsSecretField(t, filepath.Join(root, "secrets", "api-gateway.secrets.yaml"), "backoffice_client_secret", testCanonicalReleaseSecret(9))
	if err := writeKubernetesReleaseManifest(root); err != nil {
		t.Fatal(err)
	}

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "does not match the Keycloak managed credential") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want back-office credential coherence rejection", err)
	}
}

func TestValidateKubernetesDeploymentRootRejectsSplicedGatewayRoleCredential(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	setNoebsSecretField(t, filepath.Join(root, "secrets", "api-gateway.secrets.yaml"), "service_databases", map[string]interface{}{
		"api-gateway": gatewayAuthDatabaseURL("gateway_auth_runtime", testCanonicalReleaseSecret(9)),
	})
	if err := writeKubernetesReleaseManifest(root); err != nil {
		t.Fatal(err)
	}

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "does not match its release role credential") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want gateway role credential coherence rejection", err)
	}
}

func TestValidateKubernetesDeploymentRootRejectsTenantOutsideCatalog(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	setNoebsSecretField(t, filepath.Join(root, "secrets", "wallet-api.secrets.yaml"), "default_tenant_id", "tenant-unknown")
	if err := writeKubernetesReleaseManifest(root); err != nil {
		t.Fatal(err)
	}

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "unknown tenant") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want catalog membership rejection", err)
	}
}

func TestValidateKubernetesDeploymentRootConstructsBackofficeOIDCRuntime(t *testing.T) {
	root := writeKubernetesSecretReleaseRoot(t)
	path := filepath.Join(root, "config.yaml")
	config := readYAMLMapFileMust(t, path)
	getMap(config, "noebs")["backoffice_redirect_url"] = "https://api.noebs.sd/wrong-callback"
	payload, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeKubernetesReleaseManifest(root); err != nil {
		t.Fatal(err)
	}

	err = validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "back-office OIDC runtime") {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want constructed OIDC runtime rejection", err)
	}
}

type preflightRootOptions struct {
	defaultTenantID         string
	omitEBSConsumerEndpoint bool
	omitEBSIPINKey          bool
	omitCardVaultDataKey    bool
	keycloakPassword        string
}

func writePreflightRoot(t *testing.T, opts preflightRootOptions) string {
	t.Helper()
	root := t.TempDir()
	defaultTenantID := opts.defaultTenantID
	if defaultTenantID == "" {
		defaultTenantID = "tenant-cutover"
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
		var document map[string]interface{}
		if err := yaml.Unmarshal([]byte(payload), &document); err != nil {
			t.Fatalf("parse %s fixture: %v", fileName, err)
		}
		noebs := getMap(document, "noebs")
		noebs["default_tenant_id"] = defaultTenantID
		if fileName == "ebs-adapter.secrets.yaml" && opts.omitEBSConsumerEndpoint {
			delete(noebs, "consumer_endpoint")
		}
		if fileName == "ebs-adapter.secrets.yaml" && opts.omitEBSIPINKey {
			delete(noebs, "ipin_key")
		}
		if fileName == "card-vault.secrets.yaml" && opts.omitCardVaultDataKey {
			delete(noebs, "data_key")
		}
		encoded, err := yaml.Marshal(document)
		if err != nil {
			t.Fatalf("marshal %s fixture: %v", fileName, err)
		}
		writePreflightFile(t, root, filepath.Join("deploy", "docker", "secrets", fileName), string(encoded))
	}
	writePreflightFile(t, root, "deploy/docker/postgres/bootstrap.secrets.yaml", `noebs:
  db_url: "postgres://noebs:postgres-password@db:5432/noebs?sslmode=disable"
`)
	writePreflightFile(t, root, "deploy/docker/temporal/postgres-password.txt", "temporal-postgres-password\n")
	writePreflightFile(t, root, "deploy/docker/keycloak/postgres-password.txt", keycloakPassword+"\n")
	now := time.Now().UTC()
	transport, err := prepareInternalTransportRelease(newTestInternalTransportInputs(t, now), rand.Reader, now)
	if err != nil {
		t.Fatalf("prepare Keycloak Postgres transport identity: %v", err)
	}
	keycloakPostgres := transport.platform["keycloak-postgres"]
	keycloak := transport.platform["keycloak"]
	writePreflightFile(t, root, "deploy/docker/keycloak/ca.pem", transport.caCertificate)
	writePreflightFile(t, root, "deploy/docker/keycloak/postgres-tls.crt", keycloakPostgres.Certificate)
	writePreflightFile(t, root, "deploy/docker/keycloak/postgres-tls.key", keycloakPostgres.PrivateKey)
	writePreflightFile(t, root, "deploy/docker/keycloak/tls.crt", keycloak.Certificate)
	writePreflightFile(t, root, "deploy/docker/keycloak/tls.key", keycloak.PrivateKey)
	writePreflightFile(t, root, "deploy/docker/keycloak/keycloak.conf", `http-enabled=false
http-relative-path=/auth
https-port=8443
https-certificate-file=/opt/keycloak/conf/tls.crt
https-certificate-key-file=/opt/keycloak/conf/tls.key
https-protocols=TLSv1.3
http-management-scheme=http
http-management-port=9000
http-management-relative-path=/
hostname-strict=false
proxy-headers=xforwarded
health-enabled=true
metrics-enabled=true

db=postgres
db-url=jdbc:postgresql://keycloak-postgres:5432/keycloak
db-username=keycloak
db-password=`+keycloakPassword+`
db-tls-mode=verify-server
db-tls-trust-store-file=/opt/keycloak/conf/db-ca.pem
`)
	return root
}

func removeNoebsSecretField(t *testing.T, path, field string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document map[string]interface{}
	if err := yaml.Unmarshal(payload, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	delete(getMap(document, "noebs"), field)
	payload, err = yaml.Marshal(document)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setNoebsSecretField(t *testing.T, path, field string, value interface{}) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document map[string]interface{}
	if err := yaml.Unmarshal(payload, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	getMap(document, "noebs")[field] = value
	payload, err = yaml.Marshal(document)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
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
