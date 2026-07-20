package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

const tenantCatalogMigrationTarget = `noebs-(identity-auth|card-vault|ebs-adapter|psp-webhook|admin-reporting|notification-chat|wallet-ledger)-migrate`

func TestTenantCatalogKubernetesMountContract(t *testing.T) {
	base := filepath.Join("..", "deploy", "kubernetes", "base")
	authority := filepath.Join("..", "deploy", "kubernetes", "keycloak-authority")
	authorityData, err := os.ReadFile(filepath.Join(authority, "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var authorityKustomization struct {
		GeneratorOptions struct {
			Immutable bool `yaml:"immutable"`
		} `yaml:"generatorOptions"`
		ConfigMapGenerators []struct {
			Name  string   `yaml:"name"`
			Files []string `yaml:"files"`
		} `yaml:"configMapGenerator"`
	}
	if err := yaml.Unmarshal(authorityData, &authorityKustomization); err != nil {
		t.Fatal(err)
	}
	if !authorityKustomization.GeneratorOptions.Immutable {
		t.Fatal("Keycloak authority ConfigMaps must be immutable")
	}
	wantGenerators := []struct {
		name string
		file string
	}{
		{name: "tenant-catalog", file: "tenant-catalog.yaml"},
		{name: "keycloak-desired-state", file: "keycloak-desired-state.yaml"},
	}
	if len(authorityKustomization.ConfigMapGenerators) != len(wantGenerators) {
		t.Fatalf("Keycloak authority generators = %#v", authorityKustomization.ConfigMapGenerators)
	}
	for index, want := range wantGenerators {
		got := authorityKustomization.ConfigMapGenerators[index]
		if got.Name != want.name || !slices.Equal(got.Files, []string{want.file}) {
			t.Fatalf("Keycloak authority generator %d = %#v", index, got)
		}
	}

	data, err := os.ReadFile(filepath.Join(base, "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var kustomization struct {
		Resources []string `yaml:"resources"`
		Patches   []struct {
			Target struct {
				Kind string `yaml:"kind"`
				Name string `yaml:"name"`
			} `yaml:"target"`
			Patch string `yaml:"patch"`
		} `yaml:"patches"`
	}
	if err := yaml.Unmarshal(data, &kustomization); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(kustomization.Resources, "../keycloak-authority") {
		t.Fatal("Kubernetes base does not consume the canonical Keycloak authority generator")
	}

	var migrationPatch string
	var gatewayPatch string
	for _, candidate := range kustomization.Patches {
		if candidate.Target.Kind == "Job" && candidate.Target.Name == tenantCatalogMigrationTarget {
			migrationPatch = candidate.Patch
		}
		if candidate.Target.Kind == "Deployment" && candidate.Target.Name == "api-gateway" {
			gatewayPatch = candidate.Patch
		}
	}
	if migrationPatch == "" {
		t.Fatalf("missing exact migration tenant catalog target %q", tenantCatalogMigrationTarget)
	}
	if gatewayPatch == "" {
		t.Fatal("missing API gateway tenant catalog patch")
	}
	assertTenantCatalogPatch(t, migrationPatch, "migration")
	assertTenantCatalogPatch(t, gatewayPatch, "API gateway")

	preflight := decodeCatalogWorkload(t, filepath.Join(base, "preflight-job.yaml"))
	requireConfigMapFileMount(t, preflight, "tenant-catalog", "tenant-catalog", "/preflight/tenant-catalog.yaml", "tenant-catalog.yaml")
	requireSecretFileMount(t, preflight, "release-manifest", "noebs-release-manifest", "/preflight/release-manifest.yaml", "release-manifest.yaml")

	reconciler := decodeCatalogWorkload(t, filepath.Join(base, "keycloak-reconcile-job.yaml"))
	requireConfigMapFileMount(t, reconciler, "tenant-catalog", "tenant-catalog", "/etc/noebs-keycloak/tenant-catalog.yaml", "tenant-catalog.yaml")
	if !slices.Contains(reconciler.Spec.Template.Spec.Containers[0].Args, "--tenant-catalog") ||
		!slices.Contains(reconciler.Spec.Template.Spec.Containers[0].Args, "/etc/noebs-keycloak/tenant-catalog.yaml") {
		t.Fatalf("Keycloak reconciler args = %v", reconciler.Spec.Template.Spec.Containers[0].Args)
	}
}

func assertTenantCatalogPatch(t testing.TB, patch, label string) {
	t.Helper()
	var operations []struct {
		Op    string `yaml:"op"`
		Path  string `yaml:"path"`
		Value struct {
			Name      string `yaml:"name"`
			MountPath string `yaml:"mountPath"`
			SubPath   string `yaml:"subPath"`
			ReadOnly  bool   `yaml:"readOnly"`
			ConfigMap *struct {
				Name string `yaml:"name"`
			} `yaml:"configMap"`
		} `yaml:"value"`
	}
	if err := yaml.Unmarshal([]byte(patch), &operations); err != nil {
		t.Fatal(err)
	}
	if len(operations) != 2 || operations[0].Op != "add" || operations[0].Path != "/spec/template/spec/containers/0/volumeMounts/-" ||
		operations[0].Value.Name != "tenant-catalog" || operations[0].Value.MountPath != "/app/tenant-catalog.yaml" ||
		operations[0].Value.SubPath != "tenant-catalog.yaml" || !operations[0].Value.ReadOnly {
		t.Fatalf("tenant catalog %s mount patch = %#v", label, operations)
	}
	if operations[1].Op != "add" || operations[1].Path != "/spec/template/spec/volumes/-" ||
		operations[1].Value.Name != "tenant-catalog" || operations[1].Value.ConfigMap == nil ||
		operations[1].Value.ConfigMap.Name != "tenant-catalog" {
		t.Fatalf("tenant catalog %s volume patch = %#v", label, operations[1])
	}
}

func TestTenantCatalogDockerMigrationMountContract(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	wantMount := "./deploy/kubernetes/keycloak-authority/tenant-catalog.yaml:/app/tenant-catalog.yaml:ro"
	for _, service := range []string{
		"api-gateway",
		"identity-auth-migrate", "card-vault-migrate", "ebs-adapter-migrate", "psp-webhook-migrate",
		"admin-reporting-migrate", "notification-chat-migrate", "wallet-ledger-migrate",
	} {
		if !slices.Contains(compose.Services[service].Volumes, wantMount) {
			t.Errorf("%s volumes = %v, want %q", service, compose.Services[service].Volumes, wantMount)
		}
	}
	for _, service := range []string{"workload-auth-migrate", "gateway-auth-migrate"} {
		if slices.Contains(compose.Services[service].Volumes, wantMount) {
			t.Errorf("%s must not mount application tenant catalog", service)
		}
	}
}

type catalogWorkload struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Args         []string `yaml:"args"`
					VolumeMounts []struct {
						Name      string `yaml:"name"`
						MountPath string `yaml:"mountPath"`
						SubPath   string `yaml:"subPath"`
						ReadOnly  bool   `yaml:"readOnly"`
					} `yaml:"volumeMounts"`
				} `yaml:"containers"`
				Volumes []struct {
					Name      string `yaml:"name"`
					ConfigMap *struct {
						Name string `yaml:"name"`
					} `yaml:"configMap"`
					Secret *struct {
						SecretName string `yaml:"secretName"`
					} `yaml:"secret"`
				} `yaml:"volumes"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func decodeCatalogWorkload(t testing.TB, path string) catalogWorkload {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var workload catalogWorkload
	if err := yaml.Unmarshal(data, &workload); err != nil {
		t.Fatal(err)
	}
	return workload
}

func requireConfigMapFileMount(t testing.TB, workload catalogWorkload, volumeName, configMapName, mountPath, subPath string) {
	t.Helper()
	requireCatalogFileMount(t, workload, volumeName, mountPath, subPath)
	for _, volume := range workload.Spec.Template.Spec.Volumes {
		if volume.Name == volumeName && volume.ConfigMap != nil && volume.ConfigMap.Name == configMapName {
			return
		}
	}
	t.Fatalf("missing ConfigMap volume %s from %s", volumeName, configMapName)
}

func requireSecretFileMount(t testing.TB, workload catalogWorkload, volumeName, secretName, mountPath, subPath string) {
	t.Helper()
	requireCatalogFileMount(t, workload, volumeName, mountPath, subPath)
	for _, volume := range workload.Spec.Template.Spec.Volumes {
		if volume.Name == volumeName && volume.Secret != nil && volume.Secret.SecretName == secretName {
			return
		}
	}
	t.Fatalf("missing Secret volume %s from %s", volumeName, secretName)
}

func requireCatalogFileMount(t testing.TB, workload catalogWorkload, volumeName, mountPath, subPath string) {
	t.Helper()
	for _, mount := range workload.Spec.Template.Spec.Containers[0].VolumeMounts {
		if mount.Name == volumeName && mount.MountPath == mountPath && mount.SubPath == subPath && mount.ReadOnly {
			return
		}
	}
	t.Fatalf("missing read-only mount %s at %s", volumeName, mountPath)
}
