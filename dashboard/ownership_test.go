package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardPackageStaysReadOnly(t *testing.T) {
	forbidden := []string{
		"Create" + "Transaction",
		"Exec" + "Context",
		"INSERT" + " INTO",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dashboard package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(".", entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(data), token) {
				t.Fatalf("%s contains write token %q; admin-reporting must stay read-only", path, token)
			}
		}
	}
}
