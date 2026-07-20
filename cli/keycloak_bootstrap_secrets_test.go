package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adonese/noebs/internal/keycloakadmin"
)

func TestRenderKeycloakBootstrapSecretsDerivesSteadyConfiguration(t *testing.T) {
	releaseRoot := writeKubernetesSecretReleaseRoot(t)
	inputPath := filepath.Join(t.TempDir(), "bootstrap.secrets.yaml")
	bootstrapSecret := testCanonicalReleaseSecret(10)
	writePreflightFile(t, filepath.Dir(inputPath), filepath.Base(inputPath), "api_version: "+keycloakBootstrapInputAPIVersion+"\nclient_secret: "+bootstrapSecret+"\n")
	outputPath := filepath.Join(t.TempDir(), "rendered", "bootstrap.yaml")

	if err := renderKeycloakBootstrapSecrets(releaseRoot, "noebs", inputPath, outputPath, readPlainPreflightSecret); err != nil {
		t.Fatalf("renderKeycloakBootstrapSecrets() error = %v", err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("output mode = %o, want 600", info.Mode().Perm())
	}
	payload, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	secrets := decodeRenderedKubernetesSecrets(t, payload)
	if len(secrets) != 2 {
		t.Fatalf("rendered %d secrets, want 2", len(secrets))
	}
	requireRenderedSecretValue(t, secrets, "keycloak-bootstrap-admin", "client-secret", bootstrapSecret)
	configPayload := secretByName(t, secrets, "keycloak-bootstrap-reconciler-credentials").StringData["config.yaml"]
	config, err := keycloakadmin.LoadConfig(strings.NewReader(configPayload))
	if err != nil {
		t.Fatalf("parse bootstrap reconciler config: %v", err)
	}
	steady, err := readSteadyKeycloakReconcilerConfig(filepath.Join(releaseRoot, "platform", "keycloak-reconciler-config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if config.AdminRealm != "master" || config.ClientID != keycloakadmin.BootstrapClientID || config.ClientSecret != bootstrapSecret {
		t.Fatalf("bootstrap authority = %#v", config)
	}
	if config.BaseURL != steady.BaseURL ||
		config.ClientCredentials["noebs-backoffice"] != steady.ClientCredentials["noebs-backoffice"] ||
		config.ClientCredentials["noebs-wallet-authorizer"] != steady.ClientCredentials["noebs-wallet-authorizer"] ||
		config.IdentityProviders["google"] != steady.IdentityProviders["google"] {
		t.Fatal("bootstrap reconciler config diverged from steady release inputs")
	}
}

func TestRenderKeycloakBootstrapSecretsRefusesOverwrite(t *testing.T) {
	releaseRoot := writeKubernetesSecretReleaseRoot(t)
	inputPath := filepath.Join(t.TempDir(), "bootstrap.secrets.yaml")
	writePreflightFile(t, filepath.Dir(inputPath), filepath.Base(inputPath), "api_version: "+keycloakBootstrapInputAPIVersion+"\nclient_secret: "+testCanonicalReleaseSecret(10)+"\n")
	outputPath := filepath.Join(t.TempDir(), "bootstrap.yaml")
	if err := os.WriteFile(outputPath, []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := renderKeycloakBootstrapSecrets(releaseRoot, "noebs", inputPath, outputPath, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "file exists") {
		t.Fatalf("renderKeycloakBootstrapSecrets() error = %v, want exclusive-create rejection", err)
	}
	payload, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "existing\n" {
		t.Fatal("existing output was modified")
	}
}

func TestRenderKeycloakBootstrapSecretsRejectsNonCanonicalSecret(t *testing.T) {
	releaseRoot := writeKubernetesSecretReleaseRoot(t)
	inputPath := filepath.Join(t.TempDir(), "bootstrap.secrets.yaml")
	writePreflightFile(t, filepath.Dir(inputPath), filepath.Base(inputPath), "api_version: "+keycloakBootstrapInputAPIVersion+"\nclient_secret: short\n")
	outputPath := filepath.Join(t.TempDir(), "bootstrap.yaml")

	err := renderKeycloakBootstrapSecrets(releaseRoot, "noebs", inputPath, outputPath, readPlainPreflightSecret)
	if err == nil || !strings.Contains(err.Error(), "canonical base64url") {
		t.Fatalf("renderKeycloakBootstrapSecrets() error = %v, want canonical secret rejection", err)
	}
	assertPathMissing(t, outputPath)
}
