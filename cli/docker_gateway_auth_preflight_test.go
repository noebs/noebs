package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateDeploymentRootRequiresEveryDatabaseRolePassword(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	path := filepath.Join(root, "deploy", "docker", "postgres", "service-role-passwords.env")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "Docker Postgres role password catalog") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want missing runtime role password rejection", err)
	}
}

func TestValidateDeploymentRootRejectsNonCanonicalDatabaseRolePassword(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	setDockerRolePassword(t, root, "workload_auth_cleanup", "not-a-canonical-secret")

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "canonical base64url encoding of 32 bytes") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want non-canonical role password rejection", err)
	}
}

func TestValidateDeploymentRootRejectsSplicedDatabaseRolePassword(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	setDockerRolePassword(t, root, "gateway_auth_runtime", testCanonicalReleaseSecret(9))

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "database URL must exactly bind role") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want role URL/password coherence rejection", err)
	}
}

func TestValidateDeploymentRootRejectsPasswordReusedAcrossDatabaseFamilies(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	setDockerRolePassword(t, root, "gateway_auth_cleanup", testCanonicalReleaseSecret(1))

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "must use globally distinct passwords") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want reused role password rejection", err)
	}
}

func TestValidateDeploymentRootRejectsNonCanonicalDockerDatabaseURL(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	setNoebsSecretField(t, filepath.Join(root, "deploy", "docker", "secrets", "gateway-auth-migrate.secrets.yaml"), "service_databases", map[string]interface{}{
		"api-gateway": "postgres://gateway_auth_migrate:" + testCanonicalReleaseSecret(4) + "@postgres:5432/gateway_auth?sslmode=verify-full",
	})

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "database URL must exactly bind role") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want exact Docker database endpoint rejection", err)
	}
}

func TestValidateDeploymentRootRejectsWorkloadRuntimeURLSplice(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	path := filepath.Join(root, "deploy", "docker", "secrets", "wallet-api.secrets.yaml")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]interface{}
	if err := yaml.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	getMap(getMap(document, "noebs"), "workload_auth")["nonce_db_url"] = dockerDatabaseURL(
		"workload_auth_runtime", testCanonicalReleaseSecret(9), "workload_auth",
	)
	payload, err = yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	err = validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "wallet-api workload nonce database URL must exactly bind role") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want workload runtime URL coherence rejection", err)
	}
}

func TestValidateDeploymentRootRejectsWrongDockerPostgresIdentity(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	for _, fileName := range []string{"tls.crt", "tls.key"} {
		payload, err := os.ReadFile(filepath.Join(root, "deploy", "docker", "keycloak", fileName))
		if err != nil {
			t.Fatal(err)
		}
		writePreflightFile(t, root, filepath.Join("deploy", "docker", "postgres", fileName), string(payload))
	}

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), `certificate identity "db"`) {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want exact db certificate identity rejection", err)
	}
}

func TestValidateDeploymentRootRequiresDockerPostgresTLSInputs(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	if err := os.Remove(filepath.Join(root, "deploy", "docker", "postgres", "tls.key")); err != nil {
		t.Fatal(err)
	}

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "Noebs Postgres TLS private key") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want missing Postgres TLS input rejection", err)
	}
}

func TestValidateDeploymentRootRejectsDatabaseCAMismatch(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	setNoebsSecretField(t, filepath.Join(root, "deploy", "docker", "secrets", "api-gateway.secrets.yaml"), "database_ca_certificate", "different-ca")

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "api-gateway database CA does not match") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want database CA coherence rejection", err)
	}
}

func TestValidateDeploymentRootRejectsInternalTransportCAMismatch(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	path := filepath.Join(root, "deploy", "docker", "secrets", "api-gateway.secrets.yaml")
	document := readYAMLMapFileForTest(t, path)
	getMap(getMap(document, "noebs"), "internal_transport")["ca_certificate"] = "different-ca"
	writeYAMLMapFileForTest(t, path, document)

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "api-gateway internal transport CA does not match") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want internal transport CA rejection", err)
	}
}

func TestValidateDeploymentRootRejectsSplicedInternalTransportIdentity(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	apiGatewayPath := filepath.Join(root, "deploy", "docker", "secrets", "api-gateway.secrets.yaml")
	cardVaultPath := filepath.Join(root, "deploy", "docker", "secrets", "card-vault.secrets.yaml")
	apiGateway := readYAMLMapFileForTest(t, apiGatewayPath)
	cardVault := readYAMLMapFileForTest(t, cardVaultPath)
	getMap(apiGateway, "noebs")["internal_transport"] = getMap(getMap(cardVault, "noebs"), "internal_transport")
	writeYAMLMapFileForTest(t, apiGatewayPath, apiGateway)

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "validate api-gateway internal transport identity") {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want exact service identity rejection", err)
	}
}

func readYAMLMapFileForTest(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]interface{}
	if err := yaml.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func writeYAMLMapFileForTest(t *testing.T, path string, document map[string]interface{}) {
	t.Helper()
	payload, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setDockerRolePassword(t *testing.T, root, role, password string) {
	t.Helper()
	path := filepath.Join(root, "deploy", "docker", "postgres", "service-role-passwords.env")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(payload), "\n"), "\n")
	found := false
	for index, line := range lines {
		if strings.HasPrefix(line, role+"=") {
			lines[index] = role + "=" + password
			found = true
		}
	}
	if !found {
		t.Fatalf("role %s not found in Docker password catalog", role)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
