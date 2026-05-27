package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type manifestObject struct {
	Kind     string `yaml:"kind"`
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

type manifestContainer struct {
	Name         string           `yaml:"name"`
	Image        string           `yaml:"image"`
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

func TestNoebsDockerComposeServicesUseMountedConfigFiles(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
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
		requireComposeSecret(t, serviceName, service.Secrets, "noebs_secrets", "/app/secrets.yaml")
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
