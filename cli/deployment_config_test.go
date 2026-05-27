package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type manifestObject struct {
	Kind     string            `yaml:"kind"`
	Data     map[string]string `yaml:"data"`
	Metadata struct {
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
				RestartPolicy  string              `yaml:"restartPolicy"`
				Containers     []manifestContainer `yaml:"containers"`
				InitContainers []manifestContainer `yaml:"initContainers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
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
	Name         string           `yaml:"name"`
	Image        string           `yaml:"image"`
	Command      []string         `yaml:"command"`
	Args         []string         `yaml:"args"`
	Env          []map[string]any `yaml:"env"`
	EnvFrom      []map[string]any `yaml:"envFrom"`
	VolumeMounts []manifestMount  `yaml:"volumeMounts"`
}

type manifestMount struct {
	MountPath string `yaml:"mountPath"`
	SubPath   string `yaml:"subPath"`
}

type composeDocument struct {
	Services map[string]composeService `yaml:"services"`
	Secrets  map[string]composeSecret  `yaml:"secrets"`
}

type composeService struct {
	Environment any             `yaml:"environment"`
	EnvFile     any             `yaml:"env_file"`
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
		ServiceDiscovery     map[string]string `yaml:"service_discovery"`
		GRPCServiceDiscovery map[string]string `yaml:"grpc_service_discovery"`
		TemporalHost         string            `yaml:"temporal_host"`
		TemporalPort         string            `yaml:"temporal_port"`
	} `yaml:"noebs"`
}

type terraformServiceCatalogEntry struct {
	Port     int
	Protocol string
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
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no noebs Kubernetes containers were checked")
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
	requireComposeSecret(t, "secrets-init", secretsInit.Secrets, "postgres-bootstrap-secrets", "/app/secrets.yaml")
	requireComposeTopLevelSecret(t, compose.Secrets, "postgres-bootstrap-secrets", "./secrets.yaml")

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

func TestArgoCDApplicationTargetsCurrentHostOverlay(t *testing.T) {
	path := filepath.Join("..", "deploy", "argocd", "noebs.yaml")
	objects := decodeManifestObjects(t, path)

	var projectFound bool
	var appFound bool
	for _, object := range objects {
		switch object.Kind {
		case "AppProject":
			if object.Metadata.Name == "noebs" {
				projectFound = true
				if object.Metadata.Namespace != "argocd" {
					t.Fatalf("AppProject namespace = %q, want argocd", object.Metadata.Namespace)
				}
			}
		case "Application":
			if object.Metadata.Name != "noebs" {
				continue
			}
			appFound = true
			if object.Metadata.Namespace != "argocd" {
				t.Fatalf("Application namespace = %q, want argocd", object.Metadata.Namespace)
			}
			if object.Spec.Project != "noebs" {
				t.Fatalf("Application project = %q, want noebs", object.Spec.Project)
			}
			if object.Spec.Source.RepoURL != "https://github.com/adonese/noebs.git" {
				t.Fatalf("Application repoURL = %q", object.Spec.Source.RepoURL)
			}
			if object.Spec.Source.TargetRevision != "master" {
				t.Fatalf("Application targetRevision = %q, want master", object.Spec.Source.TargetRevision)
			}
			if object.Spec.Source.Path != "deploy/kubernetes/overlays/current-host" {
				t.Fatalf("Application path = %q, want deploy/kubernetes/overlays/current-host", object.Spec.Source.Path)
			}
			if _, err := os.Stat(filepath.Join("..", object.Spec.Source.Path, "kustomization.yaml")); err != nil {
				t.Fatalf("Application path does not contain kustomization.yaml: %v", err)
			}
			if object.Spec.Destination.Server != "https://kubernetes.default.svc" {
				t.Fatalf("Application destination server = %q", object.Spec.Destination.Server)
			}
			if object.Spec.Destination.Namespace != "noebs" {
				t.Fatalf("Application destination namespace = %q, want noebs", object.Spec.Destination.Namespace)
			}
			if !object.Spec.SyncPolicy.Automated.Prune || !object.Spec.SyncPolicy.Automated.SelfHeal {
				t.Fatalf("Application automated sync must enable prune and selfHeal")
			}
			if !containsString(object.Spec.SyncPolicy.SyncOptions, "PruneLast=true") {
				t.Fatalf("Application syncOptions = %v, want PruneLast=true", object.Spec.SyncPolicy.SyncOptions)
			}
		}
	}
	if !projectFound {
		t.Fatalf("noebs AppProject not found")
	}
	if !appFound {
		t.Fatalf("noebs Application not found")
	}
}

func TestMigrationJobsAreArgoPreSyncHooks(t *testing.T) {
	path := filepath.Join("..", "deploy", "kubernetes", "base", "migrate-job.yaml")
	objects := decodeManifestObjects(t, path)

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

	for _, object := range objects {
		if object.Kind != "Job" {
			continue
		}
		if _, ok := expectedJobs[object.Metadata.Name]; !ok {
			t.Fatalf("unexpected migration Job %q", object.Metadata.Name)
		}
		expectedJobs[object.Metadata.Name] = true
		if object.Metadata.Annotations["argocd.argoproj.io/hook"] != "PreSync" {
			t.Fatalf("%s hook = %q, want PreSync", object.Metadata.Name, object.Metadata.Annotations["argocd.argoproj.io/hook"])
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
		if serviceMount == nil || !strings.HasSuffix(serviceMount.SubPath, "-migrate.service.yaml") {
			t.Fatalf("%s service mount = %#v, want migrate service config", object.Metadata.Name, serviceMount)
		}
		requireMount(t, object.Metadata.Name, container, "/app/config.yaml", "config.yaml")
		requireMount(t, object.Metadata.Name, container, "/app/secrets.yaml", "secrets.yaml")
	}

	for job, found := range expectedJobs {
		if !found {
			t.Fatalf("migration Job %q not found", job)
		}
	}
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
	case "wallet-ledger-migrate", "wallet-worker":
		return "wallet-ledger-secrets"
	default:
		return serviceName + "-secrets"
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
