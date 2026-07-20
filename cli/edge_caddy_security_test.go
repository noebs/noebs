package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEdgeCaddyRedactsOIDCQueryCredentials(t *testing.T) {
	caddyfile := readEdgeSecurityFile(t, "Caddyfile")
	_, api, found := strings.Cut(caddyfile, "api.noebs.sd {")
	if !found {
		t.Fatal("cannot isolate api.noebs.sd Caddy configuration")
	}
	api, _, found = strings.Cut(api, "\n}\n\nrd.adonese.sd {")
	if !found {
		t.Fatal("cannot find end of api.noebs.sd Caddy configuration")
	}

	for _, contract := range []string{
		"format filter {",
		"request>uri query {",
		"wrap json",
	} {
		if !strings.Contains(api, contract) {
			t.Errorf("api.noebs.sd access log missing %q", contract)
		}
	}
	for _, parameter := range []string{
		"code",
		"state",
		"session_state",
		"session_code",
		"client_data",
		"id_token_hint",
		"nonce",
		"code_challenge",
		"tab_id",
		"execution",
	} {
		if !strings.Contains(api, "replace "+parameter+" REDACTED") {
			t.Errorf("api.noebs.sd access log does not redact %q", parameter)
		}
	}
}

func TestEdgeCaddyRunsAsFixedNonRootIdentity(t *testing.T) {
	deployment := readEdgeSecurityFile(t, "deployment.yaml")
	for _, contract := range []string{
		"runAsNonRoot: true",
		"runAsUser: 10001",
		"runAsGroup: 10001",
		"fsGroup: 10001",
		"fsGroupChangePolicy: OnRootMismatch",
		"drop: [ALL]",
		"add: [NET_BIND_SERVICE]",
	} {
		if !strings.Contains(deployment, contract) {
			t.Errorf("edge deployment missing %q", contract)
		}
	}
	if strings.Count(deployment, "              add:") != 1 {
		t.Error("NET_BIND_SERVICE must be Caddy's only added capability")
	}

	runbook := readEdgeSecurityFile(t, "README.md")
	for _, path := range []string{
		"/var/lib/docker/volumes/noebs_caddy_data/_data",
		"/var/lib/docker/volumes/noebs_caddy_config/_data",
	} {
		if !strings.Contains(runbook, path) {
			t.Errorf("edge adoption runbook missing %q", path)
		}
	}
	const ownershipCommand = "sudo chown -R -- 10001:10001 /var/lib/docker/volumes/noebs_caddy_data/_data /var/lib/docker/volumes/noebs_caddy_config/_data"
	if !strings.Contains(runbook, ownershipCommand) {
		t.Error("edge adoption runbook must transfer retained volume ownership to Caddy")
	}
}

func readEdgeSecurityFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "deploy", "kubernetes", "edge", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
