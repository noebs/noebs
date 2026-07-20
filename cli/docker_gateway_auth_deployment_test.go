package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDockerComposeOwnsGatewayAuthenticationLifecycle(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))

	requireComposeDependency(t, "gateway-auth-migrate", compose.Services["gateway-auth-migrate"], "db", "service_healthy")
	requireComposeDependency(t, "api-gateway", compose.Services["api-gateway"], "gateway-auth-migrate", "service_completed_successfully")
	requireComposeDependency(t, "gateway-auth-cleanup", compose.Services["gateway-auth-cleanup"], "gateway-auth-migrate", "service_completed_successfully")

	db, ok := compose.Services["db"]
	if !ok {
		t.Fatal("docker-compose.yml missing db service")
	}
	requireComposeTopLevelSecret(t, compose.Secrets, "service-role-passwords", "./deploy/docker/postgres/service-role-passwords.env")
	requireComposeSecret(t, "db", db.Secrets, "service-role-passwords", "service-role-passwords")
	for secretName, input := range map[string]struct {
		file   string
		target string
	}{
		"noebs_postgres_transport_ca_certificate": {file: "ca.pem", target: "/opt/noebs-postgres/secrets/ca.pem"},
		"noebs_postgres_tls_certificate":          {file: "tls.crt", target: "/opt/noebs-postgres/secrets/tls.crt"},
		"noebs_postgres_tls_private_key":          {file: "tls.key", target: "/opt/noebs-postgres/secrets/tls.key"},
	} {
		requireComposeTopLevelSecret(t, compose.Secrets, secretName, "./deploy/docker/postgres/"+input.file)
		requireComposeSecret(t, "db", db.Secrets, secretName, input.target)
	}

	for _, serviceName := range []string{"gateway-auth-migrate", "gateway-auth-cleanup"} {
		secretName := serviceName + "-secrets"
		requireComposeTopLevelSecret(t, compose.Secrets, secretName, "./deploy/docker/secrets/"+serviceName+".secrets.yaml")
		requireComposeSecret(t, serviceName, compose.Services[serviceName].Secrets, secretName, "/app/secrets.yaml")
	}
}

func TestDockerDatabaseRoleAndTLSInputsAreExplicitAndIgnored(t *testing.T) {
	gitignore, err := os.ReadFile(filepath.Join("..", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	roleCatalogPath := "/deploy/docker/postgres/service-role-passwords.env"
	if !strings.Contains(string(gitignore), roleCatalogPath) {
		t.Fatalf(".gitignore missing %s", roleCatalogPath)
	}
	for _, fileName := range []string{"ca.pem", "tls.crt", "tls.key"} {
		path := "/deploy/docker/postgres/" + fileName
		if !strings.Contains(string(gitignore), path) {
			t.Fatalf(".gitignore missing %s", path)
		}
		examplePath := filepath.Join("..", strings.TrimPrefix(path, "/")+".example")
		payload, err := os.ReadFile(examplePath)
		if err != nil {
			t.Fatalf("read %s: %v", examplePath, err)
		}
		if value := strings.TrimSpace(string(payload)); !strings.HasPrefix(value, "REPLACE_WITH_NOEBS_POSTGRES_") {
			t.Fatalf("%s does not carry an explicit Noebs Postgres TLS placeholder", examplePath)
		}
	}

	for _, serviceName := range []string{"gateway-auth-migrate", "gateway-auth-cleanup"} {
		path := filepath.Join("..", "deploy", "docker", "secrets", serviceName+".secrets.yaml.example")
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var example serviceSecretExample
		if err := yaml.Unmarshal(payload, &example); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		requirePlaceholderStrings(t, path, example.Noebs)
		requireServiceDatabaseOwners(t, path, example.Noebs, []string{"api-gateway"})
	}
}

func TestDockerComposeServiceSecretsHaveExplicitExamples(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	expectedOwners := map[string][]string{
		"api-gateway":               {"api-gateway"},
		"identity-auth":             {"identity-auth"},
		"card-vault":                {"card-vault"},
		"ebs-adapter":               {"ebs-adapter"},
		"ebs-adapter-events":        {"ebs-adapter"},
		"psp-webhook":               {"wallet-ledger"},
		"admin-reporting":           {"admin-reporting"},
		"admin-reporting-projector": {"admin-reporting"},
		"notification-chat":         {"notification-chat"},
		"wallet-api":                nil,
		"wallet-ledger":             {"wallet-ledger"},
		"wallet-worker":             {"wallet-ledger"},
		"workload-auth-migrate":     {"workload-auth-migrate"},
		"workload-auth-cleanup":     {"workload-auth-migrate"},
		"gateway-auth-migrate":      {"api-gateway"},
		"gateway-auth-cleanup":      {"api-gateway"},
		"identity-auth-migrate":     {"identity-auth"},
		"card-vault-migrate":        {"card-vault"},
		"ebs-adapter-migrate":       {"ebs-adapter"},
		"admin-reporting-migrate":   {"admin-reporting"},
		"notification-chat-migrate": {"notification-chat"},
		"wallet-ledger-migrate":     {"wallet-ledger"},
	}
	seenExamples := make(map[string]bool, len(expectedOwners))
	for secretName, secret := range compose.Secrets {
		if !strings.HasPrefix(secret.File, "./deploy/docker/secrets/") {
			continue
		}
		examplePath := filepath.Join("..", strings.TrimPrefix(secret.File, "./")+".example")
		payload, err := os.ReadFile(examplePath)
		if err != nil {
			t.Fatalf("%s missing checked-in example %s: %v", secretName, examplePath, err)
		}
		var example serviceSecretExample
		if err := yaml.Unmarshal(payload, &example); err != nil {
			t.Fatalf("parse %s: %v", examplePath, err)
		}
		if example.Noebs == nil {
			t.Fatalf("%s missing noebs", examplePath)
		}
		serviceName := strings.TrimSuffix(filepath.Base(secret.File), ".secrets.yaml")
		owners, ok := expectedOwners[serviceName]
		if !ok {
			t.Fatalf("%s has no explicit example contract", serviceName)
		}
		seenExamples[serviceName] = true
		requireServiceDatabaseOwners(t, examplePath, example.Noebs, owners)
		role, err := parseServiceRole(serviceName)
		if err != nil {
			t.Fatal(err)
		}
		transport, hasTransport := example.Noebs["internal_transport"].(map[string]any)
		if roleUsesInternalTransportIdentity(role) {
			if !hasTransport {
				t.Fatalf("%s missing internal_transport", examplePath)
			}
			for _, key := range []string{"ca_certificate", "certificate", "private_key"} {
				if !strings.HasPrefix(firstString(transport, key), "REPLACE_WITH_") {
					t.Fatalf("%s internal_transport.%s must be an explicit placeholder", examplePath, key)
				}
			}
		} else if hasTransport {
			t.Fatalf("%s must not define internal_transport", examplePath)
		}
		if role.opensDatabase() || roleReceivesSignedHTTP(role) {
			if ca := firstString(example.Noebs, "database_ca_certificate"); !strings.HasPrefix(ca, "REPLACE_WITH_") {
				t.Fatalf("%s database_ca_certificate = %q, want explicit placeholder", examplePath, ca)
			}
		}
		if role.opensDatabase() {
			for _, rawURL := range getMap(example.Noebs, "service_databases") {
				username, database := dockerExampleDatabaseIdentity(t, serviceName)
				requireDockerExampleDatabaseURL(t, examplePath, rawURL, username, database)
			}
		}
		if roleReceivesSignedHTTP(role) {
			requireDockerExampleWorkloadReceiver(t, examplePath, role, getMap(example.Noebs, "workload_auth"))
		}
		if len(workloadCallerAudiences(role)) > 0 {
			workload := getMap(example.Noebs, "workload_auth")
			if !strings.HasPrefix(firstString(workload, "signing_key_id"), "REPLACE_WITH_") ||
				!strings.HasPrefix(firstString(workload, "signing_private_key"), "REPLACE_WITH_") {
				t.Fatalf("%s missing workload signing-key placeholders", examplePath)
			}
		}
		requirePlaceholderStrings(t, examplePath, example.Noebs)
	}
	if len(seenExamples) != len(expectedOwners) {
		t.Fatalf("Docker service secret examples = %v, want all %v", seenExamples, expectedOwners)
	}
	examplePaths, err := filepath.Glob(filepath.Join("..", "deploy", "docker", "secrets", "*.secrets.yaml.example"))
	if err != nil {
		t.Fatal(err)
	}
	if len(examplePaths) != len(expectedOwners) {
		t.Fatalf("checked-in Docker service secret examples = %d, want exactly %d Compose references", len(examplePaths), len(expectedOwners))
	}
	for _, examplePath := range examplePaths {
		serviceName := strings.TrimSuffix(filepath.Base(examplePath), ".secrets.yaml.example")
		if !seenExamples[serviceName] {
			t.Fatalf("%s is not referenced by Docker Compose", examplePath)
		}
	}
}

func TestDockerComposeUsesOnePinnedMutualTLSBoundary(t *testing.T) {
	config := decodeMountedNoebsConfigFile(t, filepath.Join("..", "config.docker.yaml"))
	for role, endpoint := range config.Noebs.ServiceDiscovery {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			t.Fatalf("parse service_discovery.%s: %v", role, err)
		}
		if parsed.Scheme != "https" {
			t.Fatalf("service_discovery.%s = %q, want HTTPS", role, endpoint)
		}
	}

	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	for _, role := range internalTransportServiceRoles() {
		if !role.startsHTTP() {
			continue
		}
		serviceName := string(role)
		service, ok := compose.Services[serviceName]
		if !ok {
			t.Fatalf("docker-compose.yml missing %s", serviceName)
		}
		requireComposeHealthcheck(t, serviceName, service.Healthcheck, []string{"CMD", "/usr/local/bin/noebs", "internal-healthcheck"})
	}

	for serviceName, image := range map[string]string{
		"kafka":                   "apache/kafka@sha256:4ceccc577f03f51f6af8dbfda55194d0d892f4fa7913ffbded567ce3895622ed",
		"kafka-topics":            "apache/kafka@sha256:4ceccc577f03f51f6af8dbfda55194d0d892f4fa7913ffbded567ce3895622ed",
		"temporal-schema-migrate": "temporalio/auto-setup@sha256:f14912b699cf73015ad5c4fc18d522d4b014db90e794039214dfb7c022c2644f",
		"temporal":                "temporalio/auto-setup@sha256:f14912b699cf73015ad5c4fc18d522d4b014db90e794039214dfb7c022c2644f",
	} {
		if got := compose.Services[serviceName].Image; got != image {
			t.Fatalf("%s image = %q, want immutable %q", serviceName, got, image)
		}
	}
}

func TestDockerCleanupRolesRunEveryFiveMinutes(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	for _, serviceName := range []string{"workload-auth-cleanup", "gateway-auth-cleanup"} {
		service := compose.Services[serviceName]
		if service.Restart != "unless-stopped" {
			t.Fatalf("%s restart = %q, want unless-stopped", serviceName, service.Restart)
		}
		if len(service.Entrypoint) != 2 || service.Entrypoint[1] != "/opt/noebs/recurring-cleanup.sh" {
			t.Fatalf("%s entrypoint = %v, want recurring cleanup script", serviceName, service.Entrypoint)
		}
		requireComposeVolume(t, serviceName, service.Volumes, "./deploy/docker/recurring-cleanup.sh", "/opt/noebs/recurring-cleanup.sh")
	}
	payload, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "recurring-cleanup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "interval_seconds=300") || !strings.Contains(string(payload), `sleep "$interval_seconds"`) {
		t.Fatal("recurring cleanup script must use an explicit five-minute interval")
	}
}

func TestDockerPostgresHasNoPlaintextTransportMode(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "postgres", "postgres-start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(payload)
	for _, required := range []string{
		`install -o postgres -g postgres -m 0600 /dev/null "$pgdata/pg_hba.conf"`,
		`echo "hostssl $database $role all scram-sha-256"`,
		`hostnossl all all all reject`,
		`host all all all reject`,
		`ssl=on`,
		`ssl_min_protocol_version=TLSv1.3`,
		`ssl_max_protocol_version=TLSv1.3`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("Postgres start script missing %q", required)
		}
	}
	requireExactPostgresHBABindings(t, script)
	for _, forbidden := range []string{`tls_enabled=false`, `echo "host all all all scram-sha-256"`} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("Postgres start script retains plaintext fallback %q", forbidden)
		}
	}
}

func requireDockerExampleDatabaseURL(t *testing.T, path string, raw any, username, database string) {
	t.Helper()
	value, ok := raw.(string)
	if !ok {
		t.Fatalf("%s database URL = %#v, want string", path, raw)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("parse %s database URL: %v", path, err)
	}
	password, passwordPresent := parsed.User.Password()
	if parsed.Scheme != "postgres" || parsed.Host != "db:5432" || parsed.Path != "/"+database ||
		parsed.RawQuery != "sslmode=verify-full" || parsed.User.Username() != username ||
		!passwordPresent || !strings.HasPrefix(password, "REPLACE_WITH_") {
		t.Fatalf("%s database URL = %q, want exact TLS Docker %s/%s template", path, value, username, database)
	}
}

func dockerExampleDatabaseIdentity(t *testing.T, serviceName string) (string, string) {
	t.Helper()
	role, err := parseServiceRole(serviceName)
	if err != nil {
		t.Fatalf("parse %s service role: %v", serviceName, err)
	}
	spec, present := postgresRoleSpecForService(role)
	if !present {
		t.Fatalf("%s has no Postgres role catalog entry", serviceName)
	}
	return spec.username, spec.database
}

func requireDockerExampleWorkloadReceiver(t *testing.T, path string, role serviceRole, workload map[string]interface{}) {
	t.Helper()
	if workload == nil {
		t.Fatalf("%s missing workload_auth receiver config", path)
	}
	requireDockerExampleDatabaseURL(t, path+" workload nonce", workload["nonce_db_url"], "workload_auth_runtime", "workload_auth")
	trusted := getMap(workload, "trusted_keys")
	expected := expectedWorkloadCallers(role)
	seen := make(map[string]bool, len(trusted))
	for _, raw := range trusted {
		key := raw.(map[string]interface{})
		caller := firstString(key, "caller")
		if !expected[caller] || !strings.HasPrefix(firstString(key, "public_key"), "REPLACE_WITH_") {
			t.Fatalf("%s carries invalid workload trusted key for %q", path, caller)
		}
		seen[caller] = true
	}
	if len(seen) != len(expected) {
		t.Fatalf("%s workload callers = %v, want %v", path, seen, expected)
	}
}

func requireComposeDependency(t *testing.T, serviceName string, service composeService, dependency, condition string) {
	t.Helper()
	configured, ok := service.DependsOn[dependency]
	if !ok || configured.Condition != condition {
		t.Fatalf("%s depends_on[%s] = %#v, want condition %s", serviceName, dependency, configured, condition)
	}
}
