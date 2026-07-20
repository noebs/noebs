package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemporalPostgresReleaseIsTLS13Only(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	var bootstrap string
	var temporalConfig map[string]string
	for _, object := range objects {
		switch {
		case object.Kind == "ConfigMap" && object.Metadata.Name == "temporal-postgres-bootstrap":
			bootstrap = object.Data["start.sh"]
		case object.Kind == "ConfigMap" && object.Metadata.Name == "temporal-config":
			temporalConfig = object.Data
		}
	}
	if bootstrap == "" || temporalConfig == nil {
		t.Fatal("Temporal Postgres TLS configuration is missing")
	}
	for _, required := range []string{
		"hostssl all all all scram-sha-256",
		"hostnossl all all all reject",
		"ssl=on",
		"ssl_min_protocol_version=TLSv1.3",
		"ssl_max_protocol_version=TLSv1.3",
		"ssl_cert_file=$tls_certificate_runtime",
		"ssl_key_file=$tls_private_key_runtime",
	} {
		if !strings.Contains(bootstrap, required) {
			t.Fatalf("Temporal Postgres bootstrap missing %q", required)
		}
	}
	for _, forbidden := range []string{"host all all all scram-sha-256", "ssl=off"} {
		if strings.Contains(bootstrap, forbidden) {
			t.Fatalf("Temporal Postgres bootstrap contains fallback %q", forbidden)
		}
	}

	config := temporalConfig["temporal.yaml"]
	for _, required := range []string{
		"tls:\n          enabled: true",
		"caFile: /opt/temporal/secrets/postgres-ca.pem",
		"enableHostVerification: true",
		"serverName: temporal-postgres",
	} {
		if strings.Count(config, required) != 2 {
			t.Fatalf("Temporal persistence config count(%q) = %d, want 2", required, strings.Count(config, required))
		}
	}
	for _, required := range []string{
		"--tls=true",
		"--tls-disable-host-verification=false",
		"--tls-ca-file \"$postgres_ca\"",
		"--tls-server-name \"$postgres_host\"",
	} {
		if strings.Count(temporalConfig["schema-migrate.sh"], required) != 4 {
			t.Fatalf("Temporal schema migration count(%q) = %d, want 4", required, strings.Count(temporalConfig["schema-migrate.sh"], required))
		}
	}

	dockerBootstrap, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "temporal", "postgres-start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap != string(dockerBootstrap) {
		t.Fatal("Kubernetes and Docker Temporal Postgres bootstrap scripts differ")
	}
}
