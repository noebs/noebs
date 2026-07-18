package ebsipin

import (
	"os"
	"strings"
	"testing"
)

func TestSecretPrintingIPINDependencyStaysRemoved(t *testing.T) {
	unsafeModule := strings.Join([]string{"github.com/noebs", "ipin"}, "/")
	checks := map[string][]string{
		"../../go.mod":                   {unsafeModule},
		"../../go.sum":                   {unsafeModule},
		"../../consumer/bill_service.go": {unsafeModule},
		"../../consumer/ipin_service.go": {unsafeModule},
		"../../consumer/services.go":     {unsafeModule},
		"encrypt.go":                     {"fmt.Print", "log.Print", "os.Stdout", "os.Stderr"},
	}
	for path, forbidden := range checks {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(source), token) {
				t.Fatalf("%s contains unsafe IPIN logging token %q", path, token)
			}
		}
	}
}
