package handler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWalletAdminHTTPSurfaceUsesGRPCBridgeOnly(t *testing.T) {
	forbidden := []string{
		"type AdminHandler",
		"func NewAdminHandler",
		"func RegisterAdminRoutes",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list wallet handler files: %v", err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(data), token) {
				t.Fatalf("%s contains %q; wallet admin HTTP must go through GRPCAdminHandler only", path, token)
			}
		}
	}
}
