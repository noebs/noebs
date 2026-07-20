package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type manifestObject struct {
	Kind                         string            `yaml:"kind"`
	AutomountServiceAccountToken *bool             `yaml:"automountServiceAccountToken"`
	ImagePullSecrets             []manifestRef     `yaml:"imagePullSecrets"`
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
		PodSelector manifestLabelSelector          `yaml:"podSelector"`
		PolicyTypes []string                       `yaml:"policyTypes"`
		Ingress     []manifestNetworkPolicyIngress `yaml:"ingress"`
		TLS         []manifestIngressTLS           `yaml:"tls"`
		Rules       []manifestIngressRule          `yaml:"rules"`
		Ports       []manifestServicePort          `yaml:"ports"`
		SyncPolicy  struct {
			Automated struct {
				Prune    bool `yaml:"prune"`
				SelfHeal bool `yaml:"selfHeal"`
			} `yaml:"automated"`
			SyncOptions []string `yaml:"syncOptions"`
		} `yaml:"syncPolicy"`
		Template    manifestPodTemplate `yaml:"template"`
		JobTemplate struct {
			Spec struct {
				Template manifestPodTemplate `yaml:"template"`
			} `yaml:"spec"`
		} `yaml:"jobTemplate"`
	} `yaml:"spec"`
}

type manifestPodTemplate struct {
	Spec manifestPodSpec `yaml:"spec"`
}

type manifestPodSpec struct {
	ServiceAccountName           string              `yaml:"serviceAccountName"`
	AutomountServiceAccountToken *bool               `yaml:"automountServiceAccountToken"`
	RestartPolicy                string              `yaml:"restartPolicy"`
	Containers                   []manifestContainer `yaml:"containers"`
	InitContainers               []manifestContainer `yaml:"initContainers"`
	Volumes                      []manifestVolume    `yaml:"volumes"`
}

type manifestLabelSelector struct {
	MatchLabels      map[string]string                  `yaml:"matchLabels"`
	MatchExpressions []manifestLabelSelectorRequirement `yaml:"matchExpressions"`
}

type manifestLabelSelectorRequirement struct {
	Key      string   `yaml:"key"`
	Operator string   `yaml:"operator"`
	Values   []string `yaml:"values"`
}

type manifestNetworkPolicyIngress struct {
	From  []manifestNetworkPolicyPeer `yaml:"from"`
	Ports []manifestNetworkPolicyPort `yaml:"ports"`
}

type manifestNetworkPolicyPeer struct {
	PodSelector *manifestLabelSelector `yaml:"podSelector"`
	IPBlock     *struct {
		CIDR   string   `yaml:"cidr"`
		Except []string `yaml:"except"`
	} `yaml:"ipBlock"`
}

type manifestNetworkPolicyPort struct {
	Protocol string `yaml:"protocol"`
	Port     int    `yaml:"port"`
}

type manifestRef struct {
	Name string `yaml:"name"`
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
	Name            string           `yaml:"name"`
	Image           string           `yaml:"image"`
	ImagePullPolicy string           `yaml:"imagePullPolicy"`
	Command         []string         `yaml:"command"`
	Args            []string         `yaml:"args"`
	Env             []map[string]any `yaml:"env"`
	EnvFrom         []map[string]any `yaml:"envFrom"`
	Ports           []map[string]any `yaml:"ports"`
	ReadinessProbe  map[string]any   `yaml:"readinessProbe"`
	LivenessProbe   map[string]any   `yaml:"livenessProbe"`
	StartupProbe    map[string]any   `yaml:"startupProbe"`
	VolumeMounts    []manifestMount  `yaml:"volumeMounts"`
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
	Volumes  map[string]any            `yaml:"volumes"`
	Networks map[string]composeNetwork `yaml:"networks"`
}

type composeService struct {
	Image       string                       `yaml:"image"`
	Restart     string                       `yaml:"restart"`
	Environment any                          `yaml:"environment"`
	EnvFile     any                          `yaml:"env_file"`
	Entrypoint  []string                     `yaml:"entrypoint"`
	DependsOn   map[string]composeDependency `yaml:"depends_on"`
	Healthcheck *composeHealthcheck          `yaml:"healthcheck"`
	Ports       []string                     `yaml:"ports"`
	Profiles    []string                     `yaml:"profiles"`
	Volumes     []string                     `yaml:"volumes"`
	Secrets     []composeSecret              `yaml:"secrets"`
	Networks    yaml.Node                    `yaml:"networks"`
}

type composeNetwork struct {
	Internal bool `yaml:"internal"`
}

type composeDependency struct {
	Condition string `yaml:"condition"`
}

type composeHealthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval"`
	Timeout     string   `yaml:"timeout"`
	Retries     int      `yaml:"retries"`
	StartPeriod string   `yaml:"start_period"`
}

type composeSecret struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
	File   string `yaml:"file"`
}

type currentHostKustomization struct {
	Images []struct {
		Name    string `yaml:"name"`
		NewName string `yaml:"newName"`
		NewTag  string `yaml:"newTag"`
		Digest  string `yaml:"digest"`
	} `yaml:"images"`
	Patches []struct {
		Target struct {
			Kind string `yaml:"kind"`
			Name string `yaml:"name"`
		} `yaml:"target"`
		Patch string `yaml:"patch"`
	} `yaml:"patches"`
}

type mountedNoebsConfig struct {
	Noebs struct {
		DatabaseDriver                             string            `yaml:"db_driver"`
		OtelServiceName                            string            `yaml:"otel_service_name"`
		ServiceDiscovery                           map[string]string `yaml:"service_discovery"`
		GRPCServiceDiscovery                       map[string]string `yaml:"grpc_service_discovery"`
		KafkaBrokers                               []string          `yaml:"kafka_brokers"`
		KafkaTransactionTopic                      string            `yaml:"kafka_transaction_topic"`
		AdminReportingKafkaConsumerGroup           string            `yaml:"admin_reporting_kafka_consumer_group"`
		EBSTransactionEventPublisherBatchSize      int               `yaml:"ebs_transaction_event_publisher_batch_size"`
		EBSTransactionEventPublisherPollIntervalMs int               `yaml:"ebs_transaction_event_publisher_poll_interval_ms"`
		TemporalHost                               string            `yaml:"temporal_host"`
		TemporalPort                               string            `yaml:"temporal_port"`
		WalletEnabled                              bool              `yaml:"wallet_enabled"`
		WalletApprovalThreshold                    int64             `yaml:"wallet_approval_threshold"`
		WalletDefaultCurrency                      string            `yaml:"wallet_default_currency"`
		WalletHoldExpirySeconds                    int               `yaml:"wallet_hold_expiry_seconds"`
		WalletApprovalTimeoutSeconds               int               `yaml:"wallet_approval_timeout_seconds"`
		WalletManualTransferApprovalTimeoutSeconds int               `yaml:"wallet_manual_approval_timeout_seconds"`
		WalletPSPPollerCron                        string            `yaml:"wallet_psp_poller_cron"`
		WalletPSPPollerBatchSize                   int               `yaml:"wallet_psp_poller_batch_size"`
		WalletPSPPollerIntervalSeconds             int               `yaml:"wallet_psp_poller_interval_seconds"`
		WalletReconciliationCron                   string            `yaml:"wallet_reconciliation_cron"`
		WalletReconciliationBatchSize              int               `yaml:"wallet_reconciliation_batch_size"`
		WalletReconciliationLookbackHours          int               `yaml:"wallet_reconciliation_lookback_hours"`
	} `yaml:"noebs"`
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
			podSpec := manifestPodSpecForObject(object)
			for _, container := range append(podSpec.Containers, podSpec.InitContainers...) {
				if !strings.Contains(container.Image, "ghcr.io/noebs/noebs") {
					continue
				}
				if object.Kind == "Job" && (object.Metadata.Name == "noebs-deployment-preflight" || object.Metadata.Name == "noebs-keycloak-reconciler" || object.Metadata.Name == "temporal-namespace-bootstrap") {
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
				requireNoebsSecretVolume(t, object.Metadata.Name, container, podSpec.Volumes)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no noebs Kubernetes containers were checked")
	}
}

func TestNoebsKubernetesImagesUseNodeCache(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	checked := 0
	for _, object := range objects {
		podSpec := manifestPodSpecForObject(object)
		for _, container := range append(podSpec.Containers, podSpec.InitContainers...) {
			if !strings.Contains(container.Image, "ghcr.io/noebs/noebs:") {
				continue
			}
			checked++
			if !strings.HasSuffix(container.Image, ":master") {
				t.Fatalf("%s/%s image = %q; update the image pull invariant for non-master tags", object.Metadata.Name, container.Name, container.Image)
			}
			if container.ImagePullPolicy != "IfNotPresent" {
				t.Fatalf("%s/%s imagePullPolicy = %q, want IfNotPresent for cached Noebs image tag %q", object.Metadata.Name, container.Name, container.ImagePullPolicy, container.Image)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no Noebs Kubernetes images were checked")
	}
}

func TestKeycloakJobsGateOnVerifiedServiceAvailability(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "deploy", "kubernetes", "base", "keycloak-reconcile-job.yaml"),
		filepath.Join("..", "deploy", "kubernetes", "overlays", "bootstrap-current-host", "delete-bootstrap-client-job.yaml"),
	} {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var object manifestObject
		if err := yaml.Unmarshal(payload, &object); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if len(object.Spec.Template.Spec.InitContainers) != 1 {
			t.Fatalf("%s init containers = %d, want one Keycloak availability gate", path, len(object.Spec.Template.Spec.InitContainers))
		}
		gate := object.Spec.Template.Spec.InitContainers[0]
		if gate.Name != "wait-for-keycloak" || gate.ImagePullPolicy != "IfNotPresent" {
			t.Fatalf("%s Keycloak availability gate = %#v", path, gate)
		}
		if len(gate.Args) != 1 {
			t.Fatalf("%s Keycloak availability gate args = %v", path, gate.Args)
		}
		for _, required := range []string{
			"curl --fail --silent --cacert /etc/noebs-keycloak/ca.pem",
			"https://keycloak.noebs.svc.cluster.local:8443/auth/realms/master/.well-known/openid-configuration",
			"sleep 1",
		} {
			if !strings.Contains(gate.Args[0], required) {
				t.Fatalf("%s Keycloak availability gate missing %q", path, required)
			}
		}
		if strings.Contains(gate.Args[0], "--insecure") {
			t.Fatalf("%s Keycloak availability gate disables TLS verification", path)
		}
		requireMount(t, object.Metadata.Name, gate, "/etc/noebs-keycloak/ca.pem", "ca.pem")
	}
}

func TestCurrentHostOverlayPinsImagesAndBudgetsEveryWorkload(t *testing.T) {
	path := filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host", "kustomization.yaml")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var overlay currentHostKustomization
	if err := yaml.Unmarshal(payload, &overlay); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}

	digestPattern := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	images := make(map[string]string, len(overlay.Images))
	for _, image := range overlay.Images {
		if strings.TrimSpace(image.Name) == "" {
			t.Fatalf("%s has image with no source name", path)
		}
		if image.NewTag != "" {
			t.Fatalf("%s image %q uses mutable newTag %q", path, image.Name, image.NewTag)
		}
		if !digestPattern.MatchString(image.Digest) {
			t.Fatalf("%s image %q digest = %q", path, image.Name, image.Digest)
		}
		if _, exists := images[image.Name]; exists {
			t.Fatalf("%s transforms image %q more than once", path, image.Name)
		}
		images[image.Name] = image.Digest
	}

	type resourceOperation struct {
		Op    string `yaml:"op"`
		Path  string `yaml:"path"`
		Value struct {
			Requests map[string]string `yaml:"requests"`
			Limits   map[string]string `yaml:"limits"`
			Type     string            `yaml:"type"`
		} `yaml:"value"`
	}
	type resourcePatch struct {
		kind string
		name *regexp.Regexp
	}
	resourcePatches := make([]resourcePatch, 0, len(overlay.Patches))
	recreateCutoverTargets := map[string]bool{}
	for _, patch := range overlay.Patches {
		namePattern, err := regexp.Compile("^(?:" + patch.Target.Name + ")$")
		if err != nil {
			t.Fatalf("%s target name %q: %v", path, patch.Target.Name, err)
		}
		var operations []resourceOperation
		if err := yaml.Unmarshal([]byte(patch.Patch), &operations); err != nil {
			t.Fatalf("decode %s patch for %s/%s: %v", path, patch.Target.Kind, patch.Target.Name, err)
		}
		resourceOperations := make([]resourceOperation, 0, 1)
		for _, operation := range operations {
			if operation.Op == "add" && (operation.Path == "/spec/template/spec/containers/0/resources" ||
				operation.Path == "/spec/jobTemplate/spec/template/spec/containers/0/resources") {
				resourceOperations = append(resourceOperations, operation)
			}
			if operation.Op == "add" && operation.Path == "/spec/strategy" && operation.Value.Type == "Recreate" {
				recreateCutoverTargets[patch.Target.Name] = true
			}
		}
		if len(resourceOperations) == 0 {
			continue
		}
		if len(resourceOperations) != 1 {
			t.Fatalf("%s patch for %s/%s must add primary container resources exactly once", path, patch.Target.Kind, patch.Target.Name)
		}
		resourceOperation := resourceOperations[0]
		for _, resourceName := range []string{"cpu", "memory"} {
			if strings.TrimSpace(resourceOperation.Value.Requests[resourceName]) == "" {
				t.Fatalf("%s patch for %s/%s has no %s request", path, patch.Target.Kind, patch.Target.Name, resourceName)
			}
			if strings.TrimSpace(resourceOperation.Value.Limits[resourceName]) == "" {
				t.Fatalf("%s patch for %s/%s has no %s limit", path, patch.Target.Kind, patch.Target.Name, resourceName)
			}
		}
		resourcePatches = append(resourcePatches, resourcePatch{
			kind: patch.Target.Kind,
			name: namePattern,
		})
	}

	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	checked := 0
	for _, object := range objects {
		if !isKubernetesWorkloadKind(object.Kind) {
			continue
		}
		podSpec := manifestPodSpecForObject(object)
		containers := append(podSpec.Containers, podSpec.InitContainers...)
		if len(containers) == 0 {
			continue
		}
		checked++
		for _, container := range containers {
			repository := container.Image
			if at := strings.IndexByte(repository, '@'); at >= 0 {
				repository = repository[:at]
			}
			if slash, colon := strings.LastIndexByte(repository, '/'), strings.LastIndexByte(repository, ':'); colon > slash {
				repository = repository[:colon]
			}
			if _, ok := images[repository]; !ok {
				t.Fatalf("%s/%s image %q has no immutable current-host transform", object.Metadata.Name, container.Name, container.Image)
			}
		}

		matches := 0
		for _, patch := range resourcePatches {
			if patch.kind == object.Kind && patch.name.MatchString(object.Metadata.Name) {
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("%s/%s matches %d current-host resource patches, want 1", object.Kind, object.Metadata.Name, matches)
		}
	}
	if checked == 0 {
		t.Fatalf("no current-host workloads were checked")
	}
	if !recreateCutoverTargets[`(api-gateway|card-vault|identity-auth)`] {
		t.Fatalf("%s must use Recreate for the API gateway and schema-coupled identity/card cutover", path)
	}
}

func TestKubernetesNoebsImageReleaseIsLocalAndImmutable(t *testing.T) {
	workflowPath := filepath.Join("..", ".github", "workflows", "main.yml")
	if _, err := os.Stat(workflowPath); err == nil {
		t.Fatalf("%s must not define test or release authority", workflowPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s: %v", workflowPath, err)
	}

	documentPath := filepath.Join("..", "docs", "alpha-image-release.md")
	document, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatalf("read %s: %v", documentPath, err)
	}
	for _, required := range []string{
		"without relying on GitHub Actions",
		"`git archive`",
		"write-once",
		"full-SHA tag",
		"verified digest",
		"separate GitOps commit",
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("%s must contain %q", documentPath, required)
		}
	}

	scriptPath := filepath.Join("..", "scripts", "publish-alpha-image.sh")
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read %s: %v", scriptPath, err)
	}
	for _, required := range []string{
		`source SHA must be exactly 40 lowercase hexadecimal characters`,
		`git -C "$repo_root" archive --format=tar "$source_sha"`,
		`immutable release tag already exists`,
		`[[ $registry_digest == "$build_digest" ]]`,
		`schema_version: 1,`,
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("%s must contain %q", scriptPath, required)
		}
	}
	for _, forbidden := range []string{"docker login", ":master", "GITHUB_TOKEN"} {
		if strings.Contains(string(script), forbidden) {
			t.Fatalf("%s contains forbidden release behavior %q", scriptPath, forbidden)
		}
	}
}

func TestK3sExistingClusterEncryptionRunbookPreservesRecoveryAndOrdering(t *testing.T) {
	path := filepath.Join("..", "deploy", "host", "README.md")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(payload)
	steps := []string{
		`[[ "$k3s_version" =~ ^v1\.35\.([0-9]+)\+k3s[0-9]+$ ]]`,
		`((BASH_REMATCH[1] >= 3))`,
		`sudo systemctl stop k3s`,
		`sudo cp -a /var/lib/rancher/k3s/server/db "$backup_dir/db"`,
		`sudo install -m 0600 /var/lib/rancher/k3s/server/token`,
		`sudo test -s "$backup_dir/db/state.db"`,
		`sudo test -s "$backup_dir/server-token"`,
		`sudo systemctl start k3s`,
		`wait_for_k3s`,
		`test "$encryption_status" = 'Encryption Status: Disabled, no configuration file found'`,
		`sudo k3s secrets-encrypt enable`,
		`sudo install -m 0600 deploy/host/k3s-config.yaml`,
		`sudo systemctl restart k3s`,
		`wait_for_k3s`,
		`grep -Fx 'Current Rotation Stage: start'`,
		`.hashmatch == true`,
		`if ! sudo k3s secrets-encrypt rotate-keys; then`,
		`rotation_status="$(sudo k3s secrets-encrypt status)"`,
		`grep -Fx 'Encryption Status: Enabled' <<<"$rotation_status"`,
		`grep -Fx 'Current Rotation Stage: reencrypt_finished'`,
		`sudo systemctl restart k3s`,
		`wait_for_k3s`,
		`grep -Fx 'Current Rotation Stage: reencrypt_finished'`,
		`.activekey | startswith("XSalsa20-POLY1305 secretboxkey-")`,
	}
	cursor := 0
	for _, step := range steps {
		index := strings.Index(text[cursor:], step)
		if index < 0 {
			t.Fatalf("%s must contain %q after the preceding transition step", path, step)
		}
		cursor += index + len(step)
	}
	for _, command := range []string{"cat", "head", "tail", "less", "more", "echo", "printf", "sha256sum"} {
		forbidden := command + " /var/lib/rancher/k3s/server/token"
		if strings.Contains(text, forbidden) {
			t.Fatalf("%s exposes the K3s server token with %q", path, forbidden)
		}
	}
	for _, required := range []string{
		`sudo k3s kubectl get --raw=/readyz`,
		`sudo k3s kubectl wait --for=condition=Ready node --all`,
		`keys == ["activekey", "stage"]`,
		`grep -Fx 'Encryption Status: Disabled'`,
		`grep -Fx 'Server Encryption Hashes: All hashes match'`,
		`grep -Fx 'Encryption Status: Enabled'`,
		"https://docs.k3s.io/cli/secrets-encrypt#enable-secrets-encryption-on-an-existing-cluster",
		"https://docs.k3s.io/datastore/backup-restore",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("%s must cite %s", path, required)
		}
	}
}

func TestNoebsServiceAccountsUseGHCRPullSecret(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	serviceAccounts := map[string]manifestObject{}
	for _, object := range objects {
		if object.Kind == "ServiceAccount" {
			serviceAccounts[object.Metadata.Name] = object
		}
	}
	if len(serviceAccounts) == 0 {
		t.Fatalf("no Kubernetes ServiceAccounts were found")
	}

	checked := 0
	for _, object := range objects {
		if !isKubernetesWorkloadKind(object.Kind) || !workloadUsesNoebsImage(object) {
			continue
		}
		serviceAccountName := manifestPodSpecForObject(object).ServiceAccountName
		if serviceAccountName == "" {
			t.Fatalf("%s/%s does not declare serviceAccountName", object.Kind, object.Metadata.Name)
		}
		serviceAccount, ok := serviceAccounts[serviceAccountName]
		if !ok {
			t.Fatalf("%s/%s references missing ServiceAccount %q", object.Kind, object.Metadata.Name, serviceAccountName)
		}
		checked++
		if !manifestRefsContain(serviceAccount.ImagePullSecrets, "ghcr-credentials") {
			t.Fatalf("ServiceAccount %q must reference ghcr-credentials for %s/%s", serviceAccountName, object.Kind, object.Metadata.Name)
		}
	}
	if checked == 0 {
		t.Fatalf("no Noebs Kubernetes workloads were checked")
	}
}

func TestNoebsImageRequiresMountedRuntimeConfig(t *testing.T) {
	entrypoint, err := os.ReadFile(filepath.Join("..", "scripts", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("read entrypoint: %v", err)
	}
	entrypointText := string(entrypoint)
	for _, required := range []string{"/app/config.yaml", "/app/service.yaml", "/app/secrets.yaml"} {
		if !strings.Contains(entrypointText, required) {
			t.Fatalf("entrypoint does not require mounted %s", required)
		}
	}
	if strings.Contains(entrypointText, "/app/.sops") || strings.Contains(entrypointText, "age key") {
		t.Fatal("entrypoint must not require a runtime SOPS identity")
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
		"deploy/docker/postgres/service-role-passwords.env",
		"deploy/docker/postgres/ca.pem",
		"deploy/docker/postgres/tls.crt",
		"deploy/docker/postgres/tls.key",
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

func TestDockerComposePostgresRolePasswordExampleIsExact(t *testing.T) {
	path := filepath.Join("..", "deploy", "docker", "postgres", "service-role-passwords.env.example")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	expected := make(map[string]string, len(allPostgresRoleSpecs()))
	for _, spec := range allPostgresRoleSpecs() {
		expected[spec.username] = "REPLACE_WITH_" + strings.ToUpper(spec.username) + "_PASSWORD_BASE64URL"
	}
	actual := make(map[string]string, len(expected))
	for lineNumber, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		role, password, ok := strings.Cut(line, "=")
		if !ok || role == "" || password == "" {
			t.Fatalf("%s:%d is not an explicit role=password record", path, lineNumber+1)
		}
		if _, duplicate := actual[role]; duplicate {
			t.Fatalf("%s repeats role %s", path, role)
		}
		actual[role] = password
	}
	if len(actual) != len(expected) {
		t.Fatalf("%s role count = %d, want %d", path, len(actual), len(expected))
	}
	for role, password := range expected {
		if actual[role] != password {
			t.Fatalf("%s role %s password placeholder = %q, want %q", path, role, actual[role], password)
		}
	}
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
		"api-gateway":       {"api-gateway"},
		"identity-auth":     {"identity-auth"},
		"card-vault":        {"card-vault"},
		"ebs-adapter":       {"ebs-adapter"},
		"psp-webhook":       {"wallet-ledger"},
		"admin-reporting":   {"admin-reporting"},
		"notification-chat": {"notification-chat"},
		"wallet-api":        nil,
		"wallet-ledger":     {"wallet-ledger"},
		"wallet-worker":     {"wallet-ledger"},
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
	readOnlyRemoteScripts := map[string]bool{
		"alpha-post-deploy-smoke.sh": true,
	}
	for _, path := range scripts {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, token := range forbidden {
			if token == "ssh " && readOnlyRemoteScripts[filepath.Base(path)] {
				continue
			}
			if strings.Contains(text, token) {
				t.Fatalf("%s carries direct VM/Docker deployment behavior %q; deployment must go through Kubernetes/k3s and Argo CD", path, token)
			}
		}
		if readOnlyRemoteScripts[filepath.Base(path)] {
			for _, mutation := range []string{
				"kubectl apply",
				"kubectl delete",
				"kubectl patch",
				"kubectl set image",
				"kubectl rollout restart",
				"argocd app sync",
				"helm upgrade",
			} {
				if strings.Contains(text, mutation) {
					t.Fatalf("%s mutates the deployment with %q; post-deploy smoke must remain read-only", path, mutation)
				}
			}
		}
	}
}

func TestPostDeploySmokeCoversTheKeycloakAndRetiredEdgeBoundaries(t *testing.T) {
	path := filepath.Join("..", "scripts", "alpha-post-deploy-smoke.sh")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, required := range []string{
		`.spec.source.targetRevision // ""`,
		`.status.sync.revision // ""`,
		`issuer = origin + "/auth/realms/noebs"`,
		`issuer + "/.well-known/openid-configuration"`,
		`/auth/realms/master/.well-known/openid-configuration`,
		`/auth/admin/`,
		`id_token_signing_alg_values_supported`,
		`key.get("alg") == "RS256"`,
		`get ingress api-gateway`,
		`get secret noebs-tls`,
		`get configmap caddy-config`,
		`^caddy-config-[a-z0-9]+$`,
		`deployment/consumer-beneficiary`,
		`service/consumer-beneficiary`,
		`secret/consumer-beneficiary-secrets`,
		`secret/consumer-beneficiary-migrate-secrets`,
		`expected_roles(name)`,
		`wallet_ledger_webhook`,
		`expected_databases(name)`,
		`[[ "$topology_drift_count" == 0 ]]`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("%s missing auth cutover assertion %q", path, required)
		}
	}
}

func TestPostDeploySmokeRequiresExactFreshMigrationSets(t *testing.T) {
	path := filepath.Join("..", "scripts", "alpha-post-deploy-smoke.sh")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, required := range []string{
		`string_agg(version_id::text || chr(58) || is_applied::text, chr(44) ORDER BY version_id, id)`,
		`$identity_migrations|0:true,1:true|identity-auth`,
		`$card_vault_migrations|0:true,1:true|card-vault`,
		`$ebs_adapter_migrations|0:true,1:true|ebs-adapter`,
		`$admin_reporting_migrations|0:true,1:true|admin-reporting`,
		`$notification_chat_migrations|0:true,1:true|notification-chat`,
		`$wallet_ledger_migrations|0:true,1:true|wallet-ledger`,
		`$workload_auth_migrations|0:true,1:true|workload-auth`,
		`$gateway_auth_migrations|0:true,1:true|gateway-auth`,
		`migration set is $actual, want exactly $expected`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("%s missing exact fresh migration assertion %q", path, required)
		}
	}
	for _, forbidden := range []string{"MAX(version_id)", "want at least"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("%s retains non-exact migration assertion %q", path, forbidden)
		}
	}
}

func TestKeycloakEmptyStateCutoverHasOneExactDestructiveBoundary(t *testing.T) {
	path := filepath.Join("..", "deploy", "host", "keycloak-empty-state-cutover.md")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	steps := []string{
		`reencrypt_finished`,
		`create_noebs_application = false`,
		`tofu -chdir="$foundation_root" apply "$pause_plan"`,
		`scale deployment,statefulset --all --replicas=0`,
		`-replace=kubernetes_namespace_v1.noebs`,
		`test "$new_namespace_uid" != "$old_namespace_uid"`,
		`apply -f "$steady_secrets"`,
		`apply -f "$bootstrap_secrets"`,
		`noebs_manifest_path      = "deploy/kubernetes/overlays/bootstrap-current-host"`,
		`create_edge_application  = false`,
		`tofu -chdir="$foundation_root" apply "$bootstrap_plan"`,
		`noebs-keycloak-delete-bootstrap-client`,
		`test "$token_status" = 401`,
		`noebs_manifest_path = "deploy/kubernetes/overlays/current-host"`,
		`noebs-keycloak-reconciler`,
		`keycloak-bootstrap-admin keycloak-bootstrap-reconciler-credentials`,
		`create_edge_application = true`,
		`sudo install -d -m 0700`,
		`sudo chown -R -- 10001:10001`,
		`caddy_wrong_owner="$(sudo find`,
		`test -z "$caddy_wrong_owner"`,
		`sudo stat --format='%u:%g %a %n'`,
		`test "$caddy_host_path_status" = "$expected_caddy_host_path_status"`,
		`apply -f "$edge_internal_transport"`,
		`tofu -chdir="$foundation_root" apply "$edge_plan"`,
		`get application noebs-edge`,
		`delete configmap caddy-config --ignore-not-found`,
		`https://api.noebs.sd/auth/realms/noebs/.well-known/openid-configuration`,
		`https://api.noebs.sd/.well-known/assetlinks.json`,
		`deployment/consumer-beneficiary`,
		`retired_authority_count`,
		`scripts/alpha-post-deploy-smoke.sh "$RELEASE_COMMIT" "$RELEASE_DIGEST"`,
	}
	cursor := 0
	for _, step := range steps {
		index := strings.Index(text[cursor:], step)
		if index < 0 {
			t.Fatalf("%s must contain %q after the preceding cutover step", path, step)
		}
		cursor += index + len(step)
	}
	if !strings.Contains(text, `noebs render-edge-internal-transport "$RELEASE_ROOT" edge`) {
		t.Fatal("cutover must render the edge mTLS identity from the validated release")
	}
	for _, explanation := range []string{"local-peer authority marker", "Recreate all older", "main, Keycloak, and Temporal PostgreSQL claims"} {
		if !strings.Contains(text, explanation) {
			t.Fatalf("%s missing empty-state rationale %q", path, explanation)
		}
	}
	for _, gate := range []string{
		`: "${RELEASE_REPO_ROOT:?set the reviewed release checkout}"`,
		`foundation_root="$RELEASE_REPO_ROOT/foundation/terraform"`,
		`test -s "$foundation_root/terraform.tfstate"`,
		`grep -Fx 'Encryption Status: Enabled'`,
		`grep -Fx 'Current Rotation Stage: reencrypt_finished'`,
		`grep -Fx 'Server Encryption Hashes: All hashes match'`,
		`.activekey | startswith("XSalsa20-POLY1305 secretboxkey-")`,
		`"${kubectl[@]}" get --raw=/readyz`,
		`"${kubectl[@]}" wait --for=condition=Ready node --all`,
		`deploy/kubernetes/overlays/current-host/kustomization.yaml`,
		`deploy/kubernetes/overlays/bootstrap-current-host/kustomization.yaml`,
		`deploy/kubernetes/operations/lookup/kustomization.yaml`,
		`deploy/kubernetes/operations/memberships/base/kustomization.yaml`,
		`test "$pinned_digest" = "$RELEASE_DIGEST"`,
		`10001:10001 700 /var/lib/docker/volumes/noebs_caddy_data/_data`,
		`10001:10001 700 /var/lib/docker/volumes/noebs_caddy_config/_data`,
		`client_id=noebs-keycloak-bootstrap`,
		`/realms/master/protocol/openid-connect/token`,
	} {
		if !strings.Contains(text, gate) {
			t.Fatalf("%s missing fail-closed cutover gate %q", path, gate)
		}
	}
	if strings.Contains(text, "delete pvc") {
		t.Fatal("cutover must replace the foundation-owned namespace instead of juggling individual PVCs")
	}
}

func TestFoundationRunbookSanitizesLegacySecretStateWithoutPrintingIt(t *testing.T) {
	path := filepath.Join("..", "foundation", "terraform", "README.md")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, required := range []string{
		`legacy_foundation_root=/home/adonese/src/noebs-foundation/foundation/terraform`,
		`release_foundation_root="$RELEASE_REPO_ROOT/foundation/terraform"`,
		`test -s "$legacy_foundation_root/terraform.tfstate"`,
		`test -s "$legacy_foundation_root/terraform.tfvars.example"`,
		`test -s "$release_foundation_root/terraform.tfvars.example"`,
		`git -C "$legacy_repo_root" ls-files --error-unmatch`,
		`git -C "$RELEASE_REPO_ROOT" ls-files --error-unmatch`,
		`test ! -e "$release_foundation_root/terraform.tfstate"`,
		`test ! -e "$STATE_QUARANTINE"`,
		`mv -- "$legacy_foundation_root/terraform.tfstate"`,
		`chmod 0600 "$release_foundation_root/terraform.tfstate"`,
		`cmp -s "$STATE_QUARANTINE/pre-relocation.tfstate"`,
		`! -name 'terraform.tfvars.example'`,
		`tofu -chdir="$release_foundation_root" state pull`,
		`grep -Fx 'kubernetes_namespace_v1.noebs'`,
		`grep -Fx 'kubernetes_manifest.noebs_project'`,
		`awk '/(^|\.)data\.kubernetes_secret_v1\./'`,
		`state rm -dry-run`,
		`-backup="$STATE_QUARANTINE/state-rm.automatic-backup.tfstate"`,
		`"kubernetes_secret", "kubernetes_secret_v1"`,
		`! rg -n 'data "kubernetes_secret(_v1)?"' "$release_foundation_root"/*.tf`,
		`post-removal.tfplan`,
		`an empty-state plan is a hard stop`,
		`filesystem snapshots and external backups`,
		`cryptographic erasure`,
		"Do not run `tofu state show`, `tofu show`, `jq`",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("%s missing protected state migration contract %q", path, required)
		}
	}
	if strings.Count(text, `git -C "$RELEASE_REPO_ROOT" diff --quiet`) < 2 {
		t.Fatalf("%s must recheck the tracked release tree after artifact quarantine", path)
	}
}

func TestRepositoryDoesNotCarryLegacySingleHostDeploymentArtifacts(t *testing.T) {
	for _, path := range []string{
		"fly.toml",
		"litefs.yml",
		"litefs.static-lease.yml",
		"noebs-fly-litefs.conf",
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
		podSpec := manifestPodSpecForObject(object)
		if len(podSpec.Containers)+len(podSpec.InitContainers) == 0 {
			t.Fatalf("%s has no pod containers", workload)
		}
		checked++

		expectedServiceAccount := expectedServiceAccountForWorkload(t, object)
		serviceAccount := podSpec.ServiceAccountName
		if serviceAccount != expectedServiceAccount {
			t.Fatalf("%s serviceAccountName = %q, want %q", workload, serviceAccount, expectedServiceAccount)
		}
		if !serviceAccounts[serviceAccount] {
			t.Fatalf("%s references missing ServiceAccount %q", workload, serviceAccount)
		}
		usedServiceAccounts[serviceAccount] = true
		if podSpec.AutomountServiceAccountToken == nil || *podSpec.AutomountServiceAccountToken {
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
	if len(config.Noebs.KafkaBrokers) == 0 {
		t.Fatalf("kafka_brokers must be explicit in mounted config")
	}
	for i, endpoint := range config.Noebs.KafkaBrokers {
		serviceName, port := parseHostPortDiscoveryEndpoint(t, fmt.Sprintf("kafka_brokers[%d]", i), endpoint)
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

func TestKubernetesNetworkPoliciesDeclareIngressPorts(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	expected := map[string]struct {
		targetPod      string
		port           int
		allowedSources []string
	}{
		"api-gateway-ingress":         {targetPod: "api-gateway", port: 8080, allowedSources: []string{"ip:10.42.0.1/32"}},
		"postgres-ingress":            {targetPod: "postgres", port: 5432},
		"kafka-ingress":               {targetPod: "kafka", port: 9092, allowedSources: []string{"ebs-adapter-events", "admin-reporting-projector", "kafka-topics"}},
		"temporal-postgres-ingress":   {targetPod: "temporal-postgres", port: 5432, allowedSources: []string{"temporal", "temporal-schema-migrate"}},
		"temporal-frontend-ingress":   {targetPod: "temporal", port: 7233, allowedSources: []string{"wallet-ledger", "wallet-worker", "temporal-namespace-bootstrap"}},
		"keycloak-postgres-ingress":   {targetPod: "keycloak-postgres", port: 5432, allowedSources: []string{"keycloak"}},
		"keycloak-https-ingress":      {targetPod: "keycloak", port: 8443, allowedSources: []string{"ip:10.42.0.1/32", "api-gateway", "keycloak-reconciler", "temporal", "temporal-namespace-bootstrap", "wallet-ledger", "wallet-worker"}},
		"keycloak-management-ingress": {targetPod: "keycloak", port: 9000, allowedSources: []string{"ip:10.42.0.1/32"}},
		"identity-auth-ingress":       {targetPod: "identity-auth", port: 8080, allowedSources: []string{"api-gateway"}},
		"card-vault-ingress":          {targetPod: "card-vault", port: 8080, allowedSources: []string{"api-gateway", "ebs-adapter"}},
		"ebs-adapter-ingress":         {targetPod: "ebs-adapter", port: 8080, allowedSources: []string{"api-gateway"}},
		"psp-webhook-ingress":         {targetPod: "psp-webhook", port: 8080, allowedSources: []string{"api-gateway"}},
		"admin-reporting-ingress":     {targetPod: "admin-reporting", port: 8080, allowedSources: []string{"api-gateway"}},
		"notification-chat-ingress":   {targetPod: "notification-chat", port: 8080, allowedSources: []string{"api-gateway", "ebs-adapter"}},
		"wallet-api-ingress":          {targetPod: "wallet-api", port: 8080, allowedSources: []string{"api-gateway"}},
		"wallet-ledger-grpc-ingress":  {targetPod: "wallet-ledger", port: 9090, allowedSources: []string{"wallet-api"}},
	}
	defaultDenyFound := false
	found := map[string]bool{}
	for _, object := range objects {
		if object.Kind != "NetworkPolicy" {
			continue
		}
		want, ok := expected[object.Metadata.Name]
		if !ok {
			if object.Metadata.Name == "default-deny" {
				defaultDenyFound = true
				continue
			}
			if containsString(object.Spec.PolicyTypes, "Egress") && !containsString(object.Spec.PolicyTypes, "Ingress") {
				continue
			}
			t.Fatalf("unexpected ingress NetworkPolicy %q", object.Metadata.Name)
		}
		found[object.Metadata.Name] = true
		requirePortScopedIngressNetworkPolicy(t, object, want.targetPod, want.port, want.allowedSources)
	}
	if !defaultDenyFound {
		t.Fatalf("missing default-deny NetworkPolicy")
	}
	for name := range expected {
		if !found[name] {
			t.Fatalf("missing NetworkPolicy %q", name)
		}
	}
}

func TestPostgresNetworkPolicyMatchesExactDatabaseConsumers(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	consumers := make(map[string]bool)
	for _, spec := range allPostgresRoleSpecs() {
		if spec.service == "" {
			continue
		}
		name := string(spec.service)
		if spec.service.runsMigrations() || spec.service.cleansWorkloadAuthNonces() || spec.service.cleansGatewayAuthSessions() {
			name = "noebs-" + name
		}
		consumers[name] = true
	}
	for _, role := range serviceRoleCatalog {
		if roleReceivesSignedHTTP(role) {
			consumers[string(role)] = true
		}
	}
	want := make([]string, 0, len(consumers))
	for consumer := range consumers {
		want = append(want, consumer)
	}
	slices.Sort(want)

	var ingressConsumers []string
	var egressConsumers []string
	for _, object := range objects {
		switch object.Metadata.Name {
		case "postgres-ingress":
			if len(object.Spec.Ingress) != 1 || len(object.Spec.Ingress[0].From) != 1 || object.Spec.Ingress[0].From[0].PodSelector == nil {
				t.Fatalf("postgres ingress peers = %#v", object.Spec.Ingress)
			}
			ingressConsumers = exactNetworkPolicySelectorValues(t, "postgres ingress", *object.Spec.Ingress[0].From[0].PodSelector)
		case "postgres-egress":
			egressConsumers = exactNetworkPolicySelectorValues(t, "postgres egress", object.Spec.PodSelector)
		}
	}
	slices.Sort(ingressConsumers)
	slices.Sort(egressConsumers)
	if !slices.Equal(ingressConsumers, want) {
		t.Fatalf("postgres ingress consumers = %v, want %v", ingressConsumers, want)
	}
	if !slices.Equal(egressConsumers, want) {
		t.Fatalf("postgres egress consumers = %v, want %v", egressConsumers, want)
	}
}

func TestKubernetesAPIGatewayTargetsHaveIngressPolicies(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	policiesByTarget := networkPoliciesByTargetPod(objects)

	targetRoles := map[string]bool{}
	for _, spec := range gatewayProxyRouteSpecs() {
		targetRoles[string(spec.role)] = true
	}
	for role := range targetRoles {
		policy, ok := policiesByTarget[role]
		if !ok {
			t.Fatalf("gateway target role %s has no NetworkPolicy", role)
		}
		requireIngressNetworkPolicyAllows(t, policy, "api-gateway")
	}
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
		parsed, err := url.Parse(endpoint)
		if err != nil {
			t.Fatalf("service_discovery.%s = %q: %v", role, endpoint, err)
		}
		requireTerraformServiceCatalogEntry(t, catalog, name, port, parsed.Scheme)
	}
	for role, endpoint := range config.Noebs.GRPCServiceDiscovery {
		name, port := parseHostPortDiscoveryEndpoint(t, role, endpoint)
		requireTerraformServiceCatalogEntry(t, catalog, name, port, "grpc")
	}
	for i, endpoint := range config.Noebs.KafkaBrokers {
		name, port := parseHostPortDiscoveryEndpoint(t, fmt.Sprintf("kafka_brokers[%d]", i), endpoint)
		requireTerraformServiceCatalogEntry(t, catalog, name, port, "kafka")
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
		switch serviceName {
		case "workload-auth":
			requireTerraformDatabaseCatalogEntry(t, catalog, serviceName, terraformDatabaseCatalogEntry{
				Database:      database,
				SecretName:    "workload-auth-migrate-secrets",
				MigrationRole: "workload-auth-migrate",
			})
			continue
		case "gateway-auth":
			requireTerraformDatabaseCatalogEntry(t, catalog, "api-gateway", terraformDatabaseCatalogEntry{
				Database:      database,
				SecretName:    "api-gateway-secrets",
				MigrationRole: "gateway-auth-migrate",
			})
			continue
		}
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

func TestRequiredKubernetesSecretDocsListEveryCutoverSecret(t *testing.T) {
	required := map[string]string{
		"postgres-credentials":                     "ca.pem",
		"service-postgres-roles":                   "passwords.env",
		"workload-auth-postgres-roles":             "roles.yaml",
		"gateway-auth-postgres-roles":              "roles.yaml",
		"internal-transport-platform":              "credentials.yaml",
		"temporal-postgres-credentials":            "tls.crt",
		"temporal-server-credentials":              "tls.crt",
		"temporal-namespace-bootstrap-credentials": "client-secret",
		"keycloak-postgres-credentials":            "password",
		"keycloak-secrets":                         "keycloak.conf",
		"keycloak-reconciler-credentials":          "config.yaml",
		"ghcr-credentials":                         ".dockerconfigjson",
	}
	for _, source := range kubernetesServiceSecretSources {
		required[source.secretName] = "secrets.yaml"
	}

	docs := []string{
		filepath.Join("..", "foundation", "terraform", "README.md"),
		filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host", "README.md"),
	}
	for _, path := range docs {
		t.Run(path, func(t *testing.T) {
			payload, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			text := string(payload)
			for secretName, key := range required {
				if !strings.Contains(text, "`"+secretName+"`") {
					t.Fatalf("%s missing required secret %s", path, secretName)
				}
				if key != "" && !strings.Contains(text, "`"+key+"`") {
					t.Fatalf("%s missing required key %s for secret %s", path, key, secretName)
				}
			}
		})
	}
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
			if container.Image != "postgres@sha256:33f923b05f64ca54ac4401c01126a6b92afe839a0aa0a52bc5aeb5cc958e5f20" {
				t.Fatalf("keycloak-postgres image is not the tested immutable digest: %q", container.Image)
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
		if container.Image != "quay.io/keycloak/keycloak@sha256:2eb3cd316835c990e69e26ade292ffa78f6fb0db7d5fc6377463c162e1979ac0" {
			t.Fatalf("keycloak image is not the tested immutable digest: %q", container.Image)
		}
		if !containsString(container.Args, "start") {
			t.Fatalf("keycloak args = %v, want start", container.Args)
		}
		if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
			t.Fatalf("keycloak must use mounted keycloak.conf instead of env/envFrom")
		}
		requireMount(t, "keycloak", container, "/opt/keycloak/conf/keycloak.conf", "keycloak.conf")
		requireMount(t, "keycloak", container, "/opt/keycloak/conf/tls.crt", "tls.crt")
		requireMount(t, "keycloak", container, "/opt/keycloak/conf/tls.key", "tls.key")
	}

	requireKubernetesServicePort(t, services, "keycloak", 8443)
	if services["keycloak"][8080] || services["keycloak"][9000] {
		t.Fatalf("Keycloak Service exposes plaintext application or management ports: %v", services["keycloak"])
	}
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

func TestKeycloakBootstrapCredentialsAreIsolatedFromSteadyDeployment(t *testing.T) {
	steadyPaths := []string{
		filepath.Join("..", "deploy", "kubernetes", "base", "keycloak.yaml"),
		filepath.Join("..", "deploy", "kubernetes", "base", "keycloak.conf.example"),
		filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host", "kustomization.yaml"),
		filepath.Join("..", "deploy", "docker", "keycloak", "keycloak.conf.example"),
	}
	for _, path := range steadyPaths {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, forbidden := range []string{
			"bootstrap-admin-username",
			"bootstrap-admin-password",
			"bootstrap-admin-client-id",
			"bootstrap-admin-client-secret",
			"KC_BOOTSTRAP_ADMIN_USERNAME",
			"KC_BOOTSTRAP_ADMIN_PASSWORD",
			"KC_BOOTSTRAP_ADMIN_CLIENT_ID",
			"KC_BOOTSTRAP_ADMIN_CLIENT_SECRET",
		} {
			if strings.Contains(string(payload), forbidden) {
				t.Fatalf("steady deployment file %s contains %s", path, forbidden)
			}
		}
	}

	for _, root := range []string{
		filepath.Join("..", "deploy", "kubernetes"),
		filepath.Join("..", "deploy", "docker", "keycloak"),
	} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				return nil
			}
			payload, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, forbidden := range []string{
				"bootstrap-admin-username",
				"bootstrap-admin-password",
				"KC_BOOTSTRAP_ADMIN_USERNAME",
				"KC_BOOTSTRAP_ADMIN_PASSWORD",
			} {
				if strings.Contains(string(payload), forbidden) {
					return fmt.Errorf("%s contains forbidden temporary user option %s", path, forbidden)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	bootstrapPath := filepath.Join("..", "deploy", "kubernetes", "overlays", "bootstrap-current-host", "kustomization.yaml")
	bootstrap, err := os.ReadFile(bootstrapPath)
	if err != nil {
		t.Fatalf("read %s: %v", bootstrapPath, err)
	}
	for _, required := range []string{"KC_BOOTSTRAP_ADMIN_CLIENT_ID", "KC_BOOTSTRAP_ADMIN_CLIENT_SECRET"} {
		if !strings.Contains(string(bootstrap), required) {
			t.Fatalf("bootstrap overlay missing %s", required)
		}
	}
}

func TestBootstrapOverlayRendersOnlyImmutableWorkloadImages(t *testing.T) {
	objects := renderKustomizationImagesForTest(t, filepath.Join("..", "deploy", "kubernetes", "overlays", "bootstrap-current-host"))
	wantNoebsImage := "ghcr.io/noebs/noebs@" + readOperationImageDigest(t, filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host", "kustomization.yaml"))
	foundDeleteJob := false
	for _, object := range objects {
		metadata := getMap(object, "metadata")
		name := firstString(metadata, "name")
		for _, image := range manifestImagesForTest(object) {
			if !strings.Contains(image, "@sha256:") {
				t.Fatalf("rendered workload %s uses mutable image %q", name, image)
			}
			if name == "noebs-keycloak-delete-bootstrap-client" {
				foundDeleteJob = true
				if image != wantNoebsImage {
					t.Fatalf("bootstrap deletion Job image = %q, want %q", image, wantNoebsImage)
				}
			}
		}
	}
	if !foundDeleteJob {
		t.Fatal("rendered bootstrap deletion Job not found")
	}
}

func TestBootstrapOverlayRendersEveryObjectIntoNoebsNamespace(t *testing.T) {
	objects := renderKustomizationImagesForTest(t, filepath.Join("..", "deploy", "kubernetes", "overlays", "bootstrap-current-host"))
	for _, object := range objects {
		metadata := getMap(object, "metadata")
		name := firstString(metadata, "name")
		if namespace := firstString(metadata, "namespace"); namespace != "noebs" {
			t.Fatalf("rendered %s %s namespace = %q, want noebs", firstString(object, "kind"), name, namespace)
		}
	}
}

type testKustomizationImage struct {
	Name    string `yaml:"name"`
	NewName string `yaml:"newName"`
	Digest  string `yaml:"digest"`
}

func renderKustomizationImagesForTest(t *testing.T, root string) []map[string]interface{} {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(root, "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read kustomization %s: %v", root, err)
	}
	var kustomization struct {
		Resources []string                 `yaml:"resources"`
		Images    []testKustomizationImage `yaml:"images"`
		Namespace string                   `yaml:"namespace"`
	}
	if err := yaml.Unmarshal(payload, &kustomization); err != nil {
		t.Fatalf("parse kustomization %s: %v", root, err)
	}
	objects := make([]map[string]interface{}, 0)
	for _, resource := range kustomization.Resources {
		path := filepath.Join(root, resource)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat kustomization resource %s: %v", path, err)
		}
		if info.IsDir() {
			objects = append(objects, renderKustomizationImagesForTest(t, path)...)
			continue
		}
		filePayload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read kustomization resource %s: %v", path, err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(filePayload))
		for {
			object := map[string]interface{}{}
			if err := decoder.Decode(&object); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				t.Fatalf("decode kustomization resource %s: %v", path, err)
			}
			if len(object) != 0 {
				objects = append(objects, object)
			}
		}
	}
	for _, object := range objects {
		applyKustomizationImagesForTest(object, kustomization.Images)
		if kustomization.Namespace != "" {
			metadata := getMap(object, "metadata")
			metadata["namespace"] = kustomization.Namespace
		}
	}
	return objects
}

func applyKustomizationImagesForTest(value interface{}, images []testKustomizationImage) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if key == "image" {
				raw, ok := child.(string)
				if !ok {
					continue
				}
				for _, image := range images {
					base := strings.SplitN(raw, "@", 2)[0]
					if colon := strings.LastIndex(base, ":"); colon > strings.LastIndex(base, "/") {
						base = base[:colon]
					}
					if base == image.Name || base == image.NewName {
						typed[key] = image.NewName + "@" + image.Digest
						break
					}
				}
				continue
			}
			applyKustomizationImagesForTest(child, images)
		}
	case []interface{}:
		for _, child := range typed {
			applyKustomizationImagesForTest(child, images)
		}
	}
}

func manifestImagesForTest(value interface{}) []string {
	images := make([]string, 0)
	var visit func(interface{})
	visit = func(current interface{}) {
		switch typed := current.(type) {
		case map[string]interface{}:
			for key, child := range typed {
				if key == "image" {
					if image, ok := child.(string); ok {
						images = append(images, image)
					}
					continue
				}
				visit(child)
			}
		case []interface{}:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return images
}

func TestKeycloakReconciliationSequenceAndMountedAuthority(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	var keycloakWave string
	var reconciler *manifestObject
	for index := range objects {
		object := &objects[index]
		switch {
		case object.Kind == "Deployment" && object.Metadata.Name == "keycloak":
			keycloakWave = object.Metadata.Annotations["argocd.argoproj.io/sync-wave"]
		case object.Kind == "Job" && object.Metadata.Name == "noebs-keycloak-reconciler":
			reconciler = object
		}
	}
	if keycloakWave != "3" {
		t.Fatalf("Keycloak sync-wave = %q, want 3", keycloakWave)
	}
	if reconciler == nil {
		t.Fatal("noebs-keycloak-reconciler Job not found")
	}
	if reconciler.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "5" {
		t.Fatalf("Keycloak reconciler sync-wave = %q, want 5", reconciler.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
	}
	pod := manifestPodSpecForObject(*reconciler)
	if pod.ServiceAccountName != "keycloak-reconciler" {
		t.Fatalf("Keycloak reconciler serviceAccountName = %q", pod.ServiceAccountName)
	}
	if len(pod.Containers) != 1 {
		t.Fatalf("Keycloak reconciler containers = %d, want 1", len(pod.Containers))
	}
	container := pod.Containers[0]
	requireMount(t, "noebs-keycloak-reconciler", container, "/etc/noebs-keycloak/desired-state.yaml", "keycloak-desired-state.yaml")
	requireMount(t, "noebs-keycloak-reconciler", container, "/etc/noebs-keycloak-reconciler/config.yaml", "config.yaml")
	requireSecretVolume(t, "noebs-keycloak-reconciler", pod.Volumes, "credentials", "keycloak-reconciler-credentials")

	bootstrapPath := filepath.Join("..", "deploy", "kubernetes", "overlays", "bootstrap-current-host", "kustomization.yaml")
	bootstrap, err := os.ReadFile(bootstrapPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"name: noebs-keycloak-reconciler", "keycloak-bootstrap-reconciler-credentials"} {
		if !strings.Contains(string(bootstrap), required) {
			t.Fatalf("%s missing %q", bootstrapPath, required)
		}
	}
	deleteObjects := decodeManifestObjects(t, filepath.Join("..", "deploy", "kubernetes", "overlays", "bootstrap-current-host", "delete-bootstrap-client-job.yaml"))
	if len(deleteObjects) != 1 || deleteObjects[0].Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "6" {
		t.Fatalf("bootstrap delete Job sequence = %#v, want one wave-6 Job", deleteObjects)
	}
}

func TestKeycloakBackofficeCallbacksMatchGatewayLifecycle(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "deploy", "kubernetes", "keycloak-authority", "keycloak-desired-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configData, err := readNoebsKubernetesConfigMapData("..")
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{
		"Keycloak desired state": string(payload),
		"gateway config":         configData["config.yaml"],
	}
	for _, required := range []string{
		"https://api.noebs.sd/backoffice/oauth/callback",
		"https://api.noebs.sd/backoffice/oauth/logout/callback",
	} {
		for name, text := range texts {
			if !strings.Contains(text, required) {
				t.Fatalf("%s missing %q", name, required)
			}
		}
	}
	for _, forbidden := range []string{"https://api.noebs.sd/backoffice/callback", "https://api.noebs.sd/backoffice/logged-out"} {
		for name, text := range texts {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains retired callback %q", name, forbidden)
			}
		}
	}
}

func TestNoebsPostgresKubernetesUsesMountedBootstrapFiles(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))

	var foundPostgres bool
	var bootstrapScript string
	for _, object := range objects {
		if object.Kind == "ConfigMap" && object.Metadata.Name == "postgres-bootstrap" {
			bootstrapScript = object.Data["start.sh"]
			if _, ok := object.Data["001-service-databases.sql"]; ok {
				t.Fatalf("postgres provisioning SQL must come from the validated service-postgres-roles Secret")
			}
		}
		for _, container := range object.Spec.Template.Spec.Containers {
			for _, mount := range container.VolumeMounts {
				if mount.Name == "service-postgres-roles" && object.Metadata.Name != "postgres" && object.Metadata.Name != "noebs-deployment-preflight" {
					t.Fatalf("%s/%s must not mount the complete Postgres role catalog", object.Kind, object.Metadata.Name)
				}
			}
		}
		if object.Kind != "StatefulSet" || object.Metadata.Name != "postgres" {
			continue
		}
		foundPostgres = true
		if len(object.Spec.Template.Spec.Containers) != 1 {
			t.Fatalf("postgres containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
		}
		container := object.Spec.Template.Spec.Containers[0]
		if container.Image != "postgres@sha256:33f923b05f64ca54ac4401c01126a6b92afe839a0aa0a52bc5aeb5cc958e5f20" {
			t.Fatalf("postgres image is not the tested immutable digest: %q", container.Image)
		}
		if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
			t.Fatalf("postgres must use mounted bootstrap files instead of env/envFrom")
		}
		if !containsString(container.Command, "/opt/noebs-postgres/bin/start.sh") {
			t.Fatalf("postgres command = %v, want mounted start.sh", container.Command)
		}
		requireMount(t, "postgres", container, "/opt/noebs-postgres/bin/start.sh", "start.sh")
		requireMount(t, "postgres", container, "/opt/noebs-postgres/init/001-service-databases.sql", "bootstrap.sql")
		requireMount(t, "postgres", container, "/run/secrets/service-role-passwords", "passwords.env")
		requireMount(t, "postgres", container, "/opt/noebs-postgres/secrets/tls.crt", "tls.crt")
		requireMount(t, "postgres", container, "/opt/noebs-postgres/secrets/tls.key", "tls.key")
		requireMount(t, "postgres", container, "/opt/noebs-postgres/secrets/ca.pem", "ca.pem")
		requireSecretVolume(t, "postgres", object.Spec.Template.Spec.Volumes, "service-postgres-roles", "service-postgres-roles")
		requireSecretVolume(t, "postgres", object.Spec.Template.Spec.Volumes, "postgres-credentials", "postgres-credentials")
		requireExecProbeDatabase(t, "postgres", "readinessProbe", container.ReadinessProbe, "postgres")
		requireExecProbeDatabase(t, "postgres", "livenessProbe", container.LivenessProbe, "postgres")
	}
	if !foundPostgres {
		t.Fatalf("postgres StatefulSet not found")
	}
	if bootstrapScript == "" {
		t.Fatalf("postgres-bootstrap ConfigMap missing start.sh")
	}
	requireKubernetesConfigMapDataMatchesFile(t, "postgres-bootstrap start.sh", bootstrapScript, filepath.Join("..", "deploy", "docker", "postgres", "postgres-start.sh"))
	for _, required := range []string{
		"--auth-local=peer",
		"--username=postgres",
		"local all all peer",
		"host all postgres all reject",
		`echo "hostssl $database $role all scram-sha-256"`,
		"hostnossl all all all reject",
		"host all all all reject",
		`authority_marker="$pgdata/.noebs-postgres-authority"`,
		"existing Postgres data contains unexpected authority",
		"hba_file=$pgdata/pg_hba.conf",
		"password_encryption=scram-sha-256",
		"ssl_min_protocol_version=TLSv1.3",
		"ssl_max_protocol_version=TLSv1.3",
	} {
		if !strings.Contains(bootstrapScript, required) {
			t.Fatalf("Postgres bootstrap script missing %q", required)
		}
	}
	requireExactPostgresHBABindings(t, bootstrapScript)
	for _, forbidden := range []string{"POSTGRES_PASSWORD", "PGPASSWORD", ".pgpass", "hostssl all all all scram-sha-256", "host all all all scram-sha-256", "ssl=off", "--username=noebs"} {
		if strings.Contains(bootstrapScript, forbidden) {
			t.Fatalf("Postgres bootstrap script contains retired or insecure authority %q", forbidden)
		}
	}
}

func TestNoebsDatabaseResetAuthorityIsRetired(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	for _, object := range objects {
		if object.Metadata.Name == "noebs-database-reset" || object.Metadata.Name == "database-reset" {
			t.Fatalf("database reset must not be a Kubernetes object: %s/%s", object.Kind, object.Metadata.Name)
		}
		if object.Kind == "ConfigMap" && object.Metadata.Name == "postgres-bootstrap" {
			if _, ok := object.Data["reset-databases.sh"]; ok {
				t.Fatalf("postgres-bootstrap must not carry database reset script")
			}
		}
	}

	path := filepath.Join("..", "deploy", "offline", "reset-noebs-service-databases.sh")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			t.Fatalf("retired network-superuser database reset script still exists: %s", path)
		}
		t.Fatalf("stat retired database reset script: %v", err)
	}
}

func TestTemporalKubernetesUsesMountedConfigAndSchemaJob(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))

	var foundPostgres bool
	var foundSchemaJob bool
	var foundNamespaceJob bool
	var foundTemporalFrontendService bool
	var foundTemporal bool
	var postgresBootstrap string
	var temporalConfig map[string]string

	for _, object := range objects {
		if object.Kind == "ConfigMap" && object.Metadata.Name == "temporal-postgres-bootstrap" {
			postgresBootstrap = object.Data["start.sh"]
		}
		if object.Kind == "ConfigMap" && object.Metadata.Name == "temporal-config" {
			temporalConfig = object.Data
		}
		if strings.Contains(object.Metadata.Name, "temporal-ui") {
			t.Fatalf("retired Temporal UI object remains: %s/%s", object.Kind, object.Metadata.Name)
		}

		switch {
		case object.Kind == "Service" && object.Metadata.Name == "temporal-frontend":
			foundTemporalFrontendService = true
			requireManifestServicePort(t, object, 7233)
		case object.Kind == "StatefulSet" && object.Metadata.Name == "temporal-postgres":
			foundPostgres = true
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("temporal-postgres containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			if container.Image != "postgres@sha256:33f923b05f64ca54ac4401c01126a6b92afe839a0aa0a52bc5aeb5cc958e5f20" {
				t.Fatalf("temporal-postgres image is not the tested immutable digest: %q", container.Image)
			}
			if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
				t.Fatalf("temporal-postgres must use mounted bootstrap files instead of env/envFrom")
			}
			if !containsString(container.Command, "/opt/temporal-postgres/bin/start.sh") {
				t.Fatalf("temporal-postgres command = %v, want mounted start.sh", container.Command)
			}
			requireMount(t, "temporal-postgres", container, "/opt/temporal-postgres/bin/start.sh", "start.sh")
			requireMount(t, "temporal-postgres", container, "/opt/temporal-postgres/secrets/password", "password")
			requireMount(t, "temporal-postgres", container, "/opt/temporal-postgres/secrets/tls.crt", "tls.crt")
			requireMount(t, "temporal-postgres", container, "/opt/temporal-postgres/secrets/tls.key", "tls.key")
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
			if container.Image != "temporalio/auto-setup:1.29.7" {
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
			requireMount(t, "temporal-schema-migrate", container, "/opt/temporal/secrets/postgres-ca.pem", "ca.pem")
		case object.Kind == "Job" && object.Metadata.Name == "temporal-namespace-bootstrap":
			foundNamespaceJob = true
			if object.Metadata.Annotations["argocd.argoproj.io/hook"] != "Sync" {
				t.Fatalf("temporal-namespace-bootstrap hook = %q, want Sync", object.Metadata.Annotations["argocd.argoproj.io/hook"])
			}
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "18" {
				t.Fatalf("temporal-namespace-bootstrap sync-wave = %q, want 18", object.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
			}
			if object.Spec.Template.Spec.ServiceAccountName != "temporal-namespace-bootstrap" {
				t.Fatalf("temporal-namespace-bootstrap serviceAccountName = %q", object.Spec.Template.Spec.ServiceAccountName)
			}
			if object.Spec.Template.Spec.AutomountServiceAccountToken == nil || *object.Spec.Template.Spec.AutomountServiceAccountToken {
				t.Fatalf("temporal-namespace-bootstrap must disable service account token automount")
			}
			if object.Spec.Template.Spec.RestartPolicy != "Never" {
				t.Fatalf("temporal-namespace-bootstrap restartPolicy = %q, want Never", object.Spec.Template.Spec.RestartPolicy)
			}
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("temporal-namespace-bootstrap containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			if container.Image != "ghcr.io/noebs/noebs:master" {
				t.Fatalf("temporal-namespace-bootstrap image = %q", container.Image)
			}
			if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
				t.Fatalf("temporal-namespace-bootstrap must use mounted config instead of env/envFrom")
			}
			if !containsString(container.Command, "/usr/local/bin/noebs") || !containsString(container.Args, "ensure-temporal-namespace") {
				t.Fatalf("temporal-namespace-bootstrap command/args = %v %v", container.Command, container.Args)
			}
			requireMount(t, "temporal-namespace-bootstrap", container, "/etc/noebs-temporal/ca.pem", "ca.pem")
			requireMount(t, "temporal-namespace-bootstrap", container, "/etc/noebs-temporal/client-secret", "client-secret")
			requireMount(t, "temporal-namespace-bootstrap", container, "/etc/noebs-keycloak/ca.pem", "ca.pem")
		case object.Kind == "Deployment" && object.Metadata.Name == "temporal":
			foundTemporal = true
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "15" {
				t.Fatalf("temporal sync-wave = %q, want 15", object.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
			}
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("temporal containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			if container.Image != "temporalio/auto-setup:1.29.7" {
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
			requireMount(t, "temporal", container, "/opt/temporal/secrets/postgres-ca.pem", "ca.pem")
			requireMount(t, "temporal", container, "/opt/temporal/secrets/tls.crt", "tls.crt")
			requireMount(t, "temporal", container, "/opt/temporal/secrets/tls.key", "tls.key")
			requireMount(t, "temporal", container, "/opt/temporal/secrets/keycloak-ca.pem", "ca.pem")
		}
	}

	if !foundPostgres {
		t.Fatalf("temporal-postgres StatefulSet not found")
	}
	if !foundSchemaJob {
		t.Fatalf("temporal-schema-migrate Job not found")
	}
	if !foundNamespaceJob {
		t.Fatalf("temporal-namespace-bootstrap Job not found")
	}
	if !foundTemporalFrontendService {
		t.Fatalf("temporal-frontend Service not found")
	}
	if !foundTemporal {
		t.Fatalf("temporal Deployment not found")
	}
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-postgres-bootstrap start.sh", postgresBootstrap, filepath.Join("..", "deploy", "docker", "temporal", "postgres-start.sh"))
	if !strings.Contains(temporalConfig["temporal.yaml"], "broadcastAddress: 127.0.0.1") {
		t.Fatalf("temporal.yaml must carry an explicit broadcast address")
	}
	for _, required := range []string{"https://keycloak.noebs.svc.cluster.local:8443/auth/realms/noebs/protocol/openid-connect/certs", "authorizer: default", "claimMapper: default", "audience: noebs-temporal", "internal-frontend:", "rpcAddress: 127.0.0.1:7236", "bindOnIP: 127.0.0.1", "caFile: /opt/temporal/secrets/postgres-ca.pem", "enableHostVerification: true", "serverName: temporal-postgres"} {
		if !strings.Contains(temporalConfig["temporal.yaml"], required) {
			t.Fatalf("temporal.yaml missing authenticated topology %q", required)
		}
	}
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-config temporal-start.sh", temporalConfig["temporal-start.sh"], filepath.Join("..", "deploy", "docker", "temporal", "temporal-start.sh"))
	requireTemporalStartScriptExplicitInputs(t, temporalConfig["temporal-start.sh"])
	requireKubernetesConfigMapDataMatchesFile(t, "temporal-config schema-migrate.sh", temporalConfig["schema-migrate.sh"], filepath.Join("..", "deploy", "docker", "temporal", "schema-migrate.sh"))
	if _, ok := temporalConfig["dynamicconfig.yaml"]; !ok {
		t.Fatalf("temporal-config missing dynamicconfig.yaml")
	}
}

func TestCurrentHostHasNoSecondaryIngressAuthority(t *testing.T) {
	overlay, err := os.ReadFile(filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(overlay), "ingress.yaml") {
		t.Fatal("current-host overlay must leave public ingress and TLS authority to edge Caddy")
	}
}

func TestNoebsDockerComposeServicesUseMountedConfigFiles(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	if _, ok := compose.Secrets["noebs_secrets"]; ok {
		t.Fatalf("docker-compose.yml must not define shared noebs_secrets for service runtimes")
	}
	for _, retired := range []string{"secrets-init", "runtime-init"} {
		if _, ok := compose.Services[retired]; ok {
			t.Fatalf("docker-compose.yml still defines retired %s service", retired)
		}
	}
	if _, ok := compose.Secrets["postgres-bootstrap-secrets"]; ok {
		t.Fatalf("docker-compose.yml must not define a network Postgres bootstrap credential")
	}
	if _, ok := compose.Volumes["noebs-runtime"]; ok {
		t.Fatalf("docker-compose.yml still defines retired rendered password volume noebs-runtime")
	}

	serviceFiles, err := filepath.Glob(filepath.Join("..", "deploy", "docker", "services", "*.yaml"))
	if err != nil {
		t.Fatalf("list docker service configs: %v", err)
	}
	if len(serviceFiles) == 0 {
		t.Fatalf("no docker service configs found")
	}
	backgroundHealthServices := map[string]bool{
		"ebs-adapter-events":        true,
		"admin-reporting-projector": true,
		"wallet-worker":             true,
	}
	signedHTTPReceivers := map[string]bool{
		"identity-auth":     true,
		"card-vault":        true,
		"ebs-adapter":       true,
		"psp-webhook":       true,
		"admin-reporting":   true,
		"notification-chat": true,
		"wallet-api":        true,
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
		rejectComposeSecret(t, serviceName, service.Secrets, "sops_age_key")
		rejectComposeSecret(t, serviceName, service.Secrets, "service-role-passwords")
		requireComposeTopLevelSecret(t, compose.Secrets, secretSource, "./deploy/docker/secrets/"+strings.TrimSuffix(secretSource, "-secrets")+".secrets.yaml")
		if backgroundHealthServices[serviceName] {
			requireComposeHTTPHealthcheck(t, serviceName, service.Healthcheck, "curl -fsS http://localhost:8080/test || exit 1")
		}
		if signedHTTPReceivers[serviceName] && service.DependsOn["workload-auth-migrate"].Condition != "service_completed_successfully" {
			t.Fatalf("%s must wait for workload-auth-migrate before its startup nonce-store check", serviceName)
		}
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
	requireComposeSecret(t, "db", db.Secrets, "service-role-passwords", "service-role-passwords")
	requireComposeSecret(t, "db", db.Secrets, "noebs_postgres_transport_ca_certificate", "/opt/noebs-postgres/secrets/ca.pem")
	requireComposeSecret(t, "db", db.Secrets, "noebs_postgres_tls_certificate", "/opt/noebs-postgres/secrets/tls.crt")
	requireComposeSecret(t, "db", db.Secrets, "noebs_postgres_tls_private_key", "/opt/noebs-postgres/secrets/tls.key")
	requireComposeTopLevelSecret(t, compose.Secrets, "service-role-passwords", "./deploy/docker/postgres/service-role-passwords.env")
	requireComposeTopLevelSecret(t, compose.Secrets, "noebs_postgres_transport_ca_certificate", "./deploy/docker/postgres/ca.pem")
	requireComposeTopLevelSecret(t, compose.Secrets, "noebs_postgres_tls_certificate", "./deploy/docker/postgres/tls.crt")
	requireComposeTopLevelSecret(t, compose.Secrets, "noebs_postgres_tls_private_key", "./deploy/docker/postgres/tls.key")
	if db.Healthcheck == nil || !slices.Equal(db.Healthcheck.Test, []string{"CMD-SHELL", "pg_isready -U postgres -d postgres"}) {
		t.Fatalf("db healthcheck = %#v, want local postgres authority", db.Healthcheck)
	}
}

func TestKafkaDockerComposeUsesMountedConfigFiles(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))

	kafka, ok := compose.Services["kafka"]
	if !ok {
		t.Fatalf("docker-compose.yml missing kafka service")
	}
	if kafka.Environment != nil {
		t.Fatalf("kafka defines environment; Kafka config must be file-mounted")
	}
	if kafka.EnvFile != nil {
		t.Fatalf("kafka defines env_file; Kafka config must be file-mounted")
	}
	if !containsString(kafka.Entrypoint, "/opt/noebs-kafka/bin/start.sh") {
		t.Fatalf("kafka entrypoint = %v, want mounted start.sh", kafka.Entrypoint)
	}
	requireComposeVolume(t, "kafka", kafka.Volumes, "./deploy/docker/kafka/server.properties", "/mnt/shared/config/server.properties")
	requireComposeVolume(t, "kafka", kafka.Volumes, "./deploy/docker/kafka/cluster.id", "/mnt/shared/config/cluster.id")
	requireComposeVolume(t, "kafka", kafka.Volumes, "./deploy/docker/kafka/start.sh", "/opt/noebs-kafka/bin/start.sh")
	requireComposeVolume(t, "kafka", kafka.Volumes, "kafka-data", "/var/lib/kafka/data")

	kafkaTopics, ok := compose.Services["kafka-topics"]
	if !ok {
		t.Fatalf("docker-compose.yml missing kafka-topics service")
	}
	if kafkaTopics.Environment != nil {
		t.Fatalf("kafka-topics defines environment; Kafka topic config must be file-mounted")
	}
	if kafkaTopics.EnvFile != nil {
		t.Fatalf("kafka-topics defines env_file; Kafka topic config must be file-mounted")
	}
	if !containsString(kafkaTopics.Entrypoint, "/opt/noebs-kafka/bin/create-topics.sh") {
		t.Fatalf("kafka-topics entrypoint = %v, want mounted create-topics.sh", kafkaTopics.Entrypoint)
	}
	requireComposeVolume(t, "kafka-topics", kafkaTopics.Volumes, "./deploy/docker/kafka/bootstrap-server", "/mnt/shared/config/bootstrap-server")
	requireComposeVolume(t, "kafka-topics", kafkaTopics.Volumes, "./deploy/docker/kafka/topics.txt", "/mnt/shared/config/topics.txt")
	requireComposeVolume(t, "kafka-topics", kafkaTopics.Volumes, "./deploy/docker/kafka/create-topics.sh", "/opt/noebs-kafka/bin/create-topics.sh")
}

func TestKafkaKubernetesStatefulSetUsesMountedConfigFiles(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))

	var foundStatefulSet bool
	var foundService bool
	var foundTopicsJob bool
	var kafkaConfig map[string]string
	for _, object := range objects {
		if object.Kind == "Service" && object.Metadata.Name == "kafka" {
			foundService = true
			requireManifestServicePort(t, object, 9092)
		}
		if object.Kind == "ConfigMap" && object.Metadata.Name == "kafka-config" {
			kafkaConfig = object.Data
		}
		if object.Kind == "Job" && object.Metadata.Name == "kafka-topics" {
			foundTopicsJob = true
			if object.Metadata.Annotations["argocd.argoproj.io/hook"] != "Sync" {
				t.Fatalf("kafka-topics hook = %q, want Sync", object.Metadata.Annotations["argocd.argoproj.io/hook"])
			}
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "5" {
				t.Fatalf("kafka-topics sync-wave = %q, want 5", object.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
			}
			if object.Spec.Template.Spec.ServiceAccountName != "kafka-topics" {
				t.Fatalf("kafka-topics serviceAccountName = %q", object.Spec.Template.Spec.ServiceAccountName)
			}
			if object.Spec.Template.Spec.AutomountServiceAccountToken == nil || *object.Spec.Template.Spec.AutomountServiceAccountToken {
				t.Fatalf("kafka-topics must set automountServiceAccountToken: false")
			}
			if object.Spec.Template.Spec.RestartPolicy != "Never" {
				t.Fatalf("kafka-topics restartPolicy = %q, want Never", object.Spec.Template.Spec.RestartPolicy)
			}
			if len(object.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("kafka-topics containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
			}
			container := object.Spec.Template.Spec.Containers[0]
			if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
				t.Fatalf("kafka-topics must use mounted config instead of env/envFrom")
			}
			if !containsString(container.Command, "/opt/noebs-kafka/bin/create-topics.sh") {
				t.Fatalf("kafka-topics command = %v, want mounted create-topics.sh", container.Command)
			}
			requireMount(t, "kafka-topics", container, "/mnt/shared/config/bootstrap-server", "bootstrap-server")
			requireMount(t, "kafka-topics", container, "/mnt/shared/config/topics.txt", "topics.txt")
			requireMount(t, "kafka-topics", container, "/opt/noebs-kafka/bin/create-topics.sh", "create-topics.sh")
		}
		if object.Kind != "StatefulSet" || object.Metadata.Name != "kafka" {
			continue
		}
		foundStatefulSet = true
		if object.Spec.Template.Spec.ServiceAccountName != "kafka" {
			t.Fatalf("kafka serviceAccountName = %q", object.Spec.Template.Spec.ServiceAccountName)
		}
		if object.Spec.Template.Spec.AutomountServiceAccountToken == nil || *object.Spec.Template.Spec.AutomountServiceAccountToken {
			t.Fatalf("kafka must set automountServiceAccountToken: false")
		}
		if len(object.Spec.Template.Spec.Containers) != 1 {
			t.Fatalf("kafka containers = %d, want 1", len(object.Spec.Template.Spec.Containers))
		}
		container := object.Spec.Template.Spec.Containers[0]
		if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
			t.Fatalf("kafka must use mounted config instead of env/envFrom")
		}
		if !containsString(container.Command, "/opt/noebs-kafka/bin/start.sh") {
			t.Fatalf("kafka command = %v, want mounted start.sh", container.Command)
		}
		requireMount(t, "kafka", container, "/mnt/shared/config/server.properties", "server.properties")
		requireMount(t, "kafka", container, "/mnt/shared/config/cluster.id", "cluster.id")
		requireMount(t, "kafka", container, "/opt/noebs-kafka/bin/start.sh", "start.sh")
		requireMount(t, "kafka", container, "/var/lib/kafka/data", "")
	}
	if !foundService {
		t.Fatalf("kafka Service not found")
	}
	if kafkaConfig == nil {
		t.Fatalf("kafka-config ConfigMap not found")
	}
	if !foundStatefulSet {
		t.Fatalf("kafka StatefulSet not found")
	}
	if !foundTopicsJob {
		t.Fatalf("kafka-topics Job not found")
	}
	requireKubernetesConfigMapDataMatchesFile(t, "kafka-config server.properties", kafkaConfig["server.properties"], filepath.Join("..", "deploy", "docker", "kafka", "server.properties"))
	requireKubernetesConfigMapDataMatchesFile(t, "kafka-config cluster.id", kafkaConfig["cluster.id"], filepath.Join("..", "deploy", "docker", "kafka", "cluster.id"))
	requireKubernetesConfigMapDataMatchesFile(t, "kafka-config bootstrap-server", kafkaConfig["bootstrap-server"], filepath.Join("..", "deploy", "docker", "kafka", "bootstrap-server"))
	requireKubernetesConfigMapDataMatchesFile(t, "kafka-config topics.txt", kafkaConfig["topics.txt"], filepath.Join("..", "deploy", "docker", "kafka", "topics.txt"))
	requireKubernetesConfigMapDataMatchesFile(t, "kafka-config start.sh", kafkaConfig["start.sh"], filepath.Join("..", "deploy", "docker", "kafka", "start.sh"))
	requireKubernetesConfigMapDataMatchesFile(t, "kafka-config create-topics.sh", kafkaConfig["create-topics.sh"], filepath.Join("..", "deploy", "docker", "kafka", "create-topics.sh"))
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
		{"wallet_approval_threshold", dockerConfig.Noebs.WalletApprovalThreshold, kubernetesConfig.Noebs.WalletApprovalThreshold},
		{"wallet_default_currency", dockerConfig.Noebs.WalletDefaultCurrency, kubernetesConfig.Noebs.WalletDefaultCurrency},
		{"wallet_hold_expiry_seconds", dockerConfig.Noebs.WalletHoldExpirySeconds, kubernetesConfig.Noebs.WalletHoldExpirySeconds},
		{"wallet_approval_timeout_seconds", dockerConfig.Noebs.WalletApprovalTimeoutSeconds, kubernetesConfig.Noebs.WalletApprovalTimeoutSeconds},
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

func TestDockerComposeKafkaRuntimeConfigMatchesKubernetes(t *testing.T) {
	dockerConfig := decodeMountedNoebsConfigFile(t, filepath.Join("..", "config.docker.yaml"))
	kubernetesConfig := decodeKubernetesBaseNoebsConfig(t)

	if strings.Join(dockerConfig.Noebs.KafkaBrokers, ",") == "" {
		t.Fatalf("config.docker.yaml kafka_brokers must be explicit")
	}
	if strings.Join(dockerConfig.Noebs.KafkaBrokers, ",") != strings.Join(kubernetesConfig.Noebs.KafkaBrokers, ",") {
		t.Fatalf("config.docker.yaml kafka_brokers = %v, want Kubernetes value %v", dockerConfig.Noebs.KafkaBrokers, kubernetesConfig.Noebs.KafkaBrokers)
	}
	if dockerConfig.Noebs.KafkaTransactionTopic == "" {
		t.Fatalf("config.docker.yaml kafka_transaction_topic must be explicit")
	}
	if dockerConfig.Noebs.KafkaTransactionTopic != kubernetesConfig.Noebs.KafkaTransactionTopic {
		t.Fatalf("config.docker.yaml kafka_transaction_topic = %q, want Kubernetes value %q", dockerConfig.Noebs.KafkaTransactionTopic, kubernetesConfig.Noebs.KafkaTransactionTopic)
	}
	if dockerConfig.Noebs.AdminReportingKafkaConsumerGroup == "" {
		t.Fatalf("config.docker.yaml admin_reporting_kafka_consumer_group must be explicit")
	}
	if dockerConfig.Noebs.AdminReportingKafkaConsumerGroup != kubernetesConfig.Noebs.AdminReportingKafkaConsumerGroup {
		t.Fatalf("config.docker.yaml admin_reporting_kafka_consumer_group = %q, want Kubernetes value %q", dockerConfig.Noebs.AdminReportingKafkaConsumerGroup, kubernetesConfig.Noebs.AdminReportingKafkaConsumerGroup)
	}
	if dockerConfig.Noebs.EBSTransactionEventPublisherBatchSize <= 0 {
		t.Fatalf("config.docker.yaml ebs_transaction_event_publisher_batch_size must be explicit")
	}
	if dockerConfig.Noebs.EBSTransactionEventPublisherBatchSize != kubernetesConfig.Noebs.EBSTransactionEventPublisherBatchSize {
		t.Fatalf("config.docker.yaml ebs_transaction_event_publisher_batch_size = %d, want Kubernetes value %d", dockerConfig.Noebs.EBSTransactionEventPublisherBatchSize, kubernetesConfig.Noebs.EBSTransactionEventPublisherBatchSize)
	}
	if dockerConfig.Noebs.EBSTransactionEventPublisherPollIntervalMs <= 0 {
		t.Fatalf("config.docker.yaml ebs_transaction_event_publisher_poll_interval_ms must be explicit")
	}
	if dockerConfig.Noebs.EBSTransactionEventPublisherPollIntervalMs != kubernetesConfig.Noebs.EBSTransactionEventPublisherPollIntervalMs {
		t.Fatalf("config.docker.yaml ebs_transaction_event_publisher_poll_interval_ms = %d, want Kubernetes value %d", dockerConfig.Noebs.EBSTransactionEventPublisherPollIntervalMs, kubernetesConfig.Noebs.EBSTransactionEventPublisherPollIntervalMs)
	}
}

func TestTemporalDockerComposeUsesMountedConfigAndSchemaJob(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	for _, network := range []string{"temporal-control", "temporal-storage", "temporal-keycloak"} {
		definition, ok := compose.Networks[network]
		if !ok || !definition.Internal {
			t.Fatalf("docker-compose.yml network %q must exist and be internal", network)
		}
	}

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
	requireComposeSecret(t, "temporal-postgres", temporalPostgres.Secrets, "temporal_postgres_tls_certificate", "/opt/temporal-postgres/secrets/tls.crt")
	requireComposeSecret(t, "temporal-postgres", temporalPostgres.Secrets, "temporal_postgres_tls_private_key", "/opt/temporal-postgres/secrets/tls.key")
	requireComposeNetworks(t, "temporal-postgres", temporalPostgres.Networks, "temporal-storage")

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
	requireComposeSecret(t, "temporal-schema-migrate", temporalSchemaMigrate.Secrets, "temporal_transport_ca_certificate", "/opt/temporal/secrets/postgres-ca.pem")
	if temporalSchemaMigrate.Image != "temporalio/auto-setup@sha256:f14912b699cf73015ad5c4fc18d522d4b014db90e794039214dfb7c022c2644f" {
		t.Fatalf("temporal-schema-migrate image = %q, want pinned Temporal 1.29.7", temporalSchemaMigrate.Image)
	}
	requireComposeNetworks(t, "temporal-schema-migrate", temporalSchemaMigrate.Networks, "temporal-storage")

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
	requireComposeSecret(t, "temporal", temporal.Secrets, "temporal_transport_ca_certificate", "/opt/temporal/secrets/postgres-ca.pem")
	requireComposeSecret(t, "temporal", temporal.Secrets, "temporal_tls_certificate", "/opt/temporal/secrets/tls.crt")
	requireComposeSecret(t, "temporal", temporal.Secrets, "temporal_tls_private_key", "/opt/temporal/secrets/tls.key")
	requireComposeSecret(t, "temporal", temporal.Secrets, "keycloak_transport_ca_certificate", "/opt/temporal/secrets/keycloak-ca.pem")
	if temporal.Image != "temporalio/auto-setup@sha256:f14912b699cf73015ad5c4fc18d522d4b014db90e794039214dfb7c022c2644f" {
		t.Fatalf("temporal image = %q, want pinned Temporal 1.29.7", temporal.Image)
	}
	requireComposeNetworks(t, "temporal", temporal.Networks, "temporal-control", "temporal-keycloak", "temporal-storage")
	rejectComposePublishedPorts(t, "temporal", temporal.Ports)
	temporalConfig, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "temporal", "temporal.yaml"))
	if err != nil {
		t.Fatalf("read Temporal config: %v", err)
	}
	for _, required := range []string{"https://keycloak:8443/auth/realms/noebs/protocol/openid-connect/certs", "authorizer: default", "claimMapper: default", "audience: noebs-temporal", "rpcName: internal-frontend", "rpcAddress: 127.0.0.1:7236", "caFile: /opt/temporal/secrets/postgres-ca.pem", "enableHostVerification: true", "serverName: temporal-postgres"} {
		if !strings.Contains(string(temporalConfig), required) {
			t.Fatalf("Docker temporal.yaml missing authenticated topology %q", required)
		}
	}
	temporalStart, err := os.ReadFile(filepath.Join("..", "deploy", "docker", "temporal", "temporal-start.sh"))
	if err != nil {
		t.Fatalf("read Temporal start script: %v", err)
	}
	requireTemporalStartScriptExplicitInputs(t, string(temporalStart))

	temporalNamespaceBootstrap, ok := compose.Services["temporal-namespace-bootstrap"]
	if !ok {
		t.Fatalf("docker-compose.yml missing temporal-namespace-bootstrap service")
	}
	if temporalNamespaceBootstrap.Environment != nil {
		t.Fatalf("temporal-namespace-bootstrap defines environment; Temporal namespace bootstrap must use mounted config")
	}
	if temporalNamespaceBootstrap.EnvFile != nil {
		t.Fatalf("temporal-namespace-bootstrap defines env_file; Temporal namespace bootstrap must use mounted config")
	}
	if !containsString(temporalNamespaceBootstrap.Entrypoint, "/usr/local/bin/noebs") || !containsString(temporalNamespaceBootstrap.Entrypoint, "ensure-temporal-namespace") {
		t.Fatalf("temporal-namespace-bootstrap entrypoint = %v, want Noebs secure namespace command", temporalNamespaceBootstrap.Entrypoint)
	}
	requireComposeSecret(t, "temporal-namespace-bootstrap", temporalNamespaceBootstrap.Secrets, "temporal_transport_ca_certificate", "/etc/noebs-temporal/ca.pem")
	requireComposeSecret(t, "temporal-namespace-bootstrap", temporalNamespaceBootstrap.Secrets, "keycloak_transport_ca_certificate", "/etc/noebs-keycloak/ca.pem")
	requireComposeSecret(t, "temporal-namespace-bootstrap", temporalNamespaceBootstrap.Secrets, "temporal_namespace_bootstrap_client_secret", "/etc/noebs-temporal/client-secret")
	requireComposeNetworks(t, "temporal-namespace-bootstrap", temporalNamespaceBootstrap.Networks, "temporal-control", "temporal-keycloak")
	rejectComposePublishedPorts(t, "temporal-namespace-bootstrap", temporalNamespaceBootstrap.Ports)

	if _, ok := compose.Services["temporal-ui"]; ok {
		t.Fatalf("docker-compose.yml must not contain the retired unauthenticated temporal-ui service")
	}
	requireComposeTopLevelSecret(t, compose.Secrets, "temporal_postgres_password", "./deploy/docker/temporal/postgres-password.txt")
	requireComposeTopLevelSecret(t, compose.Secrets, "temporal_postgres_tls_certificate", "./deploy/docker/temporal/postgres-tls.crt")
	requireComposeTopLevelSecret(t, compose.Secrets, "temporal_postgres_tls_private_key", "./deploy/docker/temporal/postgres-tls.key")
	requireComposeTopLevelSecret(t, compose.Secrets, "temporal_transport_ca_certificate", "./deploy/docker/temporal/ca.pem")
	requireComposeTopLevelSecret(t, compose.Secrets, "temporal_tls_certificate", "./deploy/docker/temporal/tls.crt")
	requireComposeTopLevelSecret(t, compose.Secrets, "temporal_tls_private_key", "./deploy/docker/temporal/tls.key")
	requireComposeTopLevelSecret(t, compose.Secrets, "temporal_namespace_bootstrap_client_secret", "./deploy/docker/temporal/namespace-bootstrap-client-secret.txt")
}

func TestCurrentHostEdgeCaddyIsCompleteAndImmutable(t *testing.T) {
	edgeRoot := filepath.Join("..", "deploy", "kubernetes", "edge")
	read := func(name string) string {
		t.Helper()
		path := filepath.Join(edgeRoot, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(data)
	}

	caddyfile := read("Caddyfile")
	const keycloakMetadataMatcher = "@keycloak_metadata {\n\t\t\tmethod GET HEAD\n\t\t\tpath /auth/realms/noebs/.well-known/openid-configuration /auth/realms/noebs/protocol/openid-connect/certs /auth/resources/*\n\t\t}"
	const keycloakBrowserGETMatcher = "@keycloak_browser_get {\n\t\t\tmethod GET\n\t\t\tpath /auth/realms/noebs/protocol/openid-connect/auth /auth/realms/noebs/protocol/openid-connect/logout /auth/realms/noebs/login-actions/authenticate /auth/realms/noebs/login-actions/required-action /auth/realms/noebs/login-actions/restart /auth/realms/noebs/login-actions/first-broker-login /auth/realms/noebs/login-actions/post-broker-login /auth/realms/noebs/broker/google/login /auth/realms/noebs/broker/google/endpoint /auth/realms/noebs/broker/after-first-broker-login /auth/realms/noebs/broker/after-post-broker-login\n\t\t}"
	const keycloakBrowserPOSTMatcher = "@keycloak_browser_post {\n\t\t\tmethod POST\n\t\t\tpath /auth/realms/noebs/protocol/openid-connect/token /auth/realms/noebs/login-actions/authenticate /auth/realms/noebs/login-actions/required-action /auth/realms/noebs/login-actions/first-broker-login /auth/realms/noebs/login-actions/post-broker-login /auth/realms/noebs/broker/after-post-broker-login\n\t\t}"
	for _, required := range []string{
		`api.noebs.sd`,
		`dsa.adonese.sd`,
		`rd.adonese.sd`,
		`unido.noebs.sd`,
		`iptv.2t.sd`,
		`path /.well-known/assetlinks.json`,
		`route {`,
		keycloakMetadataMatcher,
		keycloakBrowserGETMatcher,
		keycloakBrowserPOSTMatcher,
		`reverse_proxy @keycloak_metadata https://keycloak.noebs.svc.cluster.local:8443`,
		`reverse_proxy @keycloak_browser_get https://keycloak.noebs.svc.cluster.local:8443`,
		`reverse_proxy @keycloak_browser_post https://keycloak.noebs.svc.cluster.local:8443`,
		`tls_trust_pool file /etc/noebs-internal/ca.pem`,
		`tls_server_name keycloak.noebs.svc.cluster.local`,
		`reverse_proxy https://api-gateway.noebs.svc.cluster.local:8080`,
		`tls_server_name api-gateway.noebs.svc.cluster.local`,
		`tls_client_auth /etc/noebs-internal/tls.crt /etc/noebs-internal/tls.key`,
		`header_up X-Forwarded-Port 443`,
		`@keycloak_private path /auth /auth/*`,
		`respond @keycloak_private 404`,
		`Strict-Transport-Security "max-age=31536000; includeSubDomains"`,
	} {
		if !strings.Contains(caddyfile, required) {
			t.Errorf("edge Caddyfile missing %q", required)
		}
	}
	for _, redundant := range []string{`header_up X-Forwarded-For`, `header_up X-Forwarded-Host`} {
		if strings.Contains(caddyfile, redundant) {
			t.Errorf("edge Caddyfile overrides Caddy's secure forwarding default with %q", redundant)
		}
	}
	unmatchedKeycloakSurface := caddyfile
	for _, matcher := range []string{keycloakMetadataMatcher, keycloakBrowserGETMatcher, keycloakBrowserPOSTMatcher} {
		if strings.Count(caddyfile, matcher) != 1 {
			t.Fatalf("edge Caddyfile must define each exact Keycloak matcher once")
		}
		unmatchedKeycloakSurface = strings.Replace(unmatchedKeycloakSurface, matcher, "", 1)
	}
	if strings.Contains(unmatchedKeycloakSurface, "/auth/realms/noebs") {
		t.Error("edge Caddyfile contains a Keycloak realm path outside the exact public matchers")
	}
	if strings.Count(caddyfile, "keycloak.noebs.svc.cluster.local:8443") != 3 {
		t.Error("edge Caddyfile must proxy only the three exact Keycloak matcher classes")
	}
	if strings.Count(caddyfile, "tls_server_name keycloak.noebs.svc.cluster.local") != 3 {
		t.Error("edge Caddyfile must verify the exact Keycloak name on all three Keycloak upstreams")
	}
	if strings.Count(caddyfile, "tls_trust_pool file /etc/noebs-internal/ca.pem") != 4 {
		t.Error("edge Caddyfile must verify the release CA on all four Noebs upstreams")
	}
	for _, forbidden := range []string{
		`/auth/realms/noebs/.well-known/*`,
		`/auth/realms/noebs/protocol/openid-connect/*`,
		`/auth/realms/noebs/login-actions/*`,
		`/auth/realms/noebs/broker/*`,
		`/auth/realms/noebs/clients-registrations`,
		`/auth/realms/noebs/protocol/saml`,
		`/auth/realms/noebs/protocol/openid-connect/userinfo`,
		`/auth/realms/noebs/protocol/openid-connect/introspect`,
		`/auth/realms/noebs/protocol/openid-connect/revoke`,
		`/auth/realms/noebs/protocol/openid-connect/auth/device`,
		`/auth/realms/noebs/protocol/openid-connect/ext/par/request`,
		`/auth/realms/noebs/broker/google/link`,
		`/auth/realms/noebs/broker/google/token`,
		`/auth/realms/noebs/login-actions/registration`,
		`/auth/realms/noebs/login-actions/reset-credentials`,
		`tls_insecure_skip_verify`,
		`tls_versions`,
		`http://keycloak`,
		`reverse_proxy api-gateway.noebs.svc.cluster.local:8080`,
	} {
		if strings.Contains(caddyfile, forbidden) {
			t.Errorf("edge Caddyfile exposes forbidden Keycloak surface %q", forbidden)
		}
	}

	kustomization := read("kustomization.yaml")
	if strings.Contains(kustomization, "disableNameSuffixHash") {
		t.Error("edge ConfigMap must be content-addressed so configuration changes roll Caddy")
	}

	deployment := read("deployment.yaml")
	if !strings.Contains(deployment, "image: caddy@sha256:") {
		t.Error("edge Caddy image must be pinned by digest")
	}
	if strings.Contains(deployment, "image: caddy:2-alpine") {
		t.Error("edge deployment must not use a mutable Caddy tag")
	}
	for _, forbidden := range []string{"hostNetwork: false", "dnsPolicy: ClusterFirst\n", "hostPort:"} {
		if strings.Contains(deployment, forbidden) {
			t.Errorf("edge deployment contains incompatible networking %q", forbidden)
		}
	}
	for _, required := range []string{"hostNetwork: true", "dnsPolicy: ClusterFirstWithHostNet"} {
		if !strings.Contains(deployment, required) {
			t.Errorf("edge deployment missing %q", required)
		}
	}
	for _, required := range []string{
		"automountServiceAccountToken: false",
		"allowPrivilegeEscalation: false",
		"readOnlyRootFilesystem: true",
		"drop: [ALL]",
		"add: [NET_BIND_SERVICE]",
		"type: RuntimeDefault",
		"secretName: edge-internal-transport",
		"mountPath: /etc/noebs-internal",
	} {
		if !strings.Contains(deployment, required) {
			t.Errorf("edge deployment missing security boundary %q", required)
		}
	}
	for _, hostUpstream := range []string{"127.0.0.1:8080", "127.0.0.1:18081"} {
		if !strings.Contains(caddyfile, hostUpstream) {
			t.Errorf("edge Caddyfile missing host-loopback upstream %q", hostUpstream)
		}
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
	const keycloakImage = "quay.io/keycloak/keycloak@sha256:2eb3cd316835c990e69e26ade292ffa78f6fb0db7d5fc6377463c162e1979ac0"
	if keycloak.Image != keycloakImage {
		t.Fatalf("keycloak image = %q, want 26.7 release image %q", keycloak.Image, keycloakImage)
	}
	requireComposeSecret(t, "keycloak", keycloak.Secrets, "keycloak_config", "/opt/keycloak/conf/keycloak.conf")
	requireComposeSecret(t, "keycloak", keycloak.Secrets, "keycloak_tls_certificate", "/opt/keycloak/conf/tls.crt")
	requireComposeSecret(t, "keycloak", keycloak.Secrets, "keycloak_tls_private_key", "/opt/keycloak/conf/tls.key")
	requireComposeTopLevelSecret(t, compose.Secrets, "keycloak_config", "./deploy/docker/keycloak/keycloak.conf")
	requireComposeTopLevelSecret(t, compose.Secrets, "keycloak_tls_certificate", "./deploy/docker/keycloak/tls.crt")
	requireComposeTopLevelSecret(t, compose.Secrets, "keycloak_tls_private_key", "./deploy/docker/keycloak/tls.key")
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
}

func TestDockerComposePublishesOnlyAPIGatewayByDefault(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))

	for serviceName, service := range compose.Services {
		if serviceName == "api-gateway" {
			continue
		}
		rejectComposePublishedPorts(t, serviceName, service.Ports)
	}

	apiGateway, ok := compose.Services["api-gateway"]
	if !ok {
		t.Fatalf("docker-compose.yml missing api-gateway service")
	}
	if len(apiGateway.Ports) != 1 || apiGateway.Ports[0] != "127.0.0.1:8081:8080" {
		t.Fatalf("api-gateway ports = %v, want only loopback publication on 127.0.0.1:8081", apiGateway.Ports)
	}
	if _, exists := compose.Services["caddy"]; exists {
		t.Fatal("docker-compose.yml must not define a second Caddy edge")
	}
	for _, volume := range []string{"caddy_data", "caddy_config"} {
		if _, exists := compose.Volumes[volume]; exists {
			t.Fatalf("docker-compose.yml retains obsolete edge volume %q", volume)
		}
	}
	if _, err := os.Stat(filepath.Join("..", "Caddyfile")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root Caddyfile must not exist: %v", err)
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
		`resource "kubernetes_namespace_v1" "argocd"`,
		`count = var.argocd_installation_mode == "helm" ? 1 : 0`,
		`data "kubernetes_namespace_v1" "argocd_existing"`,
		`count = var.argocd_installation_mode == "existing" ? 1 : 0`,
		`resource "kubernetes_namespace_v1" "edge"`,
		`resource "helm_release" "argocd"`,
		`resource "kubernetes_manifest" "noebs_project"`,
		`resource "kubernetes_manifest" "noebs_application"`,
		`count = var.create_noebs_application ? 1 : 0`,
		`resource "kubernetes_manifest" "noebs_edge_application"`,
		`count = var.create_edge_application ? 1 : 0`,
		`name      = "noebs-edge"`,
		`namespace = var.argocd_namespace`,
		`var.noebs_repo_url`,
		`repoURL        = var.noebs_repo_url`,
		`targetRevision = var.noebs_target_revision`,
		`path           = var.noebs_manifest_path`,
		`path           = var.edge_manifest_path`,
		`namespace = kubernetes_namespace_v1.noebs.metadata[0].name`,
		`namespace = kubernetes_namespace_v1.edge.metadata[0].name`,
		`server    = "https://kubernetes.default.svc"`,
		`var.noebs_automated_sync ? {`,
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
	if strings.Contains(mainText, `"CreateNamespace=true"`) {
		t.Fatalf("%s must not delegate namespace ownership to Argo CD", mainPath)
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
	edgeManifestPathRe := regexp.MustCompile(`(?m)^\s*edge_manifest_path\s*=\s*"([^"]+)"\s*$`)
	edgeManifestPathMatch := edgeManifestPathRe.FindStringSubmatch(string(tfvarsExample))
	if len(edgeManifestPathMatch) != 2 {
		t.Fatalf("%s must assign edge_manifest_path", tfvarsExamplePath)
	}
	if edgeManifestPathMatch[1] != "deploy/kubernetes/edge" {
		t.Fatalf("edge_manifest_path = %q, want deploy/kubernetes/edge", edgeManifestPathMatch[1])
	}
	repoURLRe := regexp.MustCompile(`(?m)^\s*noebs_repo_url\s*=\s*"([^"]+)"\s*$`)
	repoURLMatch := repoURLRe.FindStringSubmatch(string(tfvarsExample))
	if len(repoURLMatch) != 2 {
		t.Fatalf("%s must assign noebs_repo_url", tfvarsExamplePath)
	}
	if repoURLMatch[1] != "https://github.com/noebs/noebs.git" {
		t.Fatalf("noebs_repo_url = %q, want https://github.com/noebs/noebs.git", repoURLMatch[1])
	}
	argocdModeRe := regexp.MustCompile(`(?m)^\s*argocd_installation_mode\s*=\s*"([^"]+)"\s*$`)
	argocdModeMatch := argocdModeRe.FindStringSubmatch(string(tfvarsExample))
	if len(argocdModeMatch) != 2 {
		t.Fatalf("%s must assign argocd_installation_mode", tfvarsExamplePath)
	}
	if argocdModeMatch[1] != "existing" {
		t.Fatalf("argocd_installation_mode = %q, want existing for current host", argocdModeMatch[1])
	}
	automatedSyncRe := regexp.MustCompile(`(?m)^\s*noebs_automated_sync\s*=\s*(true|false)\s*$`)
	automatedSyncMatch := automatedSyncRe.FindStringSubmatch(string(tfvarsExample))
	if len(automatedSyncMatch) != 2 || automatedSyncMatch[1] != "false" {
		t.Fatalf("%s must explicitly pause noebs_automated_sync", tfvarsExamplePath)
	}
	if _, err := os.Stat(filepath.Join("..", filepath.FromSlash(match[1]), "kustomization.yaml")); err != nil {
		t.Fatalf("noebs_manifest_path does not contain kustomization.yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join("..", filepath.FromSlash(edgeManifestPathMatch[1]), "kustomization.yaml")); err != nil {
		t.Fatalf("edge_manifest_path does not contain kustomization.yaml: %v", err)
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
		"noebs-workload-auth-migrate":     false,
		"noebs-gateway-auth-migrate":      false,
		"noebs-identity-auth-migrate":     false,
		"noebs-card-vault-migrate":        false,
		"noebs-ebs-adapter-migrate":       false,
		"noebs-admin-reporting-migrate":   false,
		"noebs-notification-chat-migrate": false,
		"noebs-wallet-ledger-migrate":     false,
	}
	expectedMigrationWaves := map[string]string{
		"noebs-workload-auth-migrate":     "9",
		"noebs-gateway-auth-migrate":      "18",
		"noebs-identity-auth-migrate":     "10",
		"noebs-card-vault-migrate":        "11",
		"noebs-ebs-adapter-migrate":       "12",
		"noebs-admin-reporting-migrate":   "14",
		"noebs-notification-chat-migrate": "15",
		"noebs-wallet-ledger-migrate":     "16",
	}
	expectedRuntimeDeployments := map[string]bool{
		"api-gateway":               false,
		"identity-auth":             false,
		"card-vault":                false,
		"ebs-adapter":               false,
		"ebs-adapter-events":        false,
		"psp-webhook":               false,
		"admin-reporting":           false,
		"admin-reporting-projector": false,
		"notification-chat":         false,
		"wallet-api":                false,
		"wallet-ledger":             false,
		"wallet-worker":             false,
	}
	backgroundHealthDeployments := map[string]bool{
		"ebs-adapter-events":        true,
		"admin-reporting-projector": true,
		"wallet-worker":             true,
	}
	expectedCleanup := map[string]bool{
		"noebs-workload-auth-cleanup": false,
		"noebs-gateway-auth-cleanup":  false,
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
			wantWave := "20"
			if object.Metadata.Name == "notification-chat" {
				wantWave = "21"
			}
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != wantWave {
				t.Fatalf("%s runtime sync-wave = %q, want %s", object.Metadata.Name, object.Metadata.Annotations["argocd.argoproj.io/sync-wave"], wantWave)
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
			if backgroundHealthDeployments[object.Metadata.Name] {
				requireContainerPort(t, object.Metadata.Name, container, "http", 8080)
				requireHTTPProbe(t, object.Metadata.Name, "readinessProbe", container.ReadinessProbe, "/test", "http", 10, 6)
				requireHTTPProbe(t, object.Metadata.Name, "livenessProbe", container.LivenessProbe, "/test", "http", 30, 3)
			}
		case "Job":
			if !strings.HasPrefix(object.Metadata.Name, "noebs-") {
				continue
			}
			if object.Metadata.Name == "noebs-deployment-preflight" || object.Metadata.Name == "noebs-keycloak-reconciler" {
				continue
			}
			if _, ok := expectedJobs[object.Metadata.Name]; !ok {
				t.Fatalf("unexpected migration Job %q", object.Metadata.Name)
			}
			expectedJobs[object.Metadata.Name] = true
			if object.Metadata.Annotations["argocd.argoproj.io/hook"] != "Sync" {
				t.Fatalf("%s hook = %q, want Sync", object.Metadata.Name, object.Metadata.Annotations["argocd.argoproj.io/hook"])
			}
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != expectedMigrationWaves[object.Metadata.Name] {
				t.Fatalf("%s sync-wave = %q, want %s", object.Metadata.Name, object.Metadata.Annotations["argocd.argoproj.io/sync-wave"], expectedMigrationWaves[object.Metadata.Name])
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
			if !strings.Contains(container.Image, "ghcr.io/noebs/noebs") {
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
		case "CronJob":
			if _, ok := expectedCleanup[object.Metadata.Name]; !ok {
				continue
			}
			expectedCleanup[object.Metadata.Name] = true
			if object.Metadata.Annotations["argocd.argoproj.io/sync-wave"] != "19" {
				t.Fatalf("%s sync-wave = %q, want 19", object.Metadata.Name, object.Metadata.Annotations["argocd.argoproj.io/sync-wave"])
			}
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
	for cleanup, found := range expectedCleanup {
		if !found {
			t.Fatalf("cleanup CronJob %q not found", cleanup)
		}
	}
}

func TestDeploymentPreflightJobRunsBeforeMigrations(t *testing.T) {
	objects := decodeManifestObjectsFromDir(t, filepath.Join("..", "deploy", "kubernetes", "base"))
	serviceConfigs := map[string]string{
		"api-gateway":               "api-gateway.service.yaml",
		"identity-auth":             "identity-auth.service.yaml",
		"card-vault":                "card-vault.service.yaml",
		"ebs-adapter":               "ebs-adapter.service.yaml",
		"ebs-adapter-events":        "ebs-adapter-events.service.yaml",
		"psp-webhook":               "psp-webhook.service.yaml",
		"admin-reporting":           "admin-reporting.service.yaml",
		"admin-reporting-projector": "admin-reporting-projector.service.yaml",
		"notification-chat":         "notification-chat.service.yaml",
		"wallet-api":                "wallet-api.service.yaml",
		"wallet-ledger":             "wallet-ledger.service.yaml",
		"wallet-worker":             "wallet-worker.service.yaml",
		"workload-auth-migrate":     "workload-auth-migrate.service.yaml",
		"workload-auth-cleanup":     "workload-auth-cleanup.service.yaml",
		"gateway-auth-migrate":      "gateway-auth-migrate.service.yaml",
		"gateway-auth-cleanup":      "gateway-auth-cleanup.service.yaml",
		"identity-auth-migrate":     "identity-auth-migrate.service.yaml",
		"card-vault-migrate":        "card-vault-migrate.service.yaml",
		"ebs-adapter-migrate":       "ebs-adapter-migrate.service.yaml",
		"admin-reporting-migrate":   "admin-reporting-migrate.service.yaml",
		"notification-chat-migrate": "notification-chat-migrate.service.yaml",
		"wallet-ledger-migrate":     "wallet-ledger-migrate.service.yaml",
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
	serviceSecrets := make(map[string]string, len(kubernetesServiceSecretSources))
	for _, source := range kubernetesServiceSecretSources {
		serviceSecrets[source.serviceName] = source.secretName
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
		if container.Image != "ghcr.io/noebs/noebs:master" {
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
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/temporal-postgres-password.txt", "password")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/keycloak-postgres-password.txt", "password")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/ghcr-dockerconfigjson", ".dockerconfigjson")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/keycloak.conf", "keycloak.conf")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/keycloak-reconciler-config.yaml", "config.yaml")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/workload-auth-postgres-roles.secrets.yaml", "roles.yaml")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/gateway-auth-postgres-roles.secrets.yaml", "roles.yaml")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/service-postgres-roles.secrets.yaml", "roles.yaml")
		requireMount(t, "noebs-deployment-preflight", container, "/preflight/platform/postgres-provisioning.sql", "bootstrap.sql")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "temporal-postgres-credentials", "temporal-postgres-credentials")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "keycloak-postgres-credentials", "keycloak-postgres-credentials")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "ghcr-credentials", "ghcr-credentials")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "keycloak-secrets", "keycloak-secrets")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "keycloak-reconciler-credentials", "keycloak-reconciler-credentials")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "workload-auth-postgres-roles", "workload-auth-postgres-roles")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "gateway-auth-postgres-roles", "gateway-auth-postgres-roles")
		requireSecretVolume(t, "noebs-deployment-preflight", object.Spec.Template.Spec.Volumes, "service-postgres-roles", "service-postgres-roles")

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

	referencedSecrets := map[string]bool{}
	for _, object := range objects {
		if object.Kind == "ServiceAccount" {
			for _, imagePullSecret := range object.ImagePullSecrets {
				if imagePullSecret.Name != "" {
					referencedSecrets[imagePullSecret.Name] = true
				}
			}
		}
		for _, volume := range manifestPodSpecForObject(object).Volumes {
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
	requiredSecretKeys := parseTerraformStringListMapLocal(t, filepath.Join("..", "foundation", "terraform", "locals.tf"), "noebs_required_kubernetes_secret_keys")
	renderedSecrets := renderedKubernetesSecretNames()
	renderedSecretKeys := renderedKubernetesSecretKeys()

	for secretName := range renderedSecrets {
		if !requiredSecrets[secretName] {
			t.Fatalf("render-kubernetes-secrets renders Secret %q but noebs_required_kubernetes_secrets does not declare it", secretName)
		}
	}
	for secretName := range requiredSecrets {
		if !renderedSecrets[secretName] {
			t.Fatalf("noebs_required_kubernetes_secrets declares Secret %q but render-kubernetes-secrets does not render it", secretName)
		}
		if _, ok := requiredSecretKeys[secretName]; !ok {
			t.Fatalf("noebs_required_kubernetes_secrets declares Secret %q but noebs_required_kubernetes_secret_keys does not declare its data keys", secretName)
		}
	}
	for secretName := range requiredSecretKeys {
		if !requiredSecrets[secretName] {
			t.Fatalf("noebs_required_kubernetes_secret_keys declares Secret %q but noebs_required_kubernetes_secrets does not declare it", secretName)
		}
		if !renderedSecrets[secretName] {
			t.Fatalf("noebs_required_kubernetes_secret_keys declares Secret %q but render-kubernetes-secrets does not render it", secretName)
		}
	}
	for secretName, keys := range renderedSecretKeys {
		requiredKeys, ok := requiredSecretKeys[secretName]
		if !ok {
			t.Fatalf("render-kubernetes-secrets renders Secret %q but noebs_required_kubernetes_secret_keys does not declare it", secretName)
		}
		for key := range keys {
			if !requiredKeys[key] {
				t.Fatalf("render-kubernetes-secrets renders Secret %q key %q but foundation does not require it", secretName, key)
			}
		}
		for key := range requiredKeys {
			if !keys[key] {
				t.Fatalf("foundation requires Secret %q key %q but render-kubernetes-secrets does not render it", secretName, key)
			}
		}
	}

	outputs, err := os.ReadFile(filepath.Join("..", "foundation", "terraform", "outputs.tf"))
	if err != nil {
		t.Fatalf("read foundation/terraform/outputs.tf: %v", err)
	}
	if !strings.Contains(string(outputs), `output "noebs_required_kubernetes_secrets"`) {
		t.Fatalf("foundation/terraform/outputs.tf must expose noebs_required_kubernetes_secrets")
	}
	if !strings.Contains(string(outputs), `output "noebs_required_kubernetes_secret_keys"`) {
		t.Fatalf("foundation/terraform/outputs.tf must expose noebs_required_kubernetes_secret_keys")
	}

	main, err := os.ReadFile(filepath.Join("..", "foundation", "terraform", "main.tf"))
	if err != nil {
		t.Fatalf("read foundation/terraform/main.tf: %v", err)
	}
	for _, forbidden := range []string{
		`data "kubernetes_secret_v1"`,
		`data.kubernetes_secret_v1`,
		`.data), required_key`,
	} {
		if strings.Contains(string(main), forbidden) {
			t.Fatalf("foundation/terraform/main.tf must not read Kubernetes Secret values through %q", forbidden)
		}
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
		"edge_namespace",
		"argocd_chart_version",
		"argocd_installation_mode",
		"noebs_repo_url",
		"noebs_target_revision",
		"noebs_manifest_path",
		"edge_manifest_path",
		"create_noebs_application",
		"noebs_automated_sync",
		"create_edge_application",
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
	revisionBlock := blocks["noebs_target_revision"]
	if !strings.Contains(revisionBlock, `can(regex("^[0-9a-f]{40}$", var.noebs_target_revision))`) {
		t.Fatal("noebs_target_revision must reject branches, tags, uppercase SHAs, and abbreviated commits")
	}
	revisionAssignmentRe := regexp.MustCompile(`(?m)^\s*noebs_target_revision\s*=\s*"([^"]+)"\s*$`)
	revisionAssignment := revisionAssignmentRe.FindStringSubmatch(tfvarsExampleText)
	if len(revisionAssignment) != 2 || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(revisionAssignment[1]) {
		t.Fatalf("%s noebs_target_revision must be an exact lowercase 40-hex commit", tfvarsExamplePath)
	}
}

func renderedKubernetesSecretNames() map[string]bool {
	secrets := map[string]bool{
		"noebs-release-manifest":                   true,
		"postgres-credentials":                     true,
		"service-postgres-roles":                   true,
		"workload-auth-postgres-roles":             true,
		"gateway-auth-postgres-roles":              true,
		"internal-transport-platform":              true,
		"temporal-postgres-credentials":            true,
		"temporal-server-credentials":              true,
		"temporal-namespace-bootstrap-credentials": true,
		"keycloak-postgres-credentials":            true,
		"keycloak-secrets":                         true,
		"keycloak-transport-ca":                    true,
		"keycloak-reconciler-credentials":          true,
		"ghcr-credentials":                         true,
	}
	for _, source := range kubernetesServiceSecretSources {
		secrets[source.secretName] = true
	}
	return secrets
}

func renderedKubernetesSecretKeys() map[string]map[string]bool {
	secrets := map[string]map[string]bool{
		"noebs-release-manifest":                   {kubernetesReleaseManifestFile: true},
		"postgres-credentials":                     {"ca.pem": true, "tls.crt": true, "tls.key": true},
		"service-postgres-roles":                   {"passwords.env": true, "bootstrap.sql": true, "roles.yaml": true},
		"workload-auth-postgres-roles":             {"roles.yaml": true},
		"gateway-auth-postgres-roles":              {"roles.yaml": true},
		"internal-transport-platform":              {"credentials.yaml": true},
		"temporal-postgres-credentials":            {"password": true, "ca.pem": true, "tls.crt": true, "tls.key": true},
		"temporal-server-credentials":              {"ca.pem": true, "tls.crt": true, "tls.key": true},
		"temporal-namespace-bootstrap-credentials": {"ca.pem": true, "client-secret": true},
		"keycloak-postgres-credentials":            {"password": true, "tls.crt": true, "tls.key": true},
		"keycloak-secrets":                         {"keycloak.conf": true, "db-ca.pem": true, "tls.crt": true, "tls.key": true},
		"keycloak-transport-ca":                    {"ca.pem": true},
		"keycloak-reconciler-credentials":          {"config.yaml": true},
		"ghcr-credentials":                         {".dockerconfigjson": true},
	}
	for _, source := range kubernetesServiceSecretSources {
		secrets[source.secretName] = map[string]bool{"secrets.yaml": true}
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
	case "Deployment", "StatefulSet", "Job", "CronJob":
		return true
	default:
		return false
	}
}

func workloadUsesNoebsImage(object manifestObject) bool {
	podSpec := manifestPodSpecForObject(object)
	for _, container := range append(podSpec.Containers, podSpec.InitContainers...) {
		if strings.Contains(container.Image, "ghcr.io/noebs/noebs") {
			return true
		}
	}
	return false
}

func manifestRefsContain(refs []manifestRef, name string) bool {
	for _, ref := range refs {
		if ref.Name == name {
			return true
		}
	}
	return false
}

func expectedServiceAccountForWorkload(t *testing.T, object manifestObject) string {
	t.Helper()
	if object.Kind != "Job" && object.Kind != "CronJob" {
		return object.Metadata.Name
	}
	if strings.HasPrefix(object.Metadata.Name, "noebs-") {
		return strings.TrimPrefix(object.Metadata.Name, "noebs-")
	}
	return object.Metadata.Name
}

func manifestPodSpecForObject(object manifestObject) manifestPodSpec {
	if object.Kind == "CronJob" {
		return object.Spec.JobTemplate.Spec.Template.Spec
	}
	return object.Spec.Template.Spec
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

func requireContainerPort(t *testing.T, workload string, container manifestContainer, name string, port int) {
	t.Helper()
	for _, entry := range container.Ports {
		gotName, ok := entry["name"].(string)
		if !ok {
			t.Fatalf("%s/%s container port entry missing string name: %#v", workload, container.Name, entry)
		}
		if gotName != name {
			continue
		}
		gotPort, ok := entry["containerPort"].(int)
		if !ok {
			t.Fatalf("%s/%s port %s containerPort = %#v, want int %d", workload, container.Name, name, entry["containerPort"], port)
		}
		if gotPort != port {
			t.Fatalf("%s/%s port %s containerPort = %d, want %d", workload, container.Name, name, gotPort, port)
		}
		return
	}
	t.Fatalf("%s/%s missing container port %s:%d", workload, container.Name, name, port)
}

func requireHTTPProbe(t *testing.T, workload, probeName string, probe map[string]any, path, port string, periodSeconds, failureThreshold int) {
	t.Helper()
	httpGet, ok := probe["httpGet"].(map[string]any)
	if !ok {
		t.Fatalf("%s %s missing httpGet: %#v", workload, probeName, probe)
	}
	gotPath, ok := httpGet["path"].(string)
	if !ok || gotPath != path {
		t.Fatalf("%s %s httpGet.path = %#v, want %q", workload, probeName, httpGet["path"], path)
	}
	gotPort, ok := httpGet["port"].(string)
	if !ok || gotPort != port {
		t.Fatalf("%s %s httpGet.port = %#v, want %q", workload, probeName, httpGet["port"], port)
	}
	gotPeriod, ok := probe["periodSeconds"].(int)
	if !ok || gotPeriod != periodSeconds {
		t.Fatalf("%s %s periodSeconds = %#v, want %d", workload, probeName, probe["periodSeconds"], periodSeconds)
	}
	gotFailureThreshold, ok := probe["failureThreshold"].(int)
	if !ok || gotFailureThreshold != failureThreshold {
		t.Fatalf("%s %s failureThreshold = %#v, want %d", workload, probeName, probe["failureThreshold"], failureThreshold)
	}
}

func requireExecProbeDatabase(t *testing.T, workload, probeName string, probe map[string]any, database string) {
	t.Helper()
	execProbe, ok := probe["exec"].(map[string]any)
	if !ok {
		t.Fatalf("%s %s missing exec: %#v", workload, probeName, probe)
	}
	rawCommand, ok := execProbe["command"].([]any)
	if !ok {
		t.Fatalf("%s %s exec.command = %#v, want list", workload, probeName, execProbe["command"])
	}
	for i := 0; i < len(rawCommand)-1; i++ {
		flag, ok := rawCommand[i].(string)
		if !ok || flag != "-d" {
			continue
		}
		gotDatabase, ok := rawCommand[i+1].(string)
		if !ok {
			t.Fatalf("%s %s -d value = %#v, want %q", workload, probeName, rawCommand[i+1], database)
		}
		if gotDatabase != database {
			t.Fatalf("%s %s database = %q, want %q", workload, probeName, gotDatabase, database)
		}
		return
	}
	t.Fatalf("%s %s command missing -d %q: %#v", workload, probeName, database, rawCommand)
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
		`export SSL_CERT_FILE="/opt/temporal/secrets/keycloak-ca.pem"`,
		`--service frontend`,
		`--service internal-frontend`,
		`--service history`,
		`--service matching`,
		`--service worker`,
	}
	for _, want := range required {
		if !strings.Contains(script, want) {
			t.Fatalf("temporal-start.sh missing explicit mounted input read: %s", want)
		}
	}
	for _, rejected := range []string{":-", "getent hosts", "$(hostname)", "broadcast_address", "--allow-no-auth", "__TEMPORAL_POSTGRES_PASSWORD__", "__TEMPORAL_BROADCAST_ADDRESS__", "__BROADCAST_ADDRESS_FROM_FILE__"} {
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

func requirePortScopedIngressNetworkPolicy(t *testing.T, object manifestObject, targetPod string, port int, allowedSources []string) {
	t.Helper()
	if len(object.Spec.PolicyTypes) != 1 || object.Spec.PolicyTypes[0] != "Ingress" {
		t.Fatalf("%s policyTypes = %v, want [Ingress]", object.Metadata.Name, object.Spec.PolicyTypes)
	}
	if got := object.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"]; got != targetPod {
		t.Fatalf("%s podSelector app.kubernetes.io/name = %q, want %q", object.Metadata.Name, got, targetPod)
	}
	if len(object.Spec.PodSelector.MatchLabels) != 1 {
		t.Fatalf("%s podSelector must select only app.kubernetes.io/name; got %v", object.Metadata.Name, object.Spec.PodSelector.MatchLabels)
	}
	if len(object.Spec.Ingress) != 1 {
		t.Fatalf("%s ingress rule count = %d, want 1", object.Metadata.Name, len(object.Spec.Ingress))
	}
	rule := object.Spec.Ingress[0]
	if len(rule.Ports) != 1 {
		t.Fatalf("%s ingress ports = %v, want exactly one port", object.Metadata.Name, rule.Ports)
	}
	if rule.Ports[0].Protocol != "TCP" || rule.Ports[0].Port != port {
		t.Fatalf("%s ingress port = %+v, want TCP/%d", object.Metadata.Name, rule.Ports[0], port)
	}
	if allowedSources == nil {
		if len(rule.From) == 0 {
			t.Fatalf("%s ingress rule has no source selector", object.Metadata.Name)
		}
		return
	}
	if len(rule.From) != len(allowedSources) {
		t.Fatalf("%s ingress peers = %d, want %d", object.Metadata.Name, len(rule.From), len(allowedSources))
	}
	wantedSources := make(map[string]bool, len(allowedSources))
	for _, source := range allowedSources {
		wantedSources[source] = true
	}
	for _, peer := range rule.From {
		source := ""
		switch {
		case peer.PodSelector != nil && peer.IPBlock == nil:
			if len(peer.PodSelector.MatchLabels) != 1 {
				t.Fatalf("%s ingress peer must use one exact pod label: %+v", object.Metadata.Name, peer)
			}
			source = peer.PodSelector.MatchLabels["app.kubernetes.io/name"]
		case peer.PodSelector == nil && peer.IPBlock != nil:
			if len(peer.IPBlock.Except) != 0 {
				t.Fatalf("%s ingress IP peer has exceptions: %+v", object.Metadata.Name, peer.IPBlock.Except)
			}
			source = "ip:" + peer.IPBlock.CIDR
		default:
			t.Fatalf("%s ingress peer must use one exact pod label or IP block: %+v", object.Metadata.Name, peer)
		}
		if !wantedSources[source] {
			t.Fatalf("%s ingress peer %q is not allowed", object.Metadata.Name, source)
		}
		delete(wantedSources, source)
	}
	if len(wantedSources) != 0 {
		t.Fatalf("%s missing ingress peers: %v", object.Metadata.Name, wantedSources)
	}
}

func exactNetworkPolicySelectorValues(t *testing.T, label string, selector manifestLabelSelector) []string {
	t.Helper()
	if len(selector.MatchLabels) != 0 || len(selector.MatchExpressions) != 1 {
		t.Fatalf("%s selector = %#v, want one exact set expression", label, selector)
	}
	requirement := selector.MatchExpressions[0]
	if requirement.Key != "app.kubernetes.io/name" || requirement.Operator != "In" || len(requirement.Values) == 0 {
		t.Fatalf("%s selector requirement = %#v", label, requirement)
	}
	return requirement.Values
}

func networkPoliciesByTargetPod(objects []manifestObject) map[string]manifestObject {
	policies := map[string]manifestObject{}
	for _, object := range objects {
		if object.Kind != "NetworkPolicy" {
			continue
		}
		if !containsString(object.Spec.PolicyTypes, "Ingress") {
			continue
		}
		target := object.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"]
		if target == "" {
			continue
		}
		policies[target] = object
	}
	return policies
}

func requireIngressNetworkPolicyAllows(t *testing.T, object manifestObject, allowedPod string) {
	t.Helper()
	for _, rule := range object.Spec.Ingress {
		for _, peer := range rule.From {
			if peer.PodSelector == nil {
				continue
			}
			if peer.PodSelector.MatchLabels["app.kubernetes.io/name"] == allowedPod {
				return
			}
		}
	}
	t.Fatalf("%s does not allow ingress from %s", object.Metadata.Name, allowedPod)
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

func requireComposeHTTPHealthcheck(t *testing.T, serviceName string, healthcheck *composeHealthcheck, command string) {
	t.Helper()
	requireComposeHealthcheck(t, serviceName, healthcheck, []string{"CMD-SHELL", command})
}

func requireComposeHealthcheck(t *testing.T, serviceName string, healthcheck *composeHealthcheck, test []string) {
	t.Helper()
	if healthcheck == nil {
		t.Fatalf("%s missing healthcheck", serviceName)
	}
	if !slices.Equal(healthcheck.Test, test) {
		t.Fatalf("%s healthcheck test = %v, want %v", serviceName, healthcheck.Test, test)
	}
	if healthcheck.Interval != "30s" {
		t.Fatalf("%s healthcheck interval = %q, want 30s", serviceName, healthcheck.Interval)
	}
	if healthcheck.Timeout != "3s" {
		t.Fatalf("%s healthcheck timeout = %q, want 3s", serviceName, healthcheck.Timeout)
	}
	if healthcheck.Retries != 3 {
		t.Fatalf("%s healthcheck retries = %d, want 3", serviceName, healthcheck.Retries)
	}
	if healthcheck.StartPeriod != "10s" {
		t.Fatalf("%s healthcheck start_period = %q, want 10s", serviceName, healthcheck.StartPeriod)
	}
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

func requireComposeNetworks(t *testing.T, serviceName string, node yaml.Node, expected ...string) {
	t.Helper()
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		node = *node.Alias
	}
	var actual []string
	switch node.Kind {
	case yaml.SequenceNode:
		for _, value := range node.Content {
			actual = append(actual, value.Value)
		}
	case yaml.MappingNode:
		for index := 0; index < len(node.Content); index += 2 {
			actual = append(actual, node.Content[index].Value)
		}
	default:
		t.Fatalf("%s networks has YAML kind %d, want sequence or mapping", serviceName, node.Kind)
	}
	slices.Sort(actual)
	want := append([]string(nil), expected...)
	slices.Sort(want)
	if !slices.Equal(actual, want) {
		t.Fatalf("%s networks = %v, want %v", serviceName, actual, want)
	}
}

func requireExactPostgresHBABindings(t *testing.T, script string) {
	t.Helper()
	start := strings.Index(script, "database_role_bindings=(")
	if start < 0 {
		t.Fatal("Postgres start script has no database-role binding catalog")
	}
	end := strings.Index(script[start:], "\n)")
	if end < 0 {
		t.Fatal("Postgres start script has an unterminated database-role binding catalog")
	}
	bindingBlock := script[start : start+end]
	matches := regexp.MustCompile(`(?m)^\s+"([a-z_]+) ([a-z_]+)"$`).FindAllStringSubmatch(bindingBlock, -1)
	actual := make(map[string]int, len(matches))
	for _, match := range matches {
		actual[match[1]+" "+match[2]]++
	}
	expected := make(map[string]bool, len(allPostgresRoleSpecs()))
	for _, spec := range allPostgresRoleSpecs() {
		expected[spec.database+" "+spec.username] = true
	}
	if len(actual) != len(expected) {
		t.Fatalf("Postgres HBA binding count = %d, want %d", len(actual), len(expected))
	}
	for binding := range expected {
		if actual[binding] != 1 {
			t.Fatalf("Postgres HBA binding %q count = %d, want 1", binding, actual[binding])
		}
	}
	if strings.Count(script, `echo "hostssl $database $role all scram-sha-256"`) != 1 {
		t.Fatal("Postgres start script must emit exactly one HBA allow rule per catalog binding")
	}
	allow := strings.Index(script, `echo "hostssl $database $role all scram-sha-256"`)
	finalReject := strings.LastIndex(script, `echo "host all all all reject"`)
	if finalReject < allow {
		t.Fatal("Postgres HBA must end network matching with an all-role reject")
	}
}

func composeSecretSourceForService(serviceName string) string {
	return serviceName + "-secrets"
}

func requirePlaceholderStrings(t *testing.T, path string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "caller" {
				caller, ok := child.(string)
				if !ok || !slices.Contains([]string{"api-gateway", "identity-auth", "ebs-adapter"}, caller) {
					t.Fatalf("%s contains invalid fixed workload caller %v", path, child)
				}
				continue
			}
			requirePlaceholderStrings(t, path, child)
		}
	case []any:
		for _, child := range typed {
			requirePlaceholderStrings(t, path, child)
		}
	case string:
		if !strings.Contains(typed, "REPLACE_WITH_") {
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
		if !ok || !strings.Contains(dbURL, "REPLACE_WITH_") {
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
		"ipin_username",
		"ipin_password",
		"pub_key",
		"ipin_key",
		"pan",
		"pin",
		"ipin",
		"exp_date",
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

func parseTerraformStringListMapLocal(t *testing.T, path, localName string) map[string]map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	lines := strings.Split(string(data), "\n")
	startRe := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(localName) + `\s*=\s*\{\s*$`)
	entryStartRe := regexp.MustCompile(`^\s*"([^"]+)"\s*=\s*\[\s*$`)
	valueRe := regexp.MustCompile(`^\s*"([^"]+)"\s*,?\s*$`)
	values := map[string]map[string]bool{}
	inMap := false
	currentName := ""
	currentValues := map[string]bool{}
	for _, line := range lines {
		if !inMap {
			if startRe.MatchString(line) {
				inMap = true
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if currentName == "" && trimmed == "}" {
			if len(values) == 0 {
				t.Fatalf("Terraform local %s is empty in %s", localName, path)
			}
			return values
		}
		if trimmed == "" {
			continue
		}
		if currentName == "" {
			match := entryStartRe.FindStringSubmatch(line)
			if len(match) != 2 {
				t.Fatalf("Terraform local %s has unsupported map entry %q in %s", localName, line, path)
			}
			currentName = match[1]
			if _, exists := values[currentName]; exists {
				t.Fatalf("Terraform local %s repeats %q in %s", localName, currentName, path)
			}
			currentValues = map[string]bool{}
			continue
		}
		if trimmed == "]" {
			if len(currentValues) == 0 {
				t.Fatalf("Terraform local %s entry %s is empty in %s", localName, currentName, path)
			}
			values[currentName] = currentValues
			currentName = ""
			currentValues = map[string]bool{}
			continue
		}
		match := valueRe.FindStringSubmatch(line)
		if len(match) != 2 {
			t.Fatalf("Terraform local %s entry %s has unsupported list item %q in %s", localName, currentName, line, path)
		}
		if currentValues[match[1]] {
			t.Fatalf("Terraform local %s entry %s repeats %q in %s", localName, currentName, match[1], path)
		}
		currentValues[match[1]] = true
	}
	if currentName != "" {
		t.Fatalf("Terraform local %s entry %s is not closed in %s", localName, currentName, path)
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
	databaseRe := regexp.MustCompile(`CREATE DATABASE ([a-z_]+) OWNER [a-z_]+`)
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
