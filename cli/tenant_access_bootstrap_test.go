package main

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestBootstrapRequiresExplicitCanonicalAuthorityInputs(t *testing.T) {
	base := []string{"--config", "config", "--service", "service", "--secrets", "runtime", "--database-secrets", "migrate", "--tenant-catalog", "catalog", "--tenant", "noebs", "--operation-id", "22222222-2222-4222-8222-222222222222", "--reason-file", "reason"}
	if _, err := parseAccessBootstrapOptions(base); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--config", "--service", "--secrets", "--database-secrets", "--tenant-catalog", "--tenant", "--operation-id", "--reason-file"} {
		t.Run(flag, func(t *testing.T) {
			args := append([]string{}, base...)
			for i, v := range args {
				if v == flag {
					args[i+1] = ""
				}
			}
			if _, err := parseAccessBootstrapOptions(args); err == nil {
				t.Fatal("accepted absent input")
			}
		})
	}
	for _, id := range []string{"not-uuid", "00000000-0000-0000-0000-000000000000", "22222222222242228222222222222222"} {
		if _, err := parseAccessBootstrapOptions(append(base, "--expected-subject", id)); err == nil {
			t.Fatal("accepted noncanonical expected subject")
		}
	}
	previous := os.Args
	defer func() { os.Args = previous }()
	os.Args = []string{"noebs", "bootstrap-tenant-admin"}
	if !isConfigUtilityCommand() {
		t.Fatal("bootstrap starts ordinary application initialization")
	}
}
func TestLegacyMembershipApplyRejectsBeforeReadingCredentials(t *testing.T) {
	_, _, _, err := runAssignKeycloakMemberships([]string{"--memberships", "absent", "--desired-state", "absent", "--tenant-catalog", "absent", "--config", "absent", "--ca", "absent"}, http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "writes are retired") {
		t.Fatalf("legacy apply=%v", err)
	}
}
