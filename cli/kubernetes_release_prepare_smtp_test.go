package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/adonese/noebs/internal/keycloakadmin"
	"gopkg.in/yaml.v3"
)

func TestPrepareKubernetesReleaseScopesSMTPEgress(t *testing.T) {
	for _, test := range []struct {
		name string
		host string
		cidr string
		port int
	}{
		{name: "tailnet IPv4", host: "100.101.102.103", cidr: "100.101.102.103/32", port: 465},
		{name: "IPv6", host: "fd7a:115c:a1e0::1", cidr: "fd7a:115c:a1e0::1/128", port: 465},
		{name: "DNS", host: "smtp.example.test", port: 465},
		{name: "explicit alternative port", host: "100.101.102.103", cidr: "100.101.102.103/32", port: 2465},
		{name: "disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inputRoot := t.TempDir()
			inputs := newTestKubernetesReleaseInputs(t, "noebs")
			source := ".."
			if test.host == "" {
				inputs.Noebs.Keycloak.SMTP = nil
				source = releaseSourceWithoutAccountEmail(t)
			} else {
				inputs.Noebs.Keycloak.SMTP.Host = test.host
				inputs.Noebs.Keycloak.SMTP.Port = test.port
			}
			inputsPath := writeKubernetesReleaseInputs(t, inputRoot, inputs)
			root := filepath.Join(t.TempDir(), "release")
			if err := prepareKubernetesRelease(source, inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), root, readPlainPreflightSecret, plainKubernetesSecretEncrypt); err != nil {
				t.Fatal(err)
			}
			policy := readYAMLMapFileMust(t, filepath.Join(root, keycloakSMTPEgressArtifact))
			if policy["apiVersion"] != "networking.k8s.io/v1" || policy["kind"] != "NetworkPolicy" {
				t.Fatalf("unexpected policy type: %v", policy)
			}
			if metadata := getMap(policy, "metadata"); metadata["namespace"] != "noebs" || metadata["name"] != "keycloak-smtp-egress" {
				t.Fatalf("unexpected policy identity: %v", metadata)
			}
			spec := getMap(policy, "spec")
			selector := getMap(spec, "podSelector")
			if len(selector) != 1 || !reflect.DeepEqual(getMap(selector, "matchLabels"), map[string]any{"app.kubernetes.io/name": "keycloak"}) || !reflect.DeepEqual(spec["policyTypes"], []any{"Egress"}) {
				t.Fatalf("SMTP policy must select only Keycloak egress: %v", spec)
			}
			egress, ok := spec["egress"].([]any)
			if !ok {
				t.Fatal("SMTP policy must contain an explicit egress array")
			}
			if test.host == "" {
				if len(egress) != 0 {
					t.Fatal("disabled SMTP retained an egress permission")
				}
			} else {
				if len(egress) != 1 {
					t.Fatalf("SMTP policy has %d egress rules, want one", len(egress))
				}
				rule := egress[0].(map[string]any)
				if !reflect.DeepEqual(rule["ports"], []any{map[string]any{"protocol": "TCP", "port": test.port}}) {
					t.Fatalf("SMTP policy changed the explicit port: %v", rule)
				}
				if test.cidr == "" {
					if _, exists := rule["to"]; exists {
						t.Fatal("DNS policy unexpectedly resolved an address at build time")
					}
				} else if !reflect.DeepEqual(rule["to"], []any{map[string]any{"ipBlock": map[string]any{"cidr": test.cidr}}}) {
					t.Fatalf("SMTP policy permits a destination other than the explicit endpoint: %v", rule)
				}
			}
			var output bytes.Buffer
			if err := renderKubernetesSecrets(&output, root, "noebs", readPlainPreflightSecret); err != nil {
				t.Fatal(err)
			}
			mounted := secretByName(t, decodeRenderedKubernetesSecrets(t, output.Bytes()), "noebs-release-manifest").StringData["keycloak-smtp-egress.yaml"]
			if mounted != readPreparedFile(t, root, keycloakSMTPEgressArtifact) {
				t.Fatal("mounted SMTP policy differs from the applied release artifact")
			}
		})
	}
}

func TestPrepareKubernetesReleaseRejectsInvalidSMTPBeforePublishing(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*kubernetesReleaseInputs)
	}{
		{name: "missing config", mutate: func(i *kubernetesReleaseInputs) { i.Noebs.Keycloak.SMTP = nil }},
		{name: "missing host", mutate: func(i *kubernetesReleaseInputs) { i.Noebs.Keycloak.SMTP.Host = "" }},
		{name: "missing port", mutate: func(i *kubernetesReleaseInputs) { i.Noebs.Keycloak.SMTP.Port = 0 }},
		{name: "CIDR host", mutate: func(i *kubernetesReleaseInputs) { i.Noebs.Keycloak.SMTP.Host = "100.64.0.0/10" }},
		{name: "missing password", mutate: func(i *kubernetesReleaseInputs) { i.Noebs.Keycloak.SMTP.Password = "" }},
		{name: "plaintext transport", mutate: func(i *kubernetesReleaseInputs) { i.Noebs.Keycloak.SMTP.TLS = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			inputRoot := t.TempDir()
			inputs := newTestKubernetesReleaseInputs(t, "noebs")
			test.mutate(&inputs)
			inputsPath := writeKubernetesReleaseInputs(t, inputRoot, inputs)
			root := filepath.Join(t.TempDir(), "release")
			calledEncrypt := false
			err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), root, readPlainPreflightSecret, func(string, []byte, string) ([]byte, error) {
				calledEncrypt = true
				return nil, errors.New("encryption must not run for invalid SMTP")
			})
			if !errors.Is(err, keycloakadmin.ErrInvalidConfig) {
				t.Fatalf("invalid SMTP must fail at the configuration boundary: %v", err)
			}
			if calledEncrypt {
				t.Fatal("invalid SMTP reached release publication")
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid SMTP created a release directory: %v", err)
			}
		})
	}
}

func TestKubernetesReleaseRejectsAlteredSMTPEgress(t *testing.T) {
	inputRoot := t.TempDir()
	inputs := newTestKubernetesReleaseInputs(t, "noebs")
	inputs.Noebs.Keycloak.SMTP.Host = "100.101.102.103"
	inputsPath := writeKubernetesReleaseInputs(t, inputRoot, inputs)
	root := filepath.Join(t.TempDir(), "release")
	if err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), root, readPlainPreflightSecret, plainKubernetesSecretEncrypt); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, keycloakSMTPEgressArtifact)
	original := readPreparedFile(t, root, keycloakSMTPEgressArtifact)
	for _, test := range []struct {
		name   string
		change func(string) string
	}{
		{name: "broader address", change: func(s string) string { return strings.ReplaceAll(s, "100.101.102.103/32", "100.64.0.0/10") }},
		{name: "different port", change: func(s string) string { return strings.ReplaceAll(s, "port: 465", "port: 25") }},
		{name: "different workload", change: func(s string) string {
			return strings.ReplaceAll(s, "app.kubernetes.io/name: keycloak", "app.kubernetes.io/name: wallet-worker")
		}},
		{name: "extra document", change: func(s string) string { return s + "---\nkind: NetworkPolicy\n" }},
		{name: "missing artifact"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.change == nil {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(test.change(original)), 0o600); err != nil {
				t.Fatal(err)
			}
			// Rehashing must not turn an inconsistent network permission into
			// a valid release merely because its file digest now matches.
			if err := writeKubernetesReleaseManifest(root); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err := renderKubernetesSecrets(&output, root, "noebs", readPlainPreflightSecret)
			if err == nil || !strings.Contains(err.Error(), "SMTP egress policy") || output.Len() != 0 {
				t.Fatalf("altered SMTP policy was rendered or failed incorrectly: %v", err)
			}
		})
	}
}

func TestKeycloakConfigRequiresSMTPTrustAndHostnameVerification(t *testing.T) {
	preflight := decodeCatalogWorkload(t, "../infra/kubernetes/base/preflight-job.yaml")
	requireSecretFileMount(t, preflight, "release-manifest", "noebs-release-manifest", "/preflight/platform/keycloak-smtp-egress.yaml", "keycloak-smtp-egress.yaml")
	for _, example := range []string{
		"../infra/kubernetes/base/keycloak.conf.example",
		"../deploy/docker/keycloak/keycloak.conf.example",
	} {
		payload, err := os.ReadFile(example)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{"truststore-paths=/opt/keycloak/conf/db-ca.pem\n", "tls-hostname-verifier=DEFAULT\n"} {
			if !strings.Contains(string(payload), required) {
				t.Fatalf("%s lacks %s", example, required)
			}
		}
	}
	root := writeKubernetesSecretReleaseRoot(t)
	original := readPreparedFile(t, root, "platform/keycloak.conf")
	for _, replacement := range []struct{ old, new string }{
		{"truststore-paths=/opt/keycloak/conf/db-ca.pem\n", ""},
		{"truststore-paths=/opt/keycloak/conf/db-ca.pem", "truststore-paths=/tmp/other-ca.pem"},
		{"tls-hostname-verifier=DEFAULT", "tls-hostname-verifier=ANY"},
	} {
		path := filepath.Join(t.TempDir(), "keycloak.conf")
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(original, replacement.old, replacement.new)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateKeycloakConfig(path, true); err == nil {
			t.Fatalf("accepted unsafe SMTP trust change %q", replacement.new)
		}
	}
}

func releaseSourceWithoutAccountEmail(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	for _, relative := range []string{
		"infra/kubernetes/base/configmap.yaml",
		"infra/kubernetes/keycloak-authority/tenant-catalog.yaml",
		"infra/kubernetes/keycloak-authority/keycloak-desired-state.yaml",
		"deploy/docker/postgres/001-service-databases.sql",
	} {
		payload, err := os.ReadFile(filepath.Join("..", relative))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(relative, "keycloak-desired-state.yaml") {
			var state keycloakadmin.DesiredState
			if err := yaml.Unmarshal(payload, &state); err != nil {
				t.Fatal(err)
			}
			state.Authentication.LocalAccounts.VerifyEmail = false
			state.Authentication.LocalAccounts.ResetPasswordAllowed = false
			payload, err = yaml.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
		}
		writePreflightFile(t, source, relative, string(payload))
	}
	return source
}
