package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareReleaseUsesExplicitDeploymentOriginAndProxy(t *testing.T) {
	source := t.TempDir()
	for _, relative := range []string{"infra/kubernetes/base/configmap.yaml", "infra/kubernetes/keycloak-authority/tenant-catalog.yaml", "infra/kubernetes/keycloak-authority/keycloak-desired-state.yaml", "deploy/docker/postgres/001-service-databases.sql"} {
		payload, err := os.ReadFile(filepath.Join("..", relative))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(payload), "https://api.noebs.sd", "https://noebs-workers.exe.xyz")
		text = strings.ReplaceAll(text, "10.42.0.1/32", "10.242.1.0/32")
		writePreflightFile(t, source, relative, text)
	}
	inputRoot := t.TempDir()
	inputs := writeKubernetesReleaseInputsFile(t, inputRoot, "tenant-cutover")
	output := filepath.Join(t.TempDir(), "release")
	if err := prepareKubernetesRelease(source, inputs, kubernetesReleaseTestAgeKeyPath(inputRoot), output, readPlainPreflightSecret, plainKubernetesSecretEncrypt); err != nil {
		t.Fatal(err)
	}
	config := readPreparedFile(t, output, "platform/keycloak.conf")
	if !strings.Contains(config, "hostname=https://noebs-workers.exe.xyz/auth\n") || !strings.Contains(config, "proxy-trusted-addresses=10.242.1.0/32\n") {
		t.Fatal("release ignored deployment origin or exact proxy source")
	}
}

func TestKeycloakReleaseRejectsAmbiguousOriginAndProxy(t *testing.T) {
	for _, value := range []string{"", "http://example.test/auth", "https://example.test/", "https://example.test/auth?", "https://user@example.test/auth", "https://*.example.test/auth"} {
		if _, err := keycloakPublicOrigin(value); err == nil {
			t.Fatalf("accepted origin %q", value)
		}
	}
	for _, value := range []string{"", "10.242.0.0/16", "0.0.0.0/32", "10.242.1.0/32,10.242.1.1/32"} {
		if err := validateKeycloakProxyAddress(value); err == nil {
			t.Fatalf("accepted proxy trust %q", value)
		}
	}
}
