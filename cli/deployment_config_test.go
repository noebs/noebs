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
	Entrypoint  []string        `yaml:"entrypoint"`
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
		RenderDBPasswordFile string            `yaml:"render_db_password_file"`
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
	if !foundTemporal {
		t.Fatalf("temporal Deployment not found")
	}
	if !foundTemporalUI {
		t.Fatalf("temporal-ui Deployment not found")
	}
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-postgres-bootstrap start.sh", postgresBootstrap, filepath.Join("..", "deploy", "docker", "temporal", "postgres-start.sh"))
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-config temporal.yaml", temporalConfig["temporal.yaml"], filepath.Join("..", "deploy", "docker", "temporal", "temporal.yaml"))
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-config temporal-start.sh", temporalConfig["temporal-start.sh"], filepath.Join("..", "deploy", "docker", "temporal", "temporal-start.sh"))
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
		case "Job":
			if !strings.HasPrefix(object.Metadata.Name, "noebs-") {
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
			if serviceMount == nil || !strings.HasSuffix(serviceMount.SubPath, "-migrate.service.yaml") {
				t.Fatalf("%s service mount = %#v, want migrate service config", object.Metadata.Name, serviceMount)
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
