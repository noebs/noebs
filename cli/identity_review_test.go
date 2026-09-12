package main

import (
	"bytes"
	"github.com/adonese/noebs/store"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIdentityDecisionCannotFallBackToDirectDatabase(t *testing.T) {
	root := t.TempDir()
	reason := filepath.Join(root, "reason.txt")
	if err := os.WriteFile(reason, []byte("Evidence matches the supplied claims"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--action", "decide", "--config", filepath.Join(root, "missing-config"), "--secrets", "missing", "--tenant", "tenant", "--reviewer", "operator", "--session", "11111111-1111-4111-8111-111111111111", "--user-id", "1", "--revision", "1", "--operation-id", "11111111-1111-4111-8111-111111111112", "--decision", "approved", "--reason-file", reason, "--policy", "test-v1", "--evidence-reviewed"}
	var output bytes.Buffer
	err := runIdentityReview(args, &output, func(string) (*store.DB, error) {
		t.Fatal("decision opened database instead of Temporal")
		return nil, nil
	})
	if err == nil || output.Len() != 0 {
		t.Fatalf("missing Temporal configuration accepted: %v", err)
	}
}

func TestIdentityReviewCommandRequiresExplicitScopeAndDecisionTerms(t *testing.T) {
	base := []string{"--secrets", "/app/secrets.yaml", "--tenant", "tenant-mojaloop", "--reviewer", "reviewer@example.test"}
	for _, args := range [][]string{
		nil,
		append(append([]string{}, base...), "--action", "decide"),
		append(append([]string{}, base...), "--action", "queue", "--limit", "101"),
		append(append([]string{}, base...), "--action", "queue", "--offset", "-1"),
		append(append([]string{}, base...), "--action", "case", "--session", "11111111-1111-4111-8111-111111111111", "--user-id", "5"),
		append(append([]string{}, base...), "--action", "case", "--session", "11111111-1111-4111-8111-111111111111", "--user-id", "5", "--output", "/tmp/case.json"),
	} {
		if _, err := parseIdentityReviewOptions(args); err == nil {
			t.Fatalf("accepted missing/invalid review authority: %v", args)
		}
	}
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = []string{"noebs", "identity-review"}
	if !isConfigUtilityCommand() {
		t.Fatal("operator review tried to initialize unrelated service runtimes")
	}
	if _, err := parseIdentityReviewOptions(append(base, "--action", "queue")); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityReviewExportsArePrivateExclusiveAndConfined(t *testing.T) {
	root := t.TempDir()
	outputPath := filepath.Join(root, ".state", "identity-review", "case.json")
	file, err := createIdentityReviewOutput(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString("private fixture"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	info, err := os.Stat(outputPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("review file mode=%v,%v", info, err)
	}
	if _, err := createIdentityReviewOutput(outputPath); err == nil {
		t.Fatal("review export overwrote an existing file")
	}
	if _, err := createIdentityReviewOutput(filepath.Join(root, "public.json")); err == nil {
		t.Fatal("export allowed outside .state")
	}
	if _, err := createIdentityReviewOutput(filepath.Join(root, ".state", "..", "escaped.json")); err == nil {
		t.Fatal("export escaped .state through traversal")
	}
	target := filepath.Join(root, "public")
	if err = os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".state", "link")
	if err = os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := createIdentityReviewOutput(filepath.Join(link, "evidence.jpg")); err == nil {
		t.Fatal("export followed a directory symlink")
	}
}

func TestIdentityReviewRejectsSecretsWithoutOpeningWrongDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.yaml")
	for _, payload := range []string{
		"noebs:\n  service_databases:\n    wallet-ledger: postgres://wallet_ledger_runtime:x@localhost/wallet_ledger?sslmode=disable\n",
		"noebs:\n  service_databases:\n    identity-auth: postgres://identity_auth_migrate:x@localhost/identity_auth?sslmode=disable\n",
		"noebs:\n  service_databases:\n    identity-auth: postgres://identity_auth_runtime:x@localhost/identity_auth?sslmode=disable\n",
	} {
		if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := openIdentityReviewDatabase(path); err == nil {
			t.Fatal("review accepted wrong database authority or missing TLS")
		}
	}
}

func TestIdentityReviewParsingDoesNotPrintReasonOrEvidence(t *testing.T) {
	var output bytes.Buffer
	err := runIdentityReview([]string{"--action", "decide", "--secrets", "missing", "--tenant", "tenant", "--reviewer", "operator", "--session", "11111111-1111-4111-8111-111111111111", "--user-id", "1", "--revision", "1", "--operation-id", "11111111-1111-4111-8111-111111111112", "--decision", "approved", "--reason-file", "missing", "--policy", "test-v1", "--evidence-reviewed"}, &output, nil)
	if err == nil || output.Len() != 0 || strings.Contains(err.Error(), "postgres:") {
		t.Fatalf("failed review emitted private content: %s,%v", output.String(), err)
	}
}

func TestIdentityReviewReadsActualRuntimeSecretContract(t *testing.T) {
	inputs := newTestInternalTransportInputs(t, time.Now().UTC())
	databaseURL := "postgres://identity_auth_runtime:fixture@postgres/identity_auth?sslmode=verify-full"
	payload, err := yaml.Marshal(map[string]any{"noebs": map[string]any{"service_databases": map[string]string{"identity-auth": databaseURL}, "database_ca_certificate": inputs.CACertificate, "default_tenant_id": "tenant-mojaloop"}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "identity-auth.secrets.yaml")
	if err = os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	gotURL, gotCA, err := identityReviewDatabaseConfig(path)
	if err != nil || gotURL != databaseURL || gotCA != inputs.CACertificate {
		t.Fatalf("runtime secret contract not loaded: %v", err)
	}
}
