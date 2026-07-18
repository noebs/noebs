package prototest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
	template := fmt.Sprintf("version: v1\nplugins:\n  - plugin: go\n    out: %s\n    opt: paths=source_relative\n", outDir)
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
