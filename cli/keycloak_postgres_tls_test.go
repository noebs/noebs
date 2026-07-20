package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeycloakPostgresReleaseIsTLS13Only(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	var postgres, keycloak manifestContainer
	var bootstrap string
	for _, object := range objects {
		switch {
		case object.Kind == "ConfigMap" && object.Metadata.Name == "keycloak-postgres-bootstrap":
			bootstrap = object.Data["start.sh"]
		case object.Kind == "StatefulSet" && object.Metadata.Name == "keycloak-postgres":
			postgres = object.Spec.Template.Spec.Containers[0]
		case object.Kind == "Deployment" && object.Metadata.Name == "keycloak":
			keycloak = object.Spec.Template.Spec.Containers[0]
		}
	}
	if bootstrap == "" || postgres.Name == "" || keycloak.Name == "" {
		t.Fatal("Keycloak or its Postgres TLS deployment boundary is missing")
	}
	for _, required := range []string{
		"hostssl all all all scram-sha-256",
		"hostnossl all all all reject",
		`ssl=on`,
		`ssl_min_protocol_version=TLSv1.3`,
		`ssl_max_protocol_version=TLSv1.3`,
		`ssl_cert_file=$tls_certificate_runtime`,
		`ssl_key_file=$tls_private_key_runtime`,
	} {
		if !strings.Contains(bootstrap, required) {
			t.Fatalf("Keycloak Postgres bootstrap missing %q", required)
		}
	}
	for _, forbidden := range []string{"host all all all", "tls_enabled", "ssl=off"} {
		if strings.Contains(bootstrap, forbidden) {
			t.Fatalf("Keycloak Postgres bootstrap contains fallback %q", forbidden)
		}
	}
	requireMount(t, "keycloak-postgres", postgres, "/opt/keycloak-postgres/secrets/tls.crt", "tls.crt")
	requireMount(t, "keycloak-postgres", postgres, "/opt/keycloak-postgres/secrets/tls.key", "tls.key")
	requireMount(t, "keycloak", keycloak, "/opt/keycloak/conf/db-ca.pem", "db-ca.pem")

	dockerBootstrap, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "keycloak", "postgres-start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap != string(dockerBootstrap) {
		t.Fatal("Kubernetes and Docker Keycloak Postgres bootstrap scripts differ")
	}
}

func TestKeycloakUsesOfficialDatabaseServerVerification(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "deploy", "kubernetes", "base", "keycloak.conf.example"),
		filepath.Join("..", "deploy", "docker", "keycloak", "keycloak.conf.example"),
	} {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		config := string(payload)
		for _, required := range []string{
			"db-url=jdbc:postgresql://keycloak-postgres:5432/keycloak\n",
			"db-tls-mode=verify-server\n",
			"db-tls-trust-store-file=/opt/keycloak/conf/db-ca.pem\n",
		} {
			if !strings.Contains(config, required) {
				t.Fatalf("%s missing %q", path, required)
			}
		}
		for _, forbidden := range []string{"sslmode=disable", "sslmode=require", "trustServerCertificate=true"} {
			if strings.Contains(config, forbidden) {
				t.Fatalf("%s contains database TLS fallback %q", path, forbidden)
			}
		}
	}
}

func TestKeycloakPostgresComposeUsesExplicitTLSFiles(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	const postgresImage = "postgres@sha256:33f923b05f64ca54ac4401c01126a6b92afe839a0aa0a52bc5aeb5cc958e5f20"
	for _, service := range []string{"db", "temporal-postgres", "keycloak-postgres"} {
		if compose.Services[service].Image != postgresImage {
			t.Fatalf("%s image = %q, want tested digest %q", service, compose.Services[service].Image, postgresImage)
		}
	}
	postgres := compose.Services["keycloak-postgres"]
	keycloak := compose.Services["keycloak"]
	requireComposeSecret(t, "keycloak-postgres", postgres.Secrets, "keycloak_postgres_tls_certificate", "/opt/keycloak-postgres/secrets/tls.crt")
	requireComposeSecret(t, "keycloak-postgres", postgres.Secrets, "keycloak_postgres_tls_private_key", "/opt/keycloak-postgres/secrets/tls.key")
	requireComposeSecret(t, "keycloak", keycloak.Secrets, "keycloak_transport_ca_certificate", "/opt/keycloak/conf/db-ca.pem")
	requireComposeTopLevelSecret(t, compose.Secrets, "keycloak_postgres_tls_certificate", "./deploy/docker/keycloak/postgres-tls.crt")
	requireComposeTopLevelSecret(t, compose.Secrets, "keycloak_postgres_tls_private_key", "./deploy/docker/keycloak/postgres-tls.key")
	requireComposeTopLevelSecret(t, compose.Secrets, "keycloak_transport_ca_certificate", "./deploy/docker/keycloak/ca.pem")
}
