package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type manifestObject struct {
	Kind                         string            `yaml:"kind"`
	AutomountServiceAccountToken *bool             `yaml:"automountServiceAccountToken"`
	Data                         map[string]string `yaml:"data"`
	Metadata                     struct {
		Name        string            `yaml:"name"`
		Namespace   string            `yaml:"namespace"`
		Annotations map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
	Spec struct {
		Project string `yaml:"project"`
		Source  struct {
			RepoURL        string `yaml:"repoURL"`
			TargetRevision string `yaml:"targetRevision"`
			Path           string `yaml:"path"`
		} `yaml:"source"`
		Destination struct {
			Server    string `yaml:"server"`
			Namespace string `yaml:"namespace"`
		} `yaml:"destination"`
		TLS        []manifestIngressTLS  `yaml:"tls"`
		Rules      []manifestIngressRule `yaml:"rules"`
		Ports      []manifestServicePort `yaml:"ports"`
		SyncPolicy struct {
			Automated struct {
				Prune    bool `yaml:"prune"`
				SelfHeal bool `yaml:"selfHeal"`
			} `yaml:"automated"`
			SyncOptions []string `yaml:"syncOptions"`
		} `yaml:"syncPolicy"`
		Template struct {
			Spec struct {
				ServiceAccountName           string              `yaml:"serviceAccountName"`
				AutomountServiceAccountToken *bool               `yaml:"automountServiceAccountToken"`
				RestartPolicy                string              `yaml:"restartPolicy"`
				Containers                   []manifestContainer `yaml:"containers"`
				InitContainers               []manifestContainer `yaml:"initContainers"`
				Volumes                      []manifestVolume    `yaml:"volumes"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

type manifestIngressTLS struct {
	SecretName string `yaml:"secretName"`
}

type manifestIngressRule struct {
	Host string `yaml:"host"`
	HTTP struct {
		Paths []manifestIngressPath `yaml:"paths"`
	} `yaml:"http"`
}

type manifestIngressPath struct {
	Path    string `yaml:"path"`
	Backend struct {
		Service struct {
			Name string `yaml:"name"`
			Port struct {
				Name string `yaml:"name"`
			} `yaml:"port"`
		} `yaml:"service"`
	} `yaml:"backend"`
}

type manifestServicePort struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

type manifestContainer struct {
	Name           string           `yaml:"name"`
	Image          string           `yaml:"image"`
	Command        []string         `yaml:"command"`
	Args           []string         `yaml:"args"`
	Env            []map[string]any `yaml:"env"`
	EnvFrom        []map[string]any `yaml:"envFrom"`
	Ports          []map[string]any `yaml:"ports"`
	ReadinessProbe map[string]any   `yaml:"readinessProbe"`
	LivenessProbe  map[string]any   `yaml:"livenessProbe"`
	StartupProbe   map[string]any   `yaml:"startupProbe"`
	VolumeMounts   []manifestMount  `yaml:"volumeMounts"`
}

type manifestMount struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	SubPath   string `yaml:"subPath"`
}

type manifestVolume struct {
	Name   string          `yaml:"name"`
	Secret *manifestSecret `yaml:"secret"`
}

type manifestSecret struct {
	SecretName string `yaml:"secretName"`
}

type composeDocument struct {
	Services map[string]composeService `yaml:"services"`
	Secrets  map[string]composeSecret  `yaml:"secrets"`
}

type composeService struct {
	Environment any             `yaml:"environment"`
	EnvFile     any             `yaml:"env_file"`
	Entrypoint  []string        `yaml:"entrypoint"`
	Ports       []string        `yaml:"ports"`
	Profiles    []string        `yaml:"profiles"`
	Volumes     []string        `yaml:"volumes"`
	Secrets     []composeSecret `yaml:"secrets"`
}

type composeSecret struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
	File   string `yaml:"file"`
}

type mountedNoebsConfig struct {
	Noebs struct {
		DatabaseDriver                             string                `yaml:"db_driver"`
		OtelServiceName                            string                `yaml:"otel_service_name"`
		ServiceDiscovery                           map[string]string     `yaml:"service_discovery"`
		GRPCServiceDiscovery                       map[string]string     `yaml:"grpc_service_discovery"`
		EBSDynamicFees                             mountedEBSDynamicFees `yaml:"ebs_dynamic_fees"`
		TemporalHost                               string                `yaml:"temporal_host"`
		TemporalPort                               string                `yaml:"temporal_port"`
		RenderDBPasswordFile                       string                `yaml:"render_db_password_file"`
		WalletEnabled                              bool                  `yaml:"wallet_enabled"`
		WalletPINRequired                          bool                  `yaml:"wallet_pin_required"`
		Wallet2FAThreshold                         int64                 `yaml:"wallet_2fa_threshold"`
		WalletApprovalThreshold                    int64                 `yaml:"wallet_approval_threshold"`
		WalletDefaultCurrency                      string                `yaml:"wallet_default_currency"`
		WalletHoldExpirySeconds                    int                   `yaml:"wallet_hold_expiry_seconds"`
		WalletApprovalTimeoutSeconds               int                   `yaml:"wallet_approval_timeout_seconds"`
		WalletVerificationTimeoutSeconds           int                   `yaml:"wallet_verification_timeout_seconds"`
		WalletManualTransferApprovalTimeoutSeconds int                   `yaml:"wallet_manual_approval_timeout_seconds"`
		WalletPSPPollerCron                        string                `yaml:"wallet_psp_poller_cron"`
		WalletPSPPollerBatchSize                   int                   `yaml:"wallet_psp_poller_batch_size"`
		WalletPSPPollerIntervalSeconds             int                   `yaml:"wallet_psp_poller_interval_seconds"`
		WalletReconciliationCron                   string                `yaml:"wallet_reconciliation_cron"`
		WalletReconciliationBatchSize              int                   `yaml:"wallet_reconciliation_batch_size"`
		WalletReconciliationLookbackHours          int                   `yaml:"wallet_reconciliation_lookback_hours"`
	} `yaml:"noebs"`
}

type mountedEBSDynamicFees struct {
	CardTransferfees   float32 `yaml:"p2p_fees"`
	CustomFees         float32 `yaml:"custom_fees"`
	SpecialPaymentFees float32 `yaml:"special_payment_fees"`
}

type mountedNoebsServiceConfig struct {
	Noebs struct {
		ServiceRole     string `yaml:"service_role"`
		DatabaseDriver  string `yaml:"db_driver"`
		OtelServiceName string `yaml:"otel_service_name"`
	} `yaml:"noebs"`
}

type terraformServiceCatalogEntry struct {
	Port     int
	Protocol string
}

type terraformDatabaseCatalogEntry struct {
	Database      string
	SecretName    string
	MigrationRole string
	ManagedBy     string
}

type serviceSecretExample struct {
	Noebs map[string]any `yaml:"noebs"`
}

func TestNoebsKubernetesServicesUseMountedConfigFiles(t *testing.T) {
	baseDir := filepath.Join("..", "deploy", "kubernetes", "base")
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("read %s: %v", baseDir, err)
	}

	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(baseDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		decoder := yaml.NewDecoder(bytes.NewReader(data))
		for {
			var object manifestObject
			if err := decoder.Decode(&object); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				t.Fatalf("decode %s: %v", path, err)
			}
			for _, container := range append(object.Spec.Template.Spec.Containers, object.Spec.Template.Spec.InitContainers...) {
				if !strings.Contains(container.Image, "ghcr.io/adonese/noebs") {
					continue
				}
				if object.Kind == "Job" && object.Metadata.Name == "noebs-deployment-preflight" {
					continue
				}
				checked++
				if len(container.Env) != 0 {
					t.Fatalf("%s/%s defines env; noebs service config must be file-mounted", object.Metadata.Name, container.Name)
				}
				if len(container.EnvFrom) != 0 {
					t.Fatalf("%s/%s defines envFrom; noebs service config must be file-mounted", object.Metadata.Name, container.Name)
				}
				requireMount(t, object.Metadata.Name, container, "/app/config.yaml", "config.yaml")
				requireMount(t, object.Metadata.Name, container, "/app/service.yaml", "")
				requireMount(t, object.Metadata.Name, container, "/app/secrets.yaml", "secrets.yaml")
				requireNoebsSecretVolume(t, object.Metadata.Name, container, object.Spec.Template.Spec.Volumes)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no noebs Kubernetes containers were checked")
	}
}

func TestNoebsImageRequiresMountedRuntimeConfig(t *testing.T) {
	entrypoint, err := os.ReadFile(filepath.Join("..", "scripts", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("read entrypoint: %v", err)
	}
	entrypointText := string(entrypoint)
	for _, required := range []string{"/app/config.yaml", "/app/service.yaml", "/app/secrets.yaml", "/app/.sops/age-key.txt"} {
		if !strings.Contains(entrypointText, required) {
			t.Fatalf("entrypoint does not require mounted %s", required)
		}
	}
	for _, rejected := range []string{"litefs", "litestream", "DB_PATH_FILE", "render-config", "|| true"} {
		if strings.Contains(entrypointText, rejected) {
			t.Fatalf("entrypoint carries legacy startup behavior %q", rejected)
		}
	}

	dockerfile, err := os.ReadFile(filepath.Join("..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfileText := string(dockerfile)
	for _, rejected := range []string{"COPY config.yaml /app/config.yaml", "litefs", "litestream", "sqlite3"} {
		if strings.Contains(dockerfileText, rejected) {
			t.Fatalf("Dockerfile carries legacy image runtime behavior %q", rejected)
		}
	}
}

func TestRepositoryDoesNotCarryRootRuntimeConfigOrSecrets(t *testing.T) {
	rootRuntimeFiles := []string{"config.yaml", "secrets.yaml"}
	args := append([]string{"-C", "..", "ls-files", "--"}, rootRuntimeFiles...)
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-files root runtime files: %v\n%s", err, output)
	}
	if tracked := strings.TrimSpace(string(output)); tracked != "" {
		t.Fatalf("root runtime config/secrets must not be tracked contracts:\n%s", tracked)
	}
	gitignore, err := os.ReadFile(filepath.Join("..", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	gitignoreText := string(gitignore)
	for _, path := range rootRuntimeFiles {
		pattern := "/" + path
		if !strings.Contains(gitignoreText, pattern) {
			t.Fatalf(".gitignore must reject local root runtime file %s", pattern)
		}
	}
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readmeText := string(readme)
	for _, rejected := range []string{
		"Building with `go get`",
		"production ready server",
		"Using `go get` method",
		"Sample for secrets.yaml",
		"root `secrets.yaml`",
	} {
		if strings.Contains(readmeText, rejected) {
			t.Fatalf("README.md carries legacy single-binary/root-secret guidance %q", rejected)
		}
	}
}

func TestDockerComposeLocalInputsAreNotTrackedGuesses(t *testing.T) {
	localOnlyInputs := []string{
		"deploy/docker/keycloak/keycloak.conf",
		"deploy/docker/keycloak/postgres-password.txt",
		"deploy/docker/temporal/postgres-password.txt",
		"deploy/docker/postgres/bootstrap.secrets.yaml",
	}

	gitignore, err := os.ReadFile(filepath.Join("..", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	gitignoreText := string(gitignore)
	for _, path := range localOnlyInputs {
		pattern := "/" + path
		if !strings.Contains(gitignoreText, pattern) {
			t.Fatalf(".gitignore missing local-only Docker Compose input %s", pattern)
		}
	}

	args := append([]string{"-C", "..", "ls-files", "--"}, localOnlyInputs...)
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-files local Docker Compose inputs: %v\n%s", err, output)
	}
	trackedExisting := []string{}
	for _, path := range strings.Fields(string(output)) {
		if _, err := os.Stat(filepath.Join("..", path)); err == nil {
			trackedExisting = append(trackedExisting, path)
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
	if len(trackedExisting) != 0 {
		t.Fatalf("local Docker Compose runtime inputs must not be committed guesses:\n%s", strings.Join(trackedExisting, "\n"))
	}
}

func TestDockerComposePostgresBootstrapSecretExampleIsExplicit(t *testing.T) {
	path := filepath.Join("..", "deploy", "docker", "postgres", "bootstrap.secrets.yaml.example")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var example serviceSecretExample
	if err := yaml.Unmarshal(data, &example); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if example.Noebs == nil {
		t.Fatalf("%s missing noebs secret map", path)
	}
	if _, ok := example.Noebs["db_url"]; !ok {
		t.Fatalf("%s missing explicit noebs.db_url placeholder", path)
	}
	if _, ok := example.Noebs["db_path"]; ok {
		t.Fatalf("%s must not carry legacy noebs.db_path", path)
	}
	if _, ok := example.Noebs["service_databases"]; ok {
		t.Fatalf("%s must not carry service database ownership; bootstrap renders only the local Postgres password", path)
	}
	requirePlaceholderStrings(t, path, example.Noebs)
}

func TestDockerComposeSecretExamplesMatchServiceOwnership(t *testing.T) {
	gitignore, err := os.ReadFile(filepath.Join("..", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), "/deploy/docker/secrets/*.secrets.yaml") {
		t.Fatalf(".gitignore must ignore local Docker Compose service secrets")
	}
	output, err := exec.Command("git", "-C", "..", "ls-files", "--", "deploy/docker/secrets/*.secrets.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-files Docker Compose service secrets: %v\n%s", err, output)
	}
	if tracked := strings.TrimSpace(string(output)); tracked != "" {
		t.Fatalf("local Docker Compose service secrets must not be committed:\n%s", tracked)
	}

	expectedDatabaseOwners := map[string][]string{
		"api-gateway":          nil,
		"identity-auth":        {"identity-auth"},
		"card-vault":           {"card-vault"},
		"ebs-adapter":          {"ebs-adapter"},
		"psp-webhook":          {"psp-webhook"},
		"admin-reporting":      {"admin-reporting"},
		"notification-chat":    {"notification-chat"},
		"consumer-beneficiary": {"consumer-beneficiary"},
		"wallet-api":           nil,
		"wallet-ledger":        {"wallet-ledger"},
		"wallet-worker":        {"wallet-ledger"},
	}
	for serviceName, owners := range expectedDatabaseOwners {
		path := filepath.Join("..", "deploy", "docker", "secrets", serviceName+".secrets.yaml.example")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var example serviceSecretExample
		if err := yaml.Unmarshal(data, &example); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if example.Noebs == nil {
			t.Fatalf("%s missing noebs secret map", path)
		}
		if _, ok := example.Noebs["db_url"]; ok {
			t.Fatalf("%s must not set noebs.db_url directly", path)
		}
		requirePlaceholderStrings(t, path, example.Noebs)
		requireServiceDatabaseOwners(t, path, example.Noebs, owners)
		if serviceName == "ebs-adapter" {
			requireEBSAdapterSecrets(t, path, example.Noebs)
		}
	}
}

func TestRepositoryDoesNotCarryDirectVMDeploymentScripts(t *testing.T) {
	scripts, err := filepath.Glob(filepath.Join("..", "scripts", "*.sh"))
	if err != nil {
		t.Fatalf("list scripts: %v", err)
	}
	forbidden := []string{
		"docker compose up",
		"exe.dev",
		"get.docker.com",
		"rsync ",
		"scp ",
		"ssh ",
		"systemctl enable --now docker",
	}
	for _, path := range scripts {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, token := range forbidden {
			if strings.Contains(text, token) {
				t.Fatalf("%s carries direct VM/Docker deployment behavior %q; deployment must go through Kubernetes/k3s and Argo CD", path, token)
			}
		}
	}
}

func TestRepositoryDoesNotCarryLegacySingleHostDeploymentArtifacts(t *testing.T) {
	for _, path := range []string{
		"fly.toml",
		"litefs.yml",
		"litefs.static-lease.yml",
	} {
		if _, err := os.Stat(filepath.Join("..", path)); err == nil {
			t.Fatalf("%s is a legacy single-host deployment artifact; deployment must go through Kubernetes/k3s and Argo CD", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat %s: %v", path, err)
		}
	}

	output, err := exec.Command("git", "-C", "..", "ls-files").CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-files: %v\n%s", err, output)
	}
	forbidden := []string{"fly_" + "consul_url", "/var/lib/" + "litefs", "lite" + "fs/"}
	for _, path := range strings.Fields(string(output)) {
		data, err := os.ReadFile(filepath.Join("..", path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := strings.ToLower(string(data))
		for _, token := range forbidden {
			if strings.Contains(text, token) {
				t.Fatalf("%s carries legacy Fly/LiteFS deployment behavior %q", path, token)
			}
		}
	}
}

func TestKubernetesWorkloadsUseExplicitServiceAccounts(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))

	serviceAccounts := map[string]bool{}
	for _, object := range objects {
		if object.Kind != "ServiceAccount" {
			continue
		}
		if object.Metadata.Name == "" {
			t.Fatalf("ServiceAccount has empty metadata.name")
		}
		if object.AutomountServiceAccountToken == nil || *object.AutomountServiceAccountToken {
			t.Fatalf("ServiceAccount %s must set automountServiceAccountToken: false", object.Metadata.Name)
		}
		serviceAccounts[object.Metadata.Name] = true
	}
	if len(serviceAccounts) == 0 {
		t.Fatalf("no ServiceAccount objects were found")
	}

	checked := 0
	usedServiceAccounts := map[string]bool{}
	for _, object := range objects {
		if !isKubernetesWorkloadKind(object.Kind) {
			continue
		}
		workload := object.Kind + "/" + object.Metadata.Name
		if len(object.Spec.Template.Spec.Containers)+len(object.Spec.Template.Spec.InitContainers) == 0 {
			t.Fatalf("%s has no pod containers", workload)
		}
		checked++

		expectedServiceAccount := expectedServiceAccountForWorkload(t, object)
		serviceAccount := object.Spec.Template.Spec.ServiceAccountName
		if serviceAccount != expectedServiceAccount {
			t.Fatalf("%s serviceAccountName = %q, want %q", workload, serviceAccount, expectedServiceAccount)
		}
		if !serviceAccounts[serviceAccount] {
			t.Fatalf("%s references missing ServiceAccount %q", workload, serviceAccount)
		}
		usedServiceAccounts[serviceAccount] = true
		if object.Spec.Template.Spec.AutomountServiceAccountToken == nil || *object.Spec.Template.Spec.AutomountServiceAccountToken {
			t.Fatalf("%s must set automountServiceAccountToken: false", workload)
		}
	}
	if checked == 0 {
		t.Fatalf("no Kubernetes workloads were checked")
	}
	for serviceAccount := range serviceAccounts {
		if !usedServiceAccounts[serviceAccount] {
			t.Fatalf("ServiceAccount %s is not assigned to a workload", serviceAccount)
		}
	}
}

func TestKubernetesServiceDiscoveryTargetsDeclaredServices(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	services := map[string]map[int]bool{}
	var config mountedNoebsConfig
	var foundConfig bool

	for _, object := range objects {
		switch object.Kind {
		case "Service":
			ports := map[int]bool{}
			for _, port := range object.Spec.Ports {
				ports[port.Port] = true
			}
			services[object.Metadata.Name] = ports
		case "ConfigMap":
			if object.Metadata.Name != "noebs-config" {
				continue
			}
			configData := object.Data["config.yaml"]
			if configData == "" {
				t.Fatalf("noebs-config missing config.yaml")
			}
			if err := yaml.Unmarshal([]byte(configData), &config); err != nil {
				t.Fatalf("parse noebs-config config.yaml: %v", err)
			}
			foundConfig = true
		}
	}
	if !foundConfig {
		t.Fatalf("noebs-config ConfigMap not found")
	}
	if len(config.Noebs.ServiceDiscovery) == 0 {
		t.Fatalf("noebs.service_discovery is empty")
	}
	if config.Noebs.ServiceDiscovery["keycloak"] == "" {
		t.Fatalf("noebs.service_discovery must include keycloak")
	}
	if len(config.Noebs.GRPCServiceDiscovery) == 0 {
		t.Fatalf("noebs.grpc_service_discovery is empty")
	}

	for role, endpoint := range config.Noebs.ServiceDiscovery {
		serviceName, port := parseHTTPDiscoveryEndpoint(t, role, endpoint)
		requireKubernetesServicePort(t, services, serviceName, port)
	}
	for role, endpoint := range config.Noebs.GRPCServiceDiscovery {
		serviceName, port := parseHostPortDiscoveryEndpoint(t, role, endpoint)
		requireKubernetesServicePort(t, services, serviceName, port)
	}
	if config.Noebs.TemporalHost == "" || config.Noebs.TemporalPort == "" {
		t.Fatalf("temporal host/port must be explicit in mounted config")
	}
	temporalPort, err := strconv.Atoi(config.Noebs.TemporalPort)
	if err != nil {
		t.Fatalf("temporal_port = %q: %v", config.Noebs.TemporalPort, err)
	}
	requireKubernetesServicePort(t, services, config.Noebs.TemporalHost, temporalPort)
}

func TestFoundationServiceCatalogMatchesKubernetesDiscovery(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	services := map[string]map[int]bool{}
	var config mountedNoebsConfig
	var foundConfig bool
	for _, object := range objects {
		switch object.Kind {
		case "Service":
			ports := map[int]bool{}
			for _, port := range object.Spec.Ports {
				ports[port.Port] = true
			}
			services[object.Metadata.Name] = ports
		case "ConfigMap":
			if object.Metadata.Name != "noebs-config" {
				continue
			}
			if err := yaml.Unmarshal([]byte(object.Data["config.yaml"]), &config); err != nil {
				t.Fatalf("parse noebs-config config.yaml: %v", err)
			}
			foundConfig = true
		}
	}
	if !foundConfig {
		t.Fatalf("noebs-config ConfigMap not found")
	}

	catalog := parseTerraformServiceCatalog(t, filepath.Join("..", "foundation", "terraform", "locals.tf"))
	for name, entry := range catalog {
		requireKubernetesServicePort(t, services, name, entry.Port)
	}
	for name, ports := range services {
		entry, ok := catalog[name]
		if !ok {
			t.Fatalf("Terraform service catalog missing Kubernetes Service %q", name)
		}
		if !ports[entry.Port] {
			t.Fatalf("Terraform service catalog %s port = %d; Kubernetes ports = %v", name, entry.Port, ports)
		}
	}
	for role, endpoint := range config.Noebs.ServiceDiscovery {
		name, port := parseHTTPDiscoveryEndpoint(t, role, endpoint)
		requireTerraformServiceCatalogEntry(t, catalog, name, port, "http")
	}
	for role, endpoint := range config.Noebs.GRPCServiceDiscovery {
		name, port := parseHostPortDiscoveryEndpoint(t, role, endpoint)
		requireTerraformServiceCatalogEntry(t, catalog, name, port, "grpc")
	}
	temporalPort, err := strconv.Atoi(config.Noebs.TemporalPort)
	if err != nil {
		t.Fatalf("temporal_port = %q: %v", config.Noebs.TemporalPort, err)
	}
	requireTerraformServiceCatalogEntry(t, catalog, config.Noebs.TemporalHost, temporalPort, "grpc")
}

func TestFoundationDatabaseCatalogDeclaresOwnedDatabases(t *testing.T) {
	catalog := parseTerraformDatabaseCatalog(t, filepath.Join("..", "foundation", "terraform", "locals.tf"))
	serviceDatabases := parseNoebsServiceDatabases(t, filepath.Join("..", "deploy", "docker", "postgres", "001-service-databases.sql"))

	for _, database := range serviceDatabases {
		serviceName := strings.ReplaceAll(database, "_", "-")
		requireTerraformDatabaseCatalogEntry(t, catalog, serviceName, terraformDatabaseCatalogEntry{
			Database:      database,
			SecretName:    serviceName + "-secrets",
			MigrationRole: serviceName + "-migrate",
		})
	}
	requireTerraformDatabaseCatalogEntry(t, catalog, "wallet-worker", terraformDatabaseCatalogEntry{
		Database:   "wallet_ledger",
		SecretName: "wallet-worker-secrets",
	})
	requireTerraformDatabaseCatalogEntry(t, catalog, "keycloak", terraformDatabaseCatalogEntry{
		Database:   "keycloak",
		SecretName: "keycloak-secrets",
		ManagedBy:  "keycloak",
	})
	requireTerraformDatabaseCatalogEntry(t, catalog, "temporal", terraformDatabaseCatalogEntry{
		Database:      "temporal",
		SecretName:    "temporal-postgres-credentials",
		MigrationRole: "temporal-schema-migrate",
		ManagedBy:     "temporal",
	})
	requireTerraformDatabaseCatalogEntry(t, catalog, "temporal-visibility", terraformDatabaseCatalogEntry{
		Database:      "temporal_visibility",
		SecretName:    "temporal-postgres-credentials",
		MigrationRole: "temporal-schema-migrate",
		ManagedBy:     "temporal",
	})
}

func TestKeycloakKubernetesDeploymentIsIndependent(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))

	services := map[string]map[int]bool{}
	var foundKeycloakDeployment bool
	var foundKeycloakPostgres bool
	var keycloakPostgresBootstrap string

	for _, object := range objects {
		if object.Kind == "ConfigMap" && object.Metadata.Name == "keycloak-postgres-bootstrap" {
			keycloakPostgresBootstrap = object.Data["start.sh"]
		}
		if object.Kind == "Service" {
			ports := map[int]bool{}
			for _, port := range object.Spec.Ports {
				ports[port.Port] = true
			}
			services[object.Metadata.Name] = ports
		}
		if object.Kind == "StatefulSet" && object.Metadata.Name == "keycloak-postgres" {
			foundKeycloakPostgres = true
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("keycloak-postgres containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			if container.Image != "postgres:16" {
				t.Fatalf("keycloak-postgres image = %q, want postgres:16", container.Image)
			}
			if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
				t.Fatalf("keycloak-postgres must use mounted bootstrap files instead of env/envFrom")
			}
			if !containsString(container.Command, "/opt/keycloak-postgres/bin/start.sh") {
				t.Fatalf("keycloak-postgres command = %v, want mounted start.sh", container.Command)
			}
			requireMount(t, "keycloak-postgres", container, "/opt/keycloak-postgres/bin/start.sh", "start.sh")
			requireMount(t, "keycloak-postgres", container, "/opt/keycloak-postgres/secrets/password", "password")
		}
		if object.Kind != "Deployment" || object.Metadata.Name != "keycloak" {
			continue
		}
		foundKeycloakDeployment = true
		if len(object.Spec.Template.Spec.Containers) != 1 {
			t.Fatalf("keycloak containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
		}
		container := object.Spec.Template.Spec.Containers[0]
		if container.Image != "quay.io/keycloak/keycloak:26.6.1" {
			t.Fatalf("keycloak image = %q, want quay.io/keycloak/keycloak:26.6.1", container.Image)
		}
		if !containsString(container.Args, "start") {
			t.Fatalf("keycloak args = %v, want start", container.Args)
		}
		if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
			t.Fatalf("keycloak must use mounted keycloak.conf instead of env/envFrom")
		}
		requireMount(t, "keycloak", container, "/opt/keycloak/conf/keycloak.conf", "keycloak.conf")
	}

	requireKubernetesServicePort(t, services, "keycloak", 8080)
	requireKubernetesServicePort(t, services, "keycloak", 9000)
	requireKubernetesServicePort(t, services, "keycloak-postgres", 5432)
	if !foundKeycloakDeployment {
		t.Fatalf("keycloak Deployment not found")
	}
	if !foundKeycloakPostgres {
		t.Fatalf("keycloak-postgres StatefulSet not found")
	}
	if keycloakPostgresBootstrap == "" {
		t.Fatalf("keycloak-postgres-bootstrap ConfigMap missing start.sh")
	}
	dockerBootstrap, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "keycloak", "postgres-start.sh"))
	if err != nil {
		t.Fatalf("read docker Keycloak Postgres bootstrap: %v", err)
	}
	if keycloakPostgresBootstrap != string(dockerBootstrap) {
		t.Fatalf("Kubernetes and Docker Keycloak Postgres bootstrap scripts differ")
	}
}

func TestNoebsPostgresKubernetesUsesMountedBootstrapFiles(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))

	var foundPostgres bool
	var bootstrapScript string
	var serviceDatabaseSQL string
	for _, object := range objects {
		if object.Kind == "ConfigMap" && object.Metadata.Name == "postgres-bootstrap" {
			bootstrapScript = object.Data["start.sh"]
			serviceDatabaseSQL = object.Data["001-service-databases.sql"]
		}
		if object.Kind != "StatefulSet" || object.Metadata.Name != "postgres" {
			continue
		}
		foundPostgres = true
		if len(object.Spec.Template.Spec.Containers) != 1 {
			t.Fatalf("postgres containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
		}
		container := object.Spec.Template.Spec.Containers[0]
		if container.Image != "postgres:16" {
			t.Fatalf("postgres image = %q, want postgres:16", container.Image)
		}
		if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
			t.Fatalf("postgres must use mounted bootstrap files instead of env/envFrom")
		}
		if !containsString(container.Command, "/opt/noebs-postgres/bin/start.sh") {
			t.Fatalf("postgres command = %v, want mounted start.sh", container.Command)
		}
		requireMount(t, "postgres", container, "/opt/noebs-postgres/bin/start.sh", "start.sh")
		requireMount(t, "postgres", container, "/opt/noebs-postgres/init/001-service-databases.sql", "001-service-databases.sql")
		requireMount(t, "postgres", container, "/opt/noebs-postgres/secrets/password", "password")
	}
	if !foundPostgres {
		t.Fatalf("postgres StatefulSet not found")
	}
	if bootstrapScript == "" {
		t.Fatalf("postgres-bootstrap ConfigMap missing start.sh")
	}
	if serviceDatabaseSQL == "" {
		t.Fatalf("postgres-bootstrap ConfigMap missing 001-service-databases.sql")
	}
	dockerBootstrap, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "postgres", "postgres-start.sh"))
	if err != nil {
		t.Fatalf("read docker Postgres bootstrap: %v", err)
	}
	if bootstrapScript != string(dockerBootstrap) {
		t.Fatalf("Kubernetes and Docker Noebs Postgres bootstrap scripts differ")
	}
	dockerSQL, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "postgres", "001-service-databases.sql"))
	if err != nil {
		t.Fatalf("read docker Postgres service database SQL: %v", err)
	}
	if serviceDatabaseSQL != string(dockerSQL) {
		t.Fatalf("Kubernetes and Docker Noebs Postgres service database SQL differ")
	}
}

func TestTemporalKubernetesUsesMountedConfigAndSchemaJob(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))

	var foundPostgres bool
	var foundSchemaJob bool
	var foundTemporalFrontendService bool
	var foundTemporal bool
	var foundTemporalUI bool
	var postgresBootstrap string
	var temporalConfig map[string]string
	var temporalUIConfig string

	for _, object := range objects {
		if object.Kind == "ConfigMap" && object.Metadata.Name == "temporal-postgres-bootstrap" {
			postgresBootstrap = object.Data["start.sh"]
		}
		if object.Kind == "ConfigMap" && object.Metadata.Name == "temporal-config" {
			temporalConfig = object.Data
		}
		if object.Kind == "ConfigMap" && object.Metadata.Name == "temporal-ui-config" {
			temporalUIConfig = object.Data["development.yaml"]
		}

		switch {
		case object.Kind == "Service" && object.Metadata.Name == "temporal-frontend":
			foundTemporalFrontendService = true
			for _, port := range []int{7233, 6933, 6934, 6935, 6939} {
				requireManifestServicePort(t, object, port)
			}
		case object.Kind == "StatefulSet" && object.Metadata.Name == "temporal-postgres":
			foundPostgres = true
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("temporal-postgres containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			if container.Image != "postgres:16" {
				t.Fatalf("temporal-postgres image = %q, want postgres:16", container.Image)
			}
			if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
				t.Fatalf("temporal-postgres must use mounted bootstrap files instead of env/envFrom")
			}
			if !containsString(container.Command, "/opt/temporal-postgres/bin/start.sh") {
				t.Fatalf("temporal-postgres command = %v, want mounted start.sh", container.Command)
			}
			requireMount(t, "temporal-postgres", container, "/opt/temporal-postgres/bin/start.sh", "start.sh")
			requireMount(t, "temporal-postgres", container, "/opt/temporal-postgres/secrets/password", "password")
		case object.Kind == "Job" && object.Metadata.Name == "temporal-schema-migrate":
			foundSchemaJob = true
			if object.Metadata.Annotations["argocd.argoproj.io/hook"] != "Sync" {
				t.Fatalf("temporal-schema-migrate hook = %q, want Sync", object.Metadata.Annotations["argocd.argoproj.io/hook"])
			}
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "5" {
				t.Fatalf("temporal-schema-migrate sync-wave = %q, want 5", object.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
			}
			if object.Spec.Template.Spec.ServiceAccountName != "temporal-schema-migrate" {
				t.Fatalf("temporal-schema-migrate serviceAccountName = %q", object.Spec.Template.Spec.ServiceAccountName)
			}
			if object.Spec.Template.Spec.AutomountServiceAccountToken == nil || *object.Spec.Template.Spec.AutomountServiceAccountToken {
				t.Fatalf("temporal-schema-migrate must disable service account token automount")
			}
			if object.Spec.Template.Spec.RestartPolicy != "Never" {
				t.Fatalf("temporal-schema-migrate restartPolicy = %q, want Never", object.Spec.Template.Spec.RestartPolicy)
			}
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("temporal-schema-migrate containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			if container.Image != "temporalio/auto-setup:1.29.1" {
				t.Fatalf("temporal-schema-migrate image = %q", container.Image)
			}
			if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
				t.Fatalf("temporal-schema-migrate must use mounted config/secrets instead of env/envFrom")
			}
			if !containsString(container.Command, "/opt/temporal/bin/schema-migrate.sh") {
				t.Fatalf("temporal-schema-migrate command = %v, want mounted schema migration script", container.Command)
			}
			requireMount(t, "temporal-schema-migrate", container, "/opt/temporal/bin/schema-migrate.sh", "schema-migrate.sh")
			requireMount(t, "temporal-schema-migrate", container, "/opt/temporal/secrets/postgres-password", "password")
		case object.Kind == "Deployment" && object.Metadata.Name == "temporal":
			foundTemporal = true
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "15" {
				t.Fatalf("temporal sync-wave = %q, want 15", object.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
			}
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("temporal containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			if container.Image != "temporalio/auto-setup:1.29.1" {
				t.Fatalf("temporal image = %q", container.Image)
			}
			if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
				t.Fatalf("temporal must use mounted config/secrets instead of env/envFrom")
			}
			if !containsString(container.Command, "/opt/temporal/bin/temporal-start.sh") {
				t.Fatalf("temporal command = %v, want mounted start script", container.Command)
			}
			requireMount(t, "temporal", container, "/opt/temporal/bin/temporal-start.sh", "temporal-start.sh")
			requireMount(t, "temporal", container, "/opt/temporal/config/temporal.yaml", "temporal.yaml")
			requireMount(t, "temporal", container, "/opt/temporal/config/dynamicconfig/docker.yaml", "dynamicconfig.yaml")
			requireMount(t, "temporal", container, "/opt/temporal/secrets/postgres-password", "password")
		case object.Kind == "Deployment" && object.Metadata.Name == "temporal-ui":
			foundTemporalUI = true
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "16" {
				t.Fatalf("temporal-ui sync-wave = %q, want 16", object.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
			}
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("temporal-ui containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			if container.Image != "temporalio/ui:2.34.0" {
				t.Fatalf("temporal-ui image = %q", container.Image)
			}
			if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
				t.Fatalf("temporal-ui must use mounted config instead of env/envFrom")
			}
			if !containsString(container.Command, "/home/ui-server/ui-server") || !containsString(container.Args, "start") {
				t.Fatalf("temporal-ui command/args = %v %v, want ui-server start", container.Command, container.Args)
			}
			requireMount(t, "temporal-ui", container, "/home/ui-server/config/development.yaml", "development.yaml")
		}
	}

	if !foundPostgres {
		t.Fatalf("temporal-postgres StatefulSet not found")
	}
	if !foundSchemaJob {
		t.Fatalf("temporal-schema-migrate Job not found")
	}
	if !foundTemporalFrontendService {
		t.Fatalf("temporal-frontend Service not found")
	}
	if !foundTemporal {
		t.Fatalf("temporal Deployment not found")
	}
	if !foundTemporalUI {
		t.Fatalf("temporal-ui Deployment not found")
	}
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-postgres-bootstrap start.sh", postgresBootstrap, filepath.Join("..", "deploy", "docker", "temporal", "postgres-start.sh"))
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-config temporal.yaml", temporalConfig["temporal.yaml"], filepath.Join("..", "deploy", "docker", "temporal", "temporal.yaml"))
	if !strings.Contains(temporalConfig["temporal.yaml"], "broadcastAddress: temporal-frontend") {
		t.Fatalf("temporal.yaml must carry an explicit broadcast address")
	}
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-config temporal-start.sh", temporalConfig["temporal-start.sh"], filepath.Join("..", "deploy", "docker", "temporal", "temporal-start.sh"))
	requireTemporalStartScriptExplicitInputs(t, temporalConfig["temporal-start.sh"])
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-config schema-migrate.sh", temporalConfig["schema-migrate.sh"], filepath.Join("..", "deploy", "docker", "temporal", "schema-migrate.sh"))
	if _, ok := temporalConfig["dynamicconfig.yaml"]; !ok {
		t.Fatalf("temporal-config missing dynamicconfig.yaml")
	}
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-ui-config development.yaml", temporalUIConfig, filepath.Join("..", "deploy", "docker", "temporal", "ui.yaml"))
}

func TestCurrentHostIngressTargetsOnlyAPIGateway(t *testing.T) {
	objects := decodeManifestObjects(t, filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host", "ingress.yaml"))

	checkedPaths := 0
	for _, object := range objects {
		if object.Kind != "Ingress" {
			continue
		}
		if len(object.Spec.Rules) == 0 {
			t.Fatalf("%s Ingress has no rules", object.Metadata.Name)
		}
		for _, rule := range object.Spec.Rules {
			if len(rule.HTTP.Paths) == 0 {
				t.Fatalf("%s Ingress host %s has no HTTP paths", object.Metadata.Name, rule.Host)
			}
			for _, ingressPath := range rule.HTTP.Paths {
				checkedPaths++
				serviceName := ingressPath.Backend.Service.Name
				if serviceName == "" {
					t.Fatalf("%s Ingress host %s path %s has no backend service", object.Metadata.Name, rule.Host, ingressPath.Path)
				}
				if serviceName != "api-gateway" {
					t.Fatalf("%s Ingress host %s path %s targets %q, want api-gateway", object.Metadata.Name, rule.Host, ingressPath.Path, serviceName)
				}
			}
		}
	}
	if checkedPaths == 0 {
		t.Fatalf("current-host overlay has no Ingress backend paths")
	}
}

func TestNoebsDockerComposeServicesUseMountedConfigFiles(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	if _, ok := compose.Secrets["noebs_secrets"]; ok {
		t.Fatalf("docker-compose.yml must not define shared noebs_secrets for service runtimes")
	}
	secretsInit, ok := compose.Services["secrets-init"]
	if !ok {
		t.Fatalf("docker-compose.yml missing secrets-init service")
	}
	if !containsString(secretsInit.Entrypoint, "render-db-password") {
		t.Fatalf("secrets-init entrypoint = %v, want explicit database password renderer", secretsInit.Entrypoint)
	}
	if containsString(secretsInit.Entrypoint, "render-config") {
		t.Fatalf("secrets-init must not run runtime config validation")
	}
	requireComposeSecret(t, "secrets-init", secretsInit.Secrets, "postgres-bootstrap-secrets", "/app/secrets.yaml")
	requireComposeTopLevelSecret(t, compose.Secrets, "postgres-bootstrap-secrets", "./deploy/docker/postgres/bootstrap.secrets.yaml")

	serviceFiles, err := filepath.Glob(filepath.Join("..", "deploy", "docker", "services", "*.yaml"))
	if err != nil {
		t.Fatalf("list docker service configs: %v", err)
	}
	if len(serviceFiles) == 0 {
		t.Fatalf("no docker service configs found")
	}

	for _, serviceFile := range serviceFiles {
		serviceName := strings.TrimSuffix(filepath.Base(serviceFile), ".yaml")
		service, ok := compose.Services[serviceName]
		if !ok {
			t.Fatalf("docker-compose.yml missing service %q for %s", serviceName, serviceFile)
		}
		if service.Environment != nil {
			t.Fatalf("%s defines environment; noebs service config must be file-mounted", serviceName)
		}
		if service.EnvFile != nil {
			t.Fatalf("%s defines env_file; noebs service config must be file-mounted", serviceName)
		}

		requireComposeVolume(t, serviceName, service.Volumes, "./config.docker.yaml", "/app/config.yaml")
		requireComposeVolume(t, serviceName, service.Volumes, "./deploy/docker/services/"+filepath.Base(serviceFile), "/app/service.yaml")
		secretSource := composeSecretSourceForService(serviceName)
		requireComposeSecret(t, serviceName, service.Secrets, secretSource, "/app/secrets.yaml")
		requireComposeSecret(t, serviceName, service.Secrets, "sops_age_key", "/app/.sops/age-key.txt")
		rejectComposeSecret(t, serviceName, service.Secrets, "postgres-bootstrap-secrets")
		requireComposeTopLevelSecret(t, compose.Secrets, secretSource, "./deploy/docker/secrets/"+strings.TrimSuffix(secretSource, "-secrets")+".secrets.yaml")
	}
}

func TestNoebsServiceIdentityConfigBelongsToServiceConfigs(t *testing.T) {
	dockerConfig := decodeMountedNoebsConfigFile(t, filepath.Join("..", "config.docker.yaml"))
	if dockerConfig.Noebs.DatabaseDriver != "" {
		t.Fatalf("config.docker.yaml must not define shared noebs.db_driver; got %q", dockerConfig.Noebs.DatabaseDriver)
	}
	if dockerConfig.Noebs.OtelServiceName != "" {
		t.Fatalf("config.docker.yaml must not define shared noebs.otel_service_name; got %q", dockerConfig.Noebs.OtelServiceName)
	}
	kubernetesConfig := decodeKubernetesBaseNoebsConfig(t)
	if kubernetesConfig.Noebs.DatabaseDriver != "" {
		t.Fatalf("Kubernetes noebs-config config.yaml must not define shared noebs.db_driver; got %q", kubernetesConfig.Noebs.DatabaseDriver)
	}
	if kubernetesConfig.Noebs.OtelServiceName != "" {
		t.Fatalf("Kubernetes noebs-config config.yaml must not define shared noebs.otel_service_name; got %q", kubernetesConfig.Noebs.OtelServiceName)
	}

	serviceFiles, err := filepath.Glob(filepath.Join("..", "deploy", "docker", "services", "*.yaml"))
	if err != nil {
		t.Fatalf("list docker service configs: %v", err)
	}
	for _, serviceFile := range serviceFiles {
		requireServiceIdentityConfig(t, serviceFile, decodeNoebsServiceConfigFile(t, serviceFile))
	}

	configMapData := decodeKubernetesNoebsConfigMapData(t)
	checked := 0
	for key, payload := range configMapData {
		if !strings.HasSuffix(key, ".service.yaml") {
			continue
		}
		checked++
		requireServiceIdentityConfig(t, "noebs-config/"+key, decodeNoebsServiceConfigBytes(t, "noebs-config/"+key, []byte(payload)))
	}
	if checked == 0 {
		t.Fatalf("Kubernetes noebs-config contains no service configs")
	}
}

func TestNoebsPostgresDockerComposeUsesMountedBootstrapFiles(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	config := decodeMountedNoebsConfigFile(t, filepath.Join("..", "config.docker.yaml"))

	db, ok := compose.Services["db"]
	if !ok {
		t.Fatalf("docker-compose.yml missing db service")
	}
	if db.Environment != nil {
		t.Fatalf("db defines environment; Noebs Postgres bootstrap must be file-mounted")
	}
	if db.EnvFile != nil {
		t.Fatalf("db defines env_file; Noebs Postgres bootstrap must be file-mounted")
	}
	if !containsString(db.Entrypoint, "/opt/noebs-postgres/bin/start.sh") {
		t.Fatalf("db entrypoint = %v, want mounted start.sh", db.Entrypoint)
	}
	requireComposeVolume(t, "db", db.Volumes, "./deploy/docker/postgres/postgres-start.sh", "/opt/noebs-postgres/bin/start.sh")
	requireComposeVolume(t, "db", db.Volumes, "./deploy/docker/postgres/001-service-databases.sql", "/opt/noebs-postgres/init/001-service-databases.sql")
	requireComposeVolume(t, "db", db.Volumes, "noebs-runtime", "/opt/noebs-postgres/secrets")
	if config.Noebs.RenderDBPasswordFile != "/app/runtime/password" {
		t.Fatalf("config.docker.yaml render_db_password_file = %q, want /app/runtime/password", config.Noebs.RenderDBPasswordFile)
	}
}

func TestDockerComposeWalletRuntimeConfigMatchesKubernetes(t *testing.T) {
	dockerConfig := decodeMountedNoebsConfigFile(t, filepath.Join("..", "config.docker.yaml"))
	kubernetesConfig := decodeKubernetesBaseNoebsConfig(t)

	checks := []struct {
		name   string
		docker any
		k8s    any
	}{
		{"wallet_enabled", dockerConfig.Noebs.WalletEnabled, kubernetesConfig.Noebs.WalletEnabled},
		{"wallet_pin_required", dockerConfig.Noebs.WalletPINRequired, kubernetesConfig.Noebs.WalletPINRequired},
		{"wallet_2fa_threshold", dockerConfig.Noebs.Wallet2FAThreshold, kubernetesConfig.Noebs.Wallet2FAThreshold},
		{"wallet_approval_threshold", dockerConfig.Noebs.WalletApprovalThreshold, kubernetesConfig.Noebs.WalletApprovalThreshold},
		{"wallet_default_currency", dockerConfig.Noebs.WalletDefaultCurrency, kubernetesConfig.Noebs.WalletDefaultCurrency},
		{"wallet_hold_expiry_seconds", dockerConfig.Noebs.WalletHoldExpirySeconds, kubernetesConfig.Noebs.WalletHoldExpirySeconds},
		{"wallet_approval_timeout_seconds", dockerConfig.Noebs.WalletApprovalTimeoutSeconds, kubernetesConfig.Noebs.WalletApprovalTimeoutSeconds},
		{"wallet_verification_timeout_seconds", dockerConfig.Noebs.WalletVerificationTimeoutSeconds, kubernetesConfig.Noebs.WalletVerificationTimeoutSeconds},
		{"wallet_manual_approval_timeout_seconds", dockerConfig.Noebs.WalletManualTransferApprovalTimeoutSeconds, kubernetesConfig.Noebs.WalletManualTransferApprovalTimeoutSeconds},
		{"wallet_psp_poller_cron", dockerConfig.Noebs.WalletPSPPollerCron, kubernetesConfig.Noebs.WalletPSPPollerCron},
		{"wallet_psp_poller_batch_size", dockerConfig.Noebs.WalletPSPPollerBatchSize, kubernetesConfig.Noebs.WalletPSPPollerBatchSize},
		{"wallet_psp_poller_interval_seconds", dockerConfig.Noebs.WalletPSPPollerIntervalSeconds, kubernetesConfig.Noebs.WalletPSPPollerIntervalSeconds},
		{"wallet_reconciliation_cron", dockerConfig.Noebs.WalletReconciliationCron, kubernetesConfig.Noebs.WalletReconciliationCron},
		{"wallet_reconciliation_batch_size", dockerConfig.Noebs.WalletReconciliationBatchSize, kubernetesConfig.Noebs.WalletReconciliationBatchSize},
		{"wallet_reconciliation_lookback_hours", dockerConfig.Noebs.WalletReconciliationLookbackHours, kubernetesConfig.Noebs.WalletReconciliationLookbackHours},
	}
	for _, check := range checks {
		if check.docker != check.k8s {
			t.Fatalf("config.docker.yaml %s = %v, want Kubernetes value %v", check.name, check.docker, check.k8s)
		}
	}
}

func TestDockerComposeEBSRuntimeConfigMatchesKubernetes(t *testing.T) {
	dockerConfig := decodeMountedNoebsConfigFile(t, filepath.Join("..", "config.docker.yaml"))
	kubernetesConfig := decodeKubernetesBaseNoebsConfig(t)

	checks := []struct {
		name   string
		docker float32
		k8s    float32
	}{
		{"p2p_fees", dockerConfig.Noebs.EBSDynamicFees.CardTransferfees, kubernetesConfig.Noebs.EBSDynamicFees.CardTransferfees},
		{"custom_fees", dockerConfig.Noebs.EBSDynamicFees.CustomFees, kubernetesConfig.Noebs.EBSDynamicFees.CustomFees},
		{"special_payment_fees", dockerConfig.Noebs.EBSDynamicFees.SpecialPaymentFees, kubernetesConfig.Noebs.EBSDynamicFees.SpecialPaymentFees},
	}
	for _, check := range checks {
		if check.docker <= 0 {
			t.Fatalf("config.docker.yaml ebs_dynamic_fees.%s = %v, want positive explicit fee", check.name, check.docker)
		}
		if check.docker != check.k8s {
			t.Fatalf("config.docker.yaml ebs_dynamic_fees.%s = %v, want Kubernetes value %v", check.name, check.docker, check.k8s)
		}
	}
}

func TestTemporalDockerComposeUsesMountedConfigAndSchemaJob(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))

	temporalPostgres, ok := compose.Services["temporal-postgres"]
	if !ok {
		t.Fatalf("docker-compose.yml missing temporal-postgres service")
	}
	if temporalPostgres.Environment != nil {
		t.Fatalf("temporal-postgres defines environment; Temporal database bootstrap must be file-mounted")
	}
	if temporalPostgres.EnvFile != nil {
		t.Fatalf("temporal-postgres defines env_file; Temporal database bootstrap must be file-mounted")
	}
	if !containsString(temporalPostgres.Entrypoint, "/opt/temporal-postgres/bin/start.sh") {
		t.Fatalf("temporal-postgres entrypoint = %v, want mounted start.sh", temporalPostgres.Entrypoint)
	}
	requireComposeVolume(t, "temporal-postgres", temporalPostgres.Volumes, "./deploy/docker/temporal/postgres-start.sh", "/opt/temporal-postgres/bin/start.sh")
	requireComposeSecret(t, "temporal-postgres", temporalPostgres.Secrets, "temporal_postgres_password", "/opt/temporal-postgres/secrets/password")

	temporalSchemaMigrate, ok := compose.Services["temporal-schema-migrate"]
	if !ok {
		t.Fatalf("docker-compose.yml missing temporal-schema-migrate service")
	}
	if temporalSchemaMigrate.Environment != nil {
		t.Fatalf("temporal-schema-migrate defines environment; Temporal migration must use mounted config/secrets")
	}
	if temporalSchemaMigrate.EnvFile != nil {
		t.Fatalf("temporal-schema-migrate defines env_file; Temporal migration must use mounted config/secrets")
	}
	if !containsString(temporalSchemaMigrate.Entrypoint, "/opt/temporal/bin/schema-migrate.sh") {
		t.Fatalf("temporal-schema-migrate entrypoint = %v, want mounted schema migration script", temporalSchemaMigrate.Entrypoint)
	}
	requireComposeVolume(t, "temporal-schema-migrate", temporalSchemaMigrate.Volumes, "./deploy/docker/temporal/schema-migrate.sh", "/opt/temporal/bin/schema-migrate.sh")
	requireComposeSecret(t, "temporal-schema-migrate", temporalSchemaMigrate.Secrets, "temporal_postgres_password", "/opt/temporal/secrets/postgres-password")

	temporal, ok := compose.Services["temporal"]
	if !ok {
		t.Fatalf("docker-compose.yml missing temporal service")
	}
	if temporal.Environment != nil {
		t.Fatalf("temporal defines environment; Temporal config must be file-mounted")
	}
	if temporal.EnvFile != nil {
		t.Fatalf("temporal defines env_file; Temporal config must be file-mounted")
	}
	if !containsString(temporal.Entrypoint, "/opt/temporal/bin/temporal-start.sh") {
		t.Fatalf("temporal entrypoint = %v, want mounted start script", temporal.Entrypoint)
	}
	requireComposeVolume(t, "temporal", temporal.Volumes, "./deploy/docker/temporal/temporal-start.sh", "/opt/temporal/bin/temporal-start.sh")
	requireComposeVolume(t, "temporal", temporal.Volumes, "./deploy/docker/temporal/temporal.yaml", "/opt/temporal/config/temporal.yaml")
	requireComposeVolume(t, "temporal", temporal.Volumes, "./deploy/docker/temporal/dynamicconfig.yaml", "/opt/temporal/config/dynamicconfig/docker.yaml")
	requireComposeSecret(t, "temporal", temporal.Secrets, "temporal_postgres_password", "/opt/temporal/secrets/postgres-password")
	rejectComposePublishedPorts(t, "temporal", temporal.Ports)
	temporalStart, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "temporal", "temporal-start.sh"))
	if err != nil {
		t.Fatalf("read Temporal start script: %v", err)
	}
	requireTemporalStartScriptExplicitInputs(t, string(temporalStart))

	temporalUI, ok := compose.Services["temporal-ui"]
	if !ok {
		t.Fatalf("docker-compose.yml missing temporal-ui service")
	}
	if temporalUI.Environment != nil {
		t.Fatalf("temporal-ui defines environment; Temporal UI config must be file-mounted")
	}
	if temporalUI.EnvFile != nil {
		t.Fatalf("temporal-ui defines env_file; Temporal UI config must be file-mounted")
	}
	if !containsString(temporalUI.Entrypoint, "/home/ui-server/ui-server") || !containsString(temporalUI.Entrypoint, "start") {
		t.Fatalf("temporal-ui entrypoint = %v, want ui-server start", temporalUI.Entrypoint)
	}
	requireComposeVolume(t, "temporal-ui", temporalUI.Volumes, "./deploy/docker/temporal/ui.yaml", "/home/ui-server/config/development.yaml")
	rejectComposePublishedPorts(t, "temporal-ui", temporalUI.Ports)
	requireComposeTopLevelSecret(t, compose.Secrets, "temporal_postgres_password", "./deploy/docker/temporal/postgres-password.txt")
}

func TestCaddyEdgeProxyTargetsOnlyAPIGateway(t *testing.T) {
	path := filepath.Join("..", "Caddyfile")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	foundProxy := false
	for lineIndex, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || !strings.HasPrefix(trimmed, "reverse_proxy") {
			continue
		}
		foundProxy = true
		fields := strings.Fields(trimmed)
		if len(fields) != 2 {
			t.Fatalf("%s:%d reverse_proxy = %q, want exactly one upstream", path, lineIndex+1, trimmed)
		}
		if fields[1] != "api-gateway:8080" {
			t.Fatalf("%s:%d reverse_proxy upstream = %q, want api-gateway:8080", path, lineIndex+1, fields[1])
		}
	}
	if !foundProxy {
		t.Fatalf("%s has no reverse_proxy directive", path)
	}
}

func TestDockerfileDoesNotDefineRoleAgnosticRuntimeMetadata(t *testing.T) {
	path := filepath.Join("..", "Dockerfile")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	forbiddenInstructions := []string{"EXPOSE", "HEALTHCHECK"}
	for lineIndex, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		instruction := strings.Fields(strings.ToUpper(trimmed))[0]
		for _, forbidden := range forbiddenInstructions {
			if instruction == forbidden {
				t.Fatalf("%s:%d defines image-level %s; runtime ports and probes must be role-specific in deployment manifests", path, lineIndex+1, forbidden)
			}
		}
	}
}

func TestKeycloakDockerComposeUsesMountedConfigSecret(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	config := decodeMountedNoebsConfigFile(t, filepath.Join("..", "config.docker.yaml"))

	keycloak, ok := compose.Services["keycloak"]
	if !ok {
		t.Fatalf("docker-compose.yml missing keycloak service")
	}
	if keycloak.Environment != nil {
		t.Fatalf("keycloak defines environment; keycloak config must be file-mounted")
	}
	if keycloak.EnvFile != nil {
		t.Fatalf("keycloak defines env_file; keycloak config must be file-mounted")
	}
	requireComposeSecret(t, "keycloak", keycloak.Secrets, "keycloak_config", "/opt/keycloak/conf/keycloak.conf")
	requireComposeTopLevelSecret(t, compose.Secrets, "keycloak_config", "./deploy/docker/keycloak/keycloak.conf")
	rejectComposePublishedPorts(t, "keycloak", keycloak.Ports)

	keycloakPostgres, ok := compose.Services["keycloak-postgres"]
	if !ok {
		t.Fatalf("docker-compose.yml missing keycloak-postgres service")
	}
	if keycloakPostgres.Environment != nil {
		t.Fatalf("keycloak-postgres defines environment; Keycloak database bootstrap must be file-mounted")
	}
	if keycloakPostgres.EnvFile != nil {
		t.Fatalf("keycloak-postgres defines env_file; Keycloak database bootstrap must be file-mounted")
	}
	requireComposeVolume(t, "keycloak-postgres", keycloakPostgres.Volumes, "./deploy/docker/keycloak/postgres-start.sh", "/opt/keycloak-postgres/bin/start.sh")
	requireComposeSecret(t, "keycloak-postgres", keycloakPostgres.Secrets, "keycloak_postgres_password", "/opt/keycloak-postgres/secrets/password")
	requireComposeTopLevelSecret(t, compose.Secrets, "keycloak_postgres_password", "./deploy/docker/keycloak/postgres-password.txt")
	if config.Noebs.ServiceDiscovery["keycloak"] == "" {
		t.Fatalf("config.docker.yaml must include noebs.service_discovery.keycloak")
	}
	serviceName, port := parseHTTPDiscoveryEndpoint(t, "keycloak", config.Noebs.ServiceDiscovery["keycloak"])
	if serviceName != "keycloak" || port != 8080 {
		t.Fatalf("keycloak service discovery = %s:%d, want keycloak:8080", serviceName, port)
	}
}

func TestDockerComposePublishesOnlyAPIGatewayByDefault(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))

	for serviceName, service := range compose.Services {
		if serviceName == "api-gateway" || serviceName == "caddy" {
			continue
		}
		rejectComposePublishedPorts(t, serviceName, service.Ports)
	}

	apiGateway, ok := compose.Services["api-gateway"]
	if !ok {
		t.Fatalf("docker-compose.yml missing api-gateway service")
	}
	if !containsString(apiGateway.Ports, "0.0.0.0:8081:8080") {
		t.Fatalf("api-gateway ports = %v, want host publication on 0.0.0.0:8081", apiGateway.Ports)
	}

	caddy, ok := compose.Services["caddy"]
	if !ok {
		t.Fatalf("docker-compose.yml missing caddy service")
	}
	if !containsString(caddy.Profiles, "edge") {
		t.Fatalf("caddy profiles = %v, want edge profile", caddy.Profiles)
	}
}

func TestFoundationOwnsArgoCDApplication(t *testing.T) {
	mainPath := filepath.Join("..", "foundation", "terraform", "main.tf")
	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read %s: %v", mainPath, err)
	}
	mainText := string(data)

	required := []string{
		`resource "kubernetes_manifest" "noebs_project"`,
		`resource "kubernetes_manifest" "noebs_application"`,
		`namespace = var.argocd_namespace`,
		`var.noebs_repo_url`,
		`repoURL        = var.noebs_repo_url`,
		`targetRevision = var.noebs_target_revision`,
		`path           = var.noebs_manifest_path`,
		`namespace = kubernetes_namespace_v1.noebs.metadata[0].name`,
		`server    = "https://kubernetes.default.svc"`,
		`prune    = true`,
		`selfHeal = true`,
		`"PruneLast=true"`,
		`depends_on = [
    kubernetes_manifest.noebs_project,
    kubernetes_namespace_v1.noebs,
  ]`,
	}
	for _, snippet := range required {
		if !strings.Contains(mainText, snippet) {
			t.Fatalf("%s missing required Argo CD ownership snippet:\n%s", mainPath, snippet)
		}
	}

	tfvarsExamplePath := filepath.Join("..", "foundation", "terraform", "terraform.tfvars.example")
	tfvarsExample, err := os.ReadFile(tfvarsExamplePath)
	if err != nil {
		t.Fatalf("read %s: %v", tfvarsExamplePath, err)
	}
	manifestPathRe := regexp.MustCompile(`(?m)^\s*noebs_manifest_path\s*=\s*"([^"]+)"\s*$`)
	match := manifestPathRe.FindStringSubmatch(string(tfvarsExample))
	if len(match) != 2 {
		t.Fatalf("%s must assign noebs_manifest_path", tfvarsExamplePath)
	}
	if match[1] != "deploy/kubernetes/overlays/current-host" {
		t.Fatalf("noebs_manifest_path = %q, want deploy/kubernetes/overlays/current-host", match[1])
	}
	if _, err := os.Stat(filepath.Join("..", filepath.FromSlash(match[1]), "kustomization.yaml")); err != nil {
		t.Fatalf("noebs_manifest_path does not contain kustomization.yaml: %v", err)
	}
}

func TestArgoCDApplicationIsOwnedByFoundationOnly(t *testing.T) {
	dir := filepath.Join("..", "deploy", "argocd")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		for _, object := range decodeManifestObjects(t, path) {
			if object.Kind == "Application" || object.Kind == "AppProject" {
				t.Fatalf("%s contains Argo CD %s %q; Foundation/OpenTofu must own Argo CD application resources", path, object.Kind, object.Metadata.Name)
			}
		}
	}
}

func TestMigrationJobsRunBeforeNoebsRuntimeWorkloads(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	expectedJobs := map[string]bool{
		"noebs-identity-auth-migrate":        false,
		"noebs-card-vault-migrate":           false,
		"noebs-ebs-adapter-migrate":          false,
		"noebs-psp-webhook-migrate":          false,
		"noebs-admin-reporting-migrate":      false,
		"noebs-notification-chat-migrate":    false,
		"noebs-consumer-beneficiary-migrate": false,
		"noebs-wallet-ledger-migrate":        false,
	}
	expectedRuntimeDeployments := map[string]bool{
		"api-gateway":          false,
		"identity-auth":        false,
		"card-vault":           false,
		"ebs-adapter":          false,
		"psp-webhook":          false,
		"admin-reporting":      false,
		"notification-chat":    false,
		"consumer-beneficiary": false,
		"wallet-api":           false,
		"wallet-ledger":        false,
		"wallet-worker":        false,
	}

	for _, object := range objects {
		switch object.Kind {
		case "Deployment":
			if !workloadUsesNoebsImage(object) {
				continue
			}
			if _, ok := expectedRuntimeDeployments[object.Metadata.Name]; !ok {
				t.Fatalf("unexpected noebs runtime Deployment %q", object.Metadata.Name)
			}
			expectedRuntimeDeployments[object.Metadata.Name] = true
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "20" {
				t.Fatalf("%s runtime sync-wave = %q, want 20", object.Metadata.Name, object.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
			}
			if object.Metadata.Annotations["argocd.argoproj.io/hook"] != "" {
				t.Fatalf("%s runtime must not be an Argo hook", object.Metadata.Name)
			}
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("%s runtime containers = %d, want 1", object.Metadata.Name, len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			serviceMount := findMount(container, "/app/service.yaml")
			expectedSubPath := object.Metadata.Name + ".service.yaml"
			if serviceMount == nil || serviceMount.SubPath != expectedSubPath {
				t.Fatalf("%s runtime service mount = %#v, want %q", object.Metadata.Name, serviceMount, expectedSubPath)
			}
		case "Job":
			if !strings.HasPrefix(object.Metadata.Name, "noebs-") {
				continue
			}
			if object.Metadata.Name == "noebs-deployment-preflight" {
				continue
			}
			if _, ok := expectedJobs[object.Metadata.Name]; !ok {
				t.Fatalf("unexpected migration Job %q", object.Metadata.Name)
			}
			expectedJobs[object.Metadata.Name] = true
			if object.Metadata.Annotations["argocd.argoproj.io/hook"] != "Sync" {
				t.Fatalf("%s hook = %q, want Sync", object.Metadata.Name, object.Metadata.Annotations["argocd.argoproj.io/hook"])
			}
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "10" {
				t.Fatalf("%s sync-wave = %q, want 10", object.Metadata.Name, object.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
			}
			if object.Metadata.Annotations["argocd.argoproj.io/hook-delete-policy"] != "BeforeHookCreation,HookSucceeded" {
				t.Fatalf("%s hook delete policy = %q", object.Metadata.Name, object.Metadata.Annotations["argocd.argoproj.io/hook-delete-policy"])
			}
			if object.Spec.Template.Spec.RestartPolicy != "Never" {
				t.Fatalf("%s restartPolicy = %q, want Never", object.Metadata.Name, object.Spec.Template.Spec.RestartPolicy)
			}
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("%s containers = %d, want 1", object.Metadata.Name, len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			if !strings.Contains(container.Image, "ghcr.io/adonese/noebs") {
				t.Fatalf("%s container image = %q", object.Metadata.Name, container.Image)
			}
			serviceMount := findMount(container, "/app/service.yaml")
			expectedSubPath := strings.TrimPrefix(object.Metadata.Name, "noebs-") + ".service.yaml"
			if serviceMount == nil || serviceMount.SubPath != expectedSubPath {
				t.Fatalf("%s service mount = %#v, want %q", object.Metadata.Name, serviceMount, expectedSubPath)
			}
			if len(container.Ports) != 0 {
				t.Fatalf("%s migration Job must not expose container ports", object.Metadata.Name)
			}
			if len(container.ReadinessProbe) != 0 || len(container.LivenessProbe) != 0 || len(container.StartupProbe) != 0 {
				t.Fatalf("%s migration Job must not define runtime probes", object.Metadata.Name)
			}
			requireMount(t, object.Metadata.Name, container, "/app/config.yaml", "config.yaml")
			requireMount(t, object.Metadata.Name, container, "/app/secrets.yaml", "secrets.yaml")
		}
	}

	for job, found := range expectedJobs {
		if !found {
			t.Fatalf("migration Job %q not found", job)
		}
	}
	for deployment, found := range expectedRuntimeDeployments {
		if !found {
			t.Fatalf("runtime Deployment %q not found", deployment)
		}
	}
}

func TestDeploymentPreflightJobRunsBeforeMigrations(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	serviceConfigs := map[string]string{
		"api-gateway":                  "api-gateway.service.yaml",
		"identity-auth":                "identity-auth.service.yaml",
		"card-vault":                   "card-vault.service.yaml",
		"ebs-adapter":                  "ebs-adapter.service.yaml",
		"psp-webhook":                  "psp-webhook.service.yaml",
		"admin-reporting":              "admin-reporting.service.yaml",
		"notification-chat":            "notification-chat.service.yaml",
		"consumer-beneficiary":         "consumer-beneficiary.service.yaml",
		"wallet-api":                   "wallet-api.service.yaml",
		"wallet-ledger":                "wallet-ledger.service.yaml",
		"wallet-worker":                "wallet-worker.service.yaml",
		"identity-auth-migrate":        "identity-auth-migrate.service.yaml",
		"card-vault-migrate":           "card-vault-migrate.service.yaml",
		"ebs-adapter-migrate":          "ebs-adapter-migrate.service.yaml",
		"psp-webhook-migrate":          "psp-webhook-migrate.service.yaml",
		"admin-reporting-migrate":      "admin-reporting-migrate.service.yaml",
		"notification-chat-migrate":    "notification-chat-migrate.service.yaml",
		"consumer-beneficiary-migrate": "consumer-beneficiary-migrate.service.yaml",
		"wallet-ledger-migrate":        "wallet-ledger-migrate.service.yaml",
	}
	rendererServiceConfigs := map[string]bool{}
	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		rendererServiceConfigs[serviceName] = true
	}
	for serviceName := range serviceConfigs {
		if !rendererServiceConfigs[serviceName] {
			t.Fatalf("preflight validates service config %s but render-kubernetes-secrets release validation does not", serviceName)
		}
	}
	for serviceName := range rendererServiceConfigs {
		if _, ok := serviceConfigs[serviceName]; !ok {
			t.Fatalf("render-kubernetes-secrets release validation expects service config %s but preflight does not mount it", serviceName)
		}
	}
	serviceSecrets := map[string]string{
		"api-gateway":          "api-gateway-secrets",
		"identity-auth":        "identity-auth-secrets",
		"card-vault":           "card-vault-secrets",
		"ebs-adapter":          "ebs-adapter-secrets",
		"psp-webhook":          "psp-webhook-secrets",
		"admin-reporting":      "admin-reporting-secrets",
		"notification-chat":    "notification-chat-secrets",
		"consumer-beneficiary": "consumer-beneficiary-secrets",
		"wallet-api":           "wallet-api-secrets",
		"wallet-ledger":        "wallet-ledger-secrets",
		"wallet-worker":        "wallet-worker-secrets",
	}

	var found bool
	for _, object := range objects {
		if object.Kind != "Job" || object.Metadata.Name != "noebs-deployment-preflight" {
			continue
		}
		found = true
		if object.Metadata.Annotations["argocd.argoproj.io/hook"] != "Sync" {
			t.Fatalf("preflight hook = %q, want Sync", object.Metadata.Annotations["argocd.argoproj.io/hook"])
		}
		if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "0" {
			t.Fatalf("preflight sync-wave = %q, want 0", object.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
		}
		if object.Metadata.Annotations["argocd.argoproj.io/hook-delete-policy"] != "BeforeHookCreation,HookSucceeded" {
			t.Fatalf("preflight hook delete policy = %q", object.Metadata.Annotations["argocd.argoproj.io/hook-delete-policy"])
		}
		if object.Spec.Template.Spec.ServiceAccountName != "deployment-preflight" {
			t.Fatalf("preflight serviceAccountName = %q", object.Spec.Template.Spec.ServiceAccountName)
		}
		if object.Spec.Template.Spec.AutomountServiceAccountToken == nil || *object.Spec.Template.Spec.AutomountServiceAccountToken {
			t.Fatalf("preflight must disable service account token automount")
		}
		if object.Spec.Template.Spec.RestartPolicy != "Never" {
			t.Fatalf("preflight restartPolicy = %q, want Never", object.Spec.Template.Spec.RestartPolicy)
		}
		if len(object.Spec.Template.Spec.Containers) != 1 {
			t.Fatalf("preflight containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
		}
		container := object.Spec.Template.Spec.Containers[0]
		if container.Image != "ghcr.io/adonese/noebs:master" {
			t.Fatalf("preflight image = %q", container.Image)
		}
		if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
			t.Fatalf("preflight must use mounted config/secrets instead of env/envFrom")
		}
		if !containsString(container.Command, "/usr/local/bin/noebs") {
			t.Fatalf("preflight command = %v, want noebs binary", container.Command)
		}
		if !containsString(container.Args, "validate-kubernetes-deployment") || !containsString(container.Args, "/preflight") {
			t.Fatalf("preflight args = %v, want validate-kubernetes-deployment /preflight", container.Args)
		}
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/config.yaml", "config.yaml")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/.sops/age-key.txt", "age-key.txt")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/postgres-password.txt", "password")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/temporal-postgres-password.txt", "password")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/keycloak-postgres-password.txt", "password")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/keycloak.conf", "keycloak.conf")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "postgres-credentials", "postgres-credentials")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "temporal-postgres-credentials", "temporal-postgres-credentials")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "keycloak-postgres-credentials", "keycloak-postgres-credentials")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "keycloak-secrets", "keycloak-secrets")

		for serviceName, subPath := range serviceConfigs {
			requireMount(t, "noebs-deployment-preflight", container, "/preflight/services/"+serviceName+".yaml", subPath)
		}
		for serviceName, volumeName := range serviceSecrets {
			requireMount(t, "noebs-deployment-preflight", container, "/preflight/secrets/"+serviceName+".secrets.yaml", "secrets.yaml")
			requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, volumeName, volumeName)
		}
	}
	if !found {
		t.Fatalf("noebs-deployment-preflight Job not found")
	}
}

func TestKubernetesSecretRendererCoversManifestSecretReferences(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	objects = append(objects, decodeManifestObjects(t, filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host", "ingress.yaml"))...)

	referencedSecrets := map[string]bool{}
	for _, object := range objects {
		for _, volume := range object.Spec.Template.Spec.Volumes {
			if volume.Secret != nil && volume.Secret.SecretName != "" {
				referencedSecrets[volume.Secret.SecretName] = true
			}
		}
		for _, tls := range object.Spec.TLS {
			if tls.SecretName != "" {
				referencedSecrets[tls.SecretName] = true
			}
		}
	}
	if len(referencedSecrets) == 0 {
		t.Fatalf("no Kubernetes Secret references were found")
	}

	renderedSecrets := renderedKubernetesSecretNames()
	for secretName := range referencedSecrets {
		if !renderedSecrets[secretName] {
			t.Fatalf("Kubernetes manifest references Secret %q but render-kubernetes-secrets does not render it", secretName)
		}
	}
	for secretName := range renderedSecrets {
		if !referencedSecrets[secretName] {
			t.Fatalf("render-kubernetes-secrets renders Secret %q but no Kubernetes manifest references it", secretName)
		}
	}
}

func TestFoundationRequiredKubernetesSecretsMatchRenderer(t *testing.T) {
	requiredSecrets := parseTerraformStringListLocal(t, filepath.Join("..", "foundation", "terraform", "locals.tf"), "noebs_required_kubernetes_secrets")
	renderedSecrets := renderedKubernetesSecretNames()

	for secretName := range renderedSecrets {
		if !requiredSecrets[secretName] {
			t.Fatalf("render-kubernetes-secrets renders Secret %q but noebs_required_kubernetes_secrets does not declare it", secretName)
		}
	}
	for secretName := range requiredSecrets {
		if !renderedSecrets[secretName] {
			t.Fatalf("noebs_required_kubernetes_secrets declares Secret %q but render-kubernetes-secrets does not render it", secretName)
		}
	}

	outputs, err := os.ReadFile(filepath.Join("..", "foundation", "terraform", "outputs.tf"))
	if err != nil {
		t.Fatalf("read foundation/terraform/outputs.tf: %v", err)
	}
	if !strings.Contains(string(outputs), `output "noebs_required_kubernetes_secrets"`) {
		t.Fatalf("foundation/terraform/outputs.tf must expose noebs_required_kubernetes_secrets")
	}
}

func TestFoundationTerraformVariablesRequireExplicitInputs(t *testing.T) {
	variablesPath := filepath.Join("..", "foundation", "terraform", "variables.tf")
	tfvarsExamplePath := filepath.Join("..", "foundation", "terraform", "terraform.tfvars.example")

	blocks := parseTerraformVariableBlocks(t, variablesPath)
	tfvarsExample, err := os.ReadFile(tfvarsExamplePath)
	if err != nil {
		t.Fatalf("read %s: %v", tfvarsExamplePath, err)
	}
	tfvarsExampleText := string(tfvarsExample)

	explicitInputs := []string{
		"deployment_host",
		"kubeconfig_path",
		"argocd_namespace",
		"noebs_namespace",
		"argocd_chart_version",
		"noebs_repo_url",
		"noebs_target_revision",
		"noebs_manifest_path",
	}
	defaultRe := regexp.MustCompile(`(?m)^\s*default\s*=`)
	nullableFalseRe := regexp.MustCompile(`(?m)^\s*nullable\s*=\s*false\s*$`)
	for _, name := range explicitInputs {
		block, ok := blocks[name]
		if !ok {
			t.Fatalf("foundation variable %q not found", name)
		}
		if defaultRe.MatchString(block) {
			t.Fatalf("foundation variable %q must not define a default; record the value in terraform.tfvars", name)
		}
		if !nullableFalseRe.MatchString(block) {
			t.Fatalf("foundation variable %q must set nullable = false", name)
		}
		assignmentRe := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=`)
		if !assignmentRe.MatchString(tfvarsExampleText) {
			t.Fatalf("%s must assign %q", tfvarsExamplePath, name)
		}
	}
}

func renderedKubernetesSecretNames() map[string]bool {
	secrets := map[string]bool{
		"sops-age-key":                  true,
		"postgres-credentials":          true,
		"temporal-postgres-credentials": true,
		"keycloak-postgres-credentials": true,
		"keycloak-secrets":              true,
		"noebs-tls":                     true,
	}
	for _, source := range kubernetesServiceSecretSources {
		secrets[source.secretName] = true
	}
	return secrets
}

func decodeManifestObjects(t *testing.T, path string) []manifestObject {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var objects []manifestObject
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var object manifestObject
		if err := decoder.Decode(&object); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode %s: %v", path, err)
		}
		if object.Kind != "" {
			objects = append(objects, object)
		}
	}
	return objects
}

func decodeManifestObjectsFromDir(t *testing.T, dir string) []manifestObject {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var objects []manifestObject
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		objects = append(objects, decodeManifestObjects(t, filepath.Join(dir, entry.Name()))...)
	}
	return objects
}

func decodeComposeDocument(t *testing.T, path string) composeDocument {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var compose composeDocument
	if err := yaml.Unmarshal(data, &compose); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return compose
}

func decodeMountedNoebsConfigFile(t *testing.T, path string) mountedNoebsConfig {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var config mountedNoebsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return config
}

func decodeKubernetesBaseNoebsConfig(t *testing.T) mountedNoebsConfig {
	t.Helper()
	configData := decodeKubernetesNoebsConfigMapData(t)["config.yaml"]
	if configData == "" {
		t.Fatalf("noebs-config missing config.yaml")
	}
	var config mountedNoebsConfig
	if err := yaml.Unmarshal([]byte(configData), &config); err != nil {
		t.Fatalf("parse noebs-config config.yaml: %v", err)
	}
	return config
}

func decodeKubernetesNoebsConfigMapData(t *testing.T) map[string]string {
	t.Helper()
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	for _, object := range objects {
		if object.Kind == "ConfigMap" && object.Metadata.Name == "noebs-config" {
			return object.Data
		}
	}
	t.Fatalf("noebs-config ConfigMap not found")
	return nil
}

func decodeNoebsServiceConfigFile(t *testing.T, path string) mountedNoebsServiceConfig {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return decodeNoebsServiceConfigBytes(t, path, data)
}

func decodeNoebsServiceConfigBytes(t *testing.T, label string, data []byte) mountedNoebsServiceConfig {
	t.Helper()
	var config mountedNoebsServiceConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse %s: %v", label, err)
	}
	return config
}

func requireServiceIdentityConfig(t *testing.T, label string, config mountedNoebsServiceConfig) {
	t.Helper()
	role, err := parseServiceRole(config.Noebs.ServiceRole)
	if err != nil {
		t.Fatalf("%s service_role = %q: %v", label, config.Noebs.ServiceRole, err)
	}
	if role.opensDatabase() {
		if config.Noebs.DatabaseDriver != "pgx" {
			t.Fatalf("%s noebs.db_driver = %q, want pgx for database-opening role %s", label, config.Noebs.DatabaseDriver, role)
		}
	} else if config.Noebs.DatabaseDriver != "" {
		t.Fatalf("%s noebs.db_driver = %q, want empty for no-database role %s", label, config.Noebs.DatabaseDriver, role)
	}
	if config.Noebs.OtelServiceName != string(role) {
		t.Fatalf("%s noebs.otel_service_name = %q, want %q", label, config.Noebs.OtelServiceName, role)
	}
}

func isKubernetesWorkloadKind(kind string) bool {
	switch kind {
	case "Deployment", "StatefulSet", "Job":
		return true
	default:
		return false
	}
}

func workloadUsesNoebsImage(object manifestObject) bool {
	for _, container := range append(object.Spec.Template.Spec.Containers, object.Spec.Template.Spec.InitContainers...) {
		if strings.Contains(container.Image, "ghcr.io/adonese/noebs") {
			return true
		}
	}
	return false
}

func expectedServiceAccountForWorkload(t *testing.T, object manifestObject) string {
	t.Helper()
	if object.Kind != "Job" {
		return object.Metadata.Name
	}
	if strings.HasPrefix(object.Metadata.Name, "noebs-") {
		return strings.TrimPrefix(object.Metadata.Name, "noebs-")
	}
	return object.Metadata.Name
}

func requireMount(t *testing.T, workload string, container manifestContainer, mountPath, subPath string) {
	t.Helper()
	for _, mount := range container.VolumeMounts {
		if mount.MountPath != mountPath {
			continue
		}
		if subPath != "" && mount.SubPath != subPath {
			t.Fatalf("%s/%s mount %s subPath = %q, want %q", workload, container.Name, mountPath, mount.SubPath, subPath)
		}
		return
	}
	t.Fatalf("%s/%s missing mount %s", workload, container.Name, mountPath)
}

func requireNoebsSecretVolume(t *testing.T, workload string, container manifestContainer, volumes []manifestVolume) {
	t.Helper()
	mount := findMount(container, "/app/secrets.yaml")
	if mount == nil {
		t.Fatalf("%s/%s missing /app/secrets.yaml mount", workload, container.Name)
	}
	if mount.Name == "" {
		t.Fatalf("%s/%s /app/secrets.yaml mount missing volume name", workload, container.Name)
	}
	expectedSecret := composeSecretSourceForService(strings.TrimPrefix(workload, "noebs-"))
	for _, volume := range volumes {
		if volume.Name != mount.Name {
			continue
		}
		if volume.Secret == nil {
			t.Fatalf("%s volume %s is not a Secret volume", workload, mount.Name)
		}
		if volume.Secret.SecretName != expectedSecret {
			t.Fatalf("%s secretName = %q, want %q", workload, volume.Secret.SecretName, expectedSecret)
		}
		return
	}
	t.Fatalf("%s missing secret volume %s", workload, mount.Name)
}

func requireSecretVolume(t *testing.T, workload string, volumes []manifestVolume, volumeName, secretName string) {
	t.Helper()
	for _, volume := range volumes {
		if volume.Name != volumeName {
			continue
		}
		if volume.Secret == nil {
			t.Fatalf("%s volume %s is not a Secret volume", workload, volumeName)
		}
		if volume.Secret.SecretName != secretName {
			t.Fatalf("%s volume %s secretName = %q, want %q", workload, volumeName, volume.Secret.SecretName, secretName)
		}
		return
	}
	t.Fatalf("%s missing Secret volume %s", workload, volumeName)
}

func parseHTTPDiscoveryEndpoint(t *testing.T, role, endpoint string) (string, int) {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("service_discovery.%s = %q: %v", role, endpoint, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		t.Fatalf("service_discovery.%s scheme = %q, want http or https", role, parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		t.Fatalf("service_discovery.%s = %q missing host", role, endpoint)
	}
	portText := parsed.Port()
	if portText == "" {
		t.Fatalf("service_discovery.%s = %q missing port", role, endpoint)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("service_discovery.%s port = %q: %v", role, portText, err)
	}
	return host, port
}

func parseHostPortDiscoveryEndpoint(t *testing.T, role, endpoint string) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil {
		t.Fatalf("grpc_service_discovery.%s = %q: %v", role, endpoint, err)
	}
	if host == "" {
		t.Fatalf("grpc_service_discovery.%s = %q missing host", role, endpoint)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("grpc_service_discovery.%s port = %q: %v", role, portText, err)
	}
	return host, port
}

func requireKubernetesConfigMapDataMatchesFile(t *testing.T, name, got, path string) {
	t.Helper()
	if got == "" {
		t.Fatalf("%s ConfigMap data is empty", name)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("%s differs from %s", name, path)
	}
}

func requireTemporalStartScriptExplicitInputs(t *testing.T, script string) {
	t.Helper()
	required := []string{
		`password="$(read_required_file "Temporal Postgres password" "$password_source")"`,
	}
	for _, want := range required {
		if !strings.Contains(script, want) {
			t.Fatalf("temporal-start.sh missing explicit mounted input read: %s", want)
		}
	}
	for _, rejected := range []string{":-", "getent hosts", "$(hostname)", "broadcast_address", "__TEMPORAL_POSTGRES_PASSWORD__", "__TEMPORAL_BROADCAST_ADDRESS__", "__BROADCAST_ADDRESS_FROM_FILE__"} {
		if strings.Contains(script, rejected) {
			t.Fatalf("temporal-start.sh must not derive password or broadcast address with %q", rejected)
		}
	}
}

func requireKubernetesServicePort(t *testing.T, services map[string]map[int]bool, serviceName string, port int) {
	t.Helper()
	ports, ok := services[serviceName]
	if !ok {
		t.Fatalf("service discovery references missing Kubernetes Service %q", serviceName)
	}
	if !ports[port] {
		t.Fatalf("Service %s ports = %v; missing port %d", serviceName, ports, port)
	}
}

func requireManifestServicePort(t *testing.T, object manifestObject, port int) {
	t.Helper()
	for _, servicePort := range object.Spec.Ports {
		if servicePort.Port == port {
			return
		}
	}
	t.Fatalf("Service %s ports = %v; missing port %d", object.Metadata.Name, object.Spec.Ports, port)
}

func requireComposeVolume(t *testing.T, serviceName string, volumes []string, source, target string) {
	t.Helper()
	for _, volume := range volumes {
		parts := strings.Split(volume, ":")
		if len(parts) >= 2 && parts[0] == source && parts[1] == target {
			return
		}
	}
	t.Fatalf("%s volumes = %v; missing %s:%s", serviceName, volumes, source, target)
}

func requireComposeSecret(t *testing.T, serviceName string, secrets []composeSecret, source, target string) {
	t.Helper()
	for _, secret := range secrets {
		if secret.Source == source && secret.Target == target {
			return
		}
	}
	t.Fatalf("%s secrets = %v; missing %s target %s", serviceName, secrets, source, target)
}

func rejectComposePublishedPorts(t *testing.T, serviceName string, ports []string) {
	t.Helper()
	if len(ports) != 0 {
		t.Fatalf("%s must not publish host ports; got %v", serviceName, ports)
	}
}

func rejectComposeSecret(t *testing.T, serviceName string, secrets []composeSecret, source string) {
	t.Helper()
	for _, secret := range secrets {
		if secret.Source == source {
			t.Fatalf("%s must not mount %s", serviceName, source)
		}
	}
}

func requireComposeTopLevelSecret(t *testing.T, secrets map[string]composeSecret, source, file string) {
	t.Helper()
	secret, ok := secrets[source]
	if !ok {
		t.Fatalf("docker-compose.yml missing top-level secret %q", source)
	}
	if secret.File != file {
		t.Fatalf("secret %s file = %q, want %q", source, secret.File, file)
	}
}

func composeSecretSourceForService(serviceName string) string {
	switch serviceName {
	case "identity-auth-migrate":
		return "identity-auth-secrets"
	case "card-vault-migrate":
		return "card-vault-secrets"
	case "ebs-adapter-migrate":
		return "ebs-adapter-secrets"
	case "psp-webhook-migrate":
		return "psp-webhook-secrets"
	case "admin-reporting-migrate":
		return "admin-reporting-secrets"
	case "notification-chat-migrate":
		return "notification-chat-secrets"
	case "consumer-beneficiary-migrate":
		return "consumer-beneficiary-secrets"
	case "wallet-ledger-migrate":
		return "wallet-ledger-secrets"
	case "wallet-worker":
		return "wallet-worker-secrets"
	default:
		return serviceName + "-secrets"
	}
}

func requirePlaceholderStrings(t *testing.T, path string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			requirePlaceholderStrings(t, path, child)
		}
	case []any:
		for _, child := range typed {
			requirePlaceholderStrings(t, path, child)
		}
	case string:
		if !strings.HasPrefix(typed, "REPLACE_WITH_") {
			t.Fatalf("%s contains non-placeholder secret value %q", path, typed)
		}
	}
}

func requireServiceDatabaseOwners(t *testing.T, path string, noebs map[string]any, owners []string) {
	t.Helper()
	raw, ok := noebs["service_databases"]
	if len(owners) == 0 {
		if ok {
			t.Fatalf("%s must not define noebs.service_databases", path)
		}
		return
	}
	if !ok {
		t.Fatalf("%s missing noebs.service_databases", path)
	}
	databases, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("%s noebs.service_databases must be a map", path)
	}
	if len(databases) != len(owners) {
		t.Fatalf("%s service_databases = %v, want owners %v", path, databases, owners)
	}
	for _, owner := range owners {
		value, ok := databases[owner]
		if !ok {
			t.Fatalf("%s missing noebs.service_databases.%s", path, owner)
		}
		dbURL, ok := value.(string)
		if !ok || !strings.HasPrefix(dbURL, "REPLACE_WITH_") {
			t.Fatalf("%s noebs.service_databases.%s = %v, want placeholder", path, owner, value)
		}
	}
}

func requireEBSAdapterSecrets(t *testing.T, path string, noebs map[string]any) {
	t.Helper()
	for _, key := range []string{
		"consumer_endpoint",
		"merchant_endpoint",
		"ipin_endpoint",
		"consumer_app_id",
		"merchant_app_id",
	} {
		value, ok := noebs[key]
		text, isString := value.(string)
		if !ok || !isString || !strings.HasPrefix(text, "REPLACE_WITH_") {
			t.Fatalf("%s missing explicit noebs.%s placeholder", path, key)
		}
	}
	for _, rejected := range []string{
		"is_consumer_prod",
		"is_merchant_prod",
		"consumer_qa",
		"consumer_prod",
		"merchant_qa",
		"merchant_prod",
		"ipin_qa",
		"ipin_prod",
		"consumer_qa_id",
		"consumer_prod_id",
		"merchant_qa_id",
		"merchant_prod_id",
	} {
		if _, ok := noebs[rejected]; ok {
			t.Fatalf("%s must not use noebs.%s to derive EBS runtime endpoints", path, rejected)
		}
	}
}

func findMount(container manifestContainer, mountPath string) *manifestMount {
	for i := range container.VolumeMounts {
		if container.VolumeMounts[i].MountPath == mountPath {
			return &container.VolumeMounts[i]
		}
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func parseTerraformServiceCatalog(t *testing.T, path string) map[string]terraformServiceCatalogEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	entryRe := regexp.MustCompile(`^\s*"?([A-Za-z0-9_-]+)"?\s*=\s*\{\s*$`)
	portRe := regexp.MustCompile(`^\s*port\s*=\s*([0-9]+)\s*$`)
	protocolRe := regexp.MustCompile(`^\s*protocol\s*=\s*"([^"]+)"\s*$`)

	catalog := map[string]terraformServiceCatalogEntry{}
	inCatalog := false
	catalogDepth := 0
	currentName := ""
	current := terraformServiceCatalogEntry{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inCatalog {
			if trimmed == "noebs_service_catalog = {" {
				inCatalog = true
				catalogDepth = 1
			}
			continue
		}

		if currentName == "" && catalogDepth == 1 {
			if match := entryRe.FindStringSubmatch(line); len(match) == 2 {
				currentName = match[1]
				current = terraformServiceCatalogEntry{}
			}
		} else if currentName != "" {
			if match := portRe.FindStringSubmatch(line); len(match) == 2 {
				port, err := strconv.Atoi(match[1])
				if err != nil {
					t.Fatalf("parse Terraform port for %s: %v", currentName, err)
				}
				current.Port = port
			}
			if match := protocolRe.FindStringSubmatch(line); len(match) == 2 {
				current.Protocol = match[1]
			}
		}

		catalogDepth += strings.Count(line, "{")
		catalogDepth -= strings.Count(line, "}")
		if currentName != "" && catalogDepth == 1 {
			if current.Port == 0 || current.Protocol == "" {
				t.Fatalf("incomplete Terraform service catalog entry %s: %+v", currentName, current)
			}
			catalog[currentName] = current
			currentName = ""
		}
		if catalogDepth == 0 {
			break
		}
	}
	if len(catalog) == 0 {
		t.Fatalf("noebs_service_catalog not found in %s", path)
	}
	return catalog
}

func parseTerraformDatabaseCatalog(t *testing.T, path string) map[string]terraformDatabaseCatalogEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	entryRe := regexp.MustCompile(`^\s*"?([A-Za-z0-9_-]+)"?\s*=\s*\{\s*$`)
	databaseRe := regexp.MustCompile(`^\s*database\s*=\s*"([^"]+)"\s*$`)
	secretNameRe := regexp.MustCompile(`^\s*secret_name\s*=\s*"([^"]+)"\s*$`)
	migrationRoleRe := regexp.MustCompile(`^\s*migration_role\s*=\s*"([^"]+)"\s*$`)
	managedByRe := regexp.MustCompile(`^\s*managed_by\s*=\s*"([^"]+)"\s*$`)

	catalog := map[string]terraformDatabaseCatalogEntry{}
	inCatalog := false
	catalogDepth := 0
	currentName := ""
	current := terraformDatabaseCatalogEntry{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inCatalog {
			if trimmed == "noebs_database_catalog = {" {
				inCatalog = true
				catalogDepth = 1
			}
			continue
		}

		if currentName == "" && catalogDepth == 1 {
			if match := entryRe.FindStringSubmatch(line); len(match) == 2 {
				currentName = match[1]
				current = terraformDatabaseCatalogEntry{}
			}
		} else if currentName != "" {
			if match := databaseRe.FindStringSubmatch(line); len(match) == 2 {
				current.Database = match[1]
			}
			if match := secretNameRe.FindStringSubmatch(line); len(match) == 2 {
				current.SecretName = match[1]
			}
			if match := migrationRoleRe.FindStringSubmatch(line); len(match) == 2 {
				current.MigrationRole = match[1]
			}
			if match := managedByRe.FindStringSubmatch(line); len(match) == 2 {
				current.ManagedBy = match[1]
			}
		}

		catalogDepth += strings.Count(line, "{")
		catalogDepth -= strings.Count(line, "}")
		if currentName != "" && catalogDepth == 1 {
			if current.Database == "" || current.SecretName == "" {
				t.Fatalf("incomplete Terraform database catalog entry %s: %+v", currentName, current)
			}
			catalog[currentName] = current
			currentName = ""
		}
		if catalogDepth == 0 {
			break
		}
	}
	if len(catalog) == 0 {
		t.Fatalf("noebs_database_catalog not found in %s", path)
	}
	return catalog
}

func parseTerraformStringListLocal(t *testing.T, path, localName string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	lines := strings.Split(string(data), "\n")
	startRe := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(localName) + `\s*=\s*\[\s*$`)
	valueRe := regexp.MustCompile(`^\s*"([^"]+)"\s*,?\s*$`)
	values := map[string]bool{}
	inList := false
	for _, line := range lines {
		if !inList {
			if startRe.MatchString(line) {
				inList = true
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "]" {
			if len(values) == 0 {
				t.Fatalf("Terraform local %s is empty in %s", localName, path)
			}
			return values
		}
		if trimmed == "" {
			continue
		}
		match := valueRe.FindStringSubmatch(line)
		if len(match) != 2 {
			t.Fatalf("Terraform local %s has unsupported list item %q in %s", localName, line, path)
		}
		if values[match[1]] {
			t.Fatalf("Terraform local %s repeats %q in %s", localName, match[1], path)
		}
		values[match[1]] = true
	}
	t.Fatalf("Terraform local %s not found in %s", localName, path)
	return nil
}

func parseTerraformVariableBlocks(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	startRe := regexp.MustCompile(`^\s*variable\s+"([^"]+)"\s*\{\s*$`)
	blocks := map[string]string{}
	currentName := ""
	currentLines := []string{}
	depth := 0
	for _, line := range strings.Split(string(data), "\n") {
		if currentName == "" {
			match := startRe.FindStringSubmatch(line)
			if len(match) != 2 {
				continue
			}
			currentName = match[1]
			currentLines = []string{line}
			depth = strings.Count(line, "{") - strings.Count(line, "}")
			continue
		}

		currentLines = append(currentLines, line)
		depth += strings.Count(line, "{")
		depth -= strings.Count(line, "}")
		if depth == 0 {
			if blocks[currentName] != "" {
				t.Fatalf("Terraform variable %q is repeated in %s", currentName, path)
			}
			blocks[currentName] = strings.Join(currentLines, "\n")
			currentName = ""
			currentLines = nil
		}
	}
	if currentName != "" {
		t.Fatalf("Terraform variable %q is not closed in %s", currentName, path)
	}
	if len(blocks) == 0 {
		t.Fatalf("no Terraform variables found in %s", path)
	}
	return blocks
}

func requireTerraformServiceCatalogEntry(t *testing.T, catalog map[string]terraformServiceCatalogEntry, name string, port int, protocol string) {
	t.Helper()
	entry, ok := catalog[name]
	if !ok {
		t.Fatalf("Terraform service catalog missing %q", name)
	}
	if entry.Port != port || entry.Protocol != protocol {
		t.Fatalf("Terraform service catalog %s = %+v, want port=%d protocol=%s", name, entry, port, protocol)
	}
}

func parseNoebsServiceDatabases(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	databaseRe := regexp.MustCompile(`CREATE DATABASE ([a-z_]+) OWNER noebs`)
	matches := databaseRe.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("%s declares no noebs service databases", path)
	}
	databases := make([]string, 0, len(matches))
	for _, match := range matches {
		databases = append(databases, match[1])
	}
	return databases
}

func requireTerraformDatabaseCatalogEntry(t *testing.T, catalog map[string]terraformDatabaseCatalogEntry, name string, want terraformDatabaseCatalogEntry) {
	t.Helper()
	entry, ok := catalog[name]
	if !ok {
		t.Fatalf("Terraform database catalog missing %q", name)
	}
	if entry.Database != want.Database {
		t.Fatalf("Terraform database catalog %s database = %q, want %q", name, entry.Database, want.Database)
	}
	if entry.SecretName != want.SecretName {
		t.Fatalf("Terraform database catalog %s secret_name = %q, want %q", name, entry.SecretName, want.SecretName)
	}
	if entry.MigrationRole != want.MigrationRole {
		t.Fatalf("Terraform database catalog %s migration_role = %q, want %q", name, entry.MigrationRole, want.MigrationRole)
	}
	if entry.ManagedBy != want.ManagedBy {
		t.Fatalf("Terraform database catalog %s managed_by = %q, want %q", name, entry.ManagedBy, want.ManagedBy)
	}
}
