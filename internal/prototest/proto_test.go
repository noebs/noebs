package prototest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBufLint(t *testing.T) {
	if _, err := exec.LookPath("buf"); err != nil {
		t.Skip("buf not installed")
	}
	cmd := exec.Command("buf", "lint")
	cmd.Dir = findRepoRoot(t)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("buf lint failed: %v\n%s", err, output)
	}
}

func TestBufGenerate(t *testing.T) {
	if _, err := exec.LookPath("buf"); err != nil {
		t.Skip("buf not installed")
	}
	if _, err := exec.LookPath("protoc-gen-go"); err != nil {
		t.Skip("protoc-gen-go not installed")
	}
	root := findRepoRoot(t)
	outDir := t.TempDir()
	templatePath := filepath.Join(outDir, "buf.gen.yaml")
	template := fmt.Sprintf("version: v2\nplugins:\n  - local: protoc-gen-go\n    out: %s\n    opt: paths=source_relative\n", filepath.Join(outDir, "proto"))
	if err := os.WriteFile(templatePath, []byte(template), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	cmd := exec.Command("buf", "generate", "--template", templatePath)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("buf generate failed: %v\n%s", err, output)
	}

	generated := filepath.Join(outDir, "proto", "noebs", "wallet", "v1", "wallet.pb.go")
	if _, err := os.Stat(generated); err != nil {
		t.Fatalf("expected generated file %s: %v", generated, err)
	}
}

func TestGeneratedHTTPArtifactsExcludeUnboundWalletTransactions(t *testing.T) {
	root := findRepoRoot(t)
	for _, path := range []string{
		filepath.Join(root, "gen", "openapi", "noebs.swagger.json"),
		filepath.Join(root, "gen", "proto", "noebs", "wallet", "v1", "wallet.pb.gw.go"),
	} {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			`"/wallet/p2p"`,
			`"/wallet/withdrawals"`,
			`WalletPublicService_RequestP2PTransfer`,
			`WalletPublicService_RequestWithdrawal`,
		} {
			if strings.Contains(string(payload), forbidden) {
				t.Fatalf("%s exposes unbound wallet transaction RPC %q", path, forbidden)
			}
		}
	}
}

func findRepoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "buf.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("buf.yaml not found from %s", dir)
		}
		dir = parent
	}
}
