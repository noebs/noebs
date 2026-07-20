package walletgrpc

import (
	"strings"
	"testing"
)

func TestWalletWorkflowIDsAreDomainSeparatedBoundedAndUnambiguous(t *testing.T) {
	constructors := []struct {
		domain string
		id     func(string, string) string
	}{
		{domain: "p2p", id: p2pWorkflowID},
		{domain: "deposit", id: depositWorkflowID},
		{domain: "withdrawal", id: withdrawalWorkflowID},
		{domain: "manual", id: manualTransferWorkflowID},
	}
	seen := make(map[string]string, len(constructors))
	for _, constructor := range constructors {
		t.Run(constructor.domain, func(t *testing.T) {
			first := constructor.id("tenant-a", "command-1")
			if first != constructor.id("tenant-a", "command-1") {
				t.Fatal("workflow ID is not deterministic")
			}
			if first == constructor.id("tenant-a", "command-2") {
				t.Fatal("different command keys produced one workflow ID")
			}
			if first == constructor.id("tenant-b", "command-1") {
				t.Fatal("different tenants produced one workflow ID")
			}
			if constructor.id("a-b", "c") == constructor.id("a", "b-c") {
				t.Fatal("tenant/key delimiter collision")
			}
			if got, want := len(constructor.id("tenant", strings.Repeat("x", 4096))), len("wallet--")+len(constructor.domain)+64; got != want {
				t.Fatalf("workflow ID length = %d, want %d", got, want)
			}
			if otherDomain, exists := seen[first]; exists {
				t.Fatalf("workflow ID collides with %s domain", otherDomain)
			}
			seen[first] = constructor.domain
		})
	}
}
