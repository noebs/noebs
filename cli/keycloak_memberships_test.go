package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/adonese/noebs/internal/keycloakadmin"
	"gopkg.in/yaml.v3"
)

const cliMembershipSubject = "11111111-1111-4111-8111-11111111111a"

func TestRunAssignKeycloakMembershipsDryRun(t *testing.T) {
	fake := newCLIMembershipFake()
	server, caPath := newKeycloakTransportTestServer(t, fake)
	configPath := writeCLIMembershipConfig(t, server.URL)
	membershipsPath := filepath.Join(t.TempDir(), "memberships.yaml")
	if err := os.WriteFile(membershipsPath, []byte("api_version: "+keycloakadmin.MembershipsAPIVersion+"\nsubject: "+cliMembershipSubject+"\nmemberships:\n  - tenant: tenant-cutover\n    class: backoffice\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	memberships, actions, dryRun, err := runAssignKeycloakMemberships([]string{
		"--memberships", membershipsPath,
		"--desired-state", "../deploy/kubernetes/keycloak-authority/keycloak-desired-state.yaml",
		"--tenant-catalog", "../deploy/kubernetes/keycloak-authority/tenant-catalog.yaml",
		"--config", configPath,
		"--ca", caPath,
		"--dry-run",
	}, nil)
	if err != nil {
		t.Fatalf("runAssignKeycloakMemberships() error = %v", err)
	}
	want := []keycloakadmin.PlannedMembershipAction{{
		Subject: cliMembershipSubject,
		Tenant:  "tenant-cutover",
		Class:   keycloakadmin.MembershipClassBackoffice,
		Action:  keycloakadmin.MembershipActionAdd,
	}}
	if !dryRun || memberships.Subject != cliMembershipSubject || !reflect.DeepEqual(actions, want) {
		t.Fatalf("runAssignKeycloakMemberships() = %#v, %#v, %t", memberships, actions, dryRun)
	}
	if fake.writeCount() != 0 {
		t.Fatalf("dry-run writes = %d", fake.writeCount())
	}
}

func TestKeycloakMembershipCommandParsing(t *testing.T) {
	if _, _, _, err := runAssignKeycloakMemberships(nil, http.DefaultClient); err == nil {
		t.Fatal("runAssignKeycloakMemberships() accepted missing flags")
	}
	if _, err := runLookupKeycloakSubject(nil, http.DefaultClient); err == nil {
		t.Fatal("runLookupKeycloakSubject() accepted missing flags")
	}

	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })
	for _, command := range []string{"assign-keycloak-memberships", "lookup-keycloak-subject"} {
		os.Args = []string{"noebs", command}
		if !isConfigUtilityCommand() {
			t.Fatalf("%s did not bypass application config loading", command)
		}
	}
}

func TestLookupKeycloakSubjectCommandWritesOnlyUUID(t *testing.T) {
	fake := newCLIMembershipFake()
	server, caPath := newKeycloakTransportTestServer(t, fake)
	configPath := writeCLIMembershipConfig(t, server.URL)
	emailPath := filepath.Join(t.TempDir(), "email")
	if err := os.WriteFile(emailPath, []byte("user@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	originalArgs := os.Args
	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"noebs", "lookup-keycloak-subject", "--email-file", emailPath, "--config", configPath, "--ca", caPath}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Args = originalArgs
		os.Stdout = originalStdout
		_ = reader.Close()
		_ = writer.Close()
	})

	if err := lookupKeycloakSubjectCommand(); err != nil {
		t.Fatalf("lookupKeycloakSubjectCommand() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(output), cliMembershipSubject+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if fake.writeCount() != 0 {
		t.Fatalf("lookup writes = %d", fake.writeCount())
	}
}

func TestKeycloakMembershipOperationManifests(t *testing.T) {
	operations := filepath.Join("..", "deploy", "kubernetes", "operations")
	releaseDigest := readOperationImageDigest(t, filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host", "kustomization.yaml"))
	for _, path := range []string{
		filepath.Join("..", "deploy", "kubernetes", "overlays", "bootstrap-current-host", "kustomization.yaml"),
		filepath.Join(operations, "lookup", "kustomization.yaml"),
		filepath.Join(operations, "memberships", "base", "kustomization.yaml"),
	} {
		if digest := readOperationImageDigest(t, path); digest != releaseDigest {
			t.Fatalf("%s digest = %q, release digest = %q", path, digest, releaseDigest)
		}
	}

	lookup := decodeCatalogWorkload(t, filepath.Join(operations, "lookup", "job.yaml"))
	if args := lookup.Spec.Template.Spec.Containers[0].Args; !slices.Equal(args, []string{
		"lookup-keycloak-subject", "--email-file", "/etc/noebs-keycloak-operation/email", "--config", "/etc/noebs-keycloak-reconciler/config.yaml", "--ca", "/etc/noebs-keycloak/ca.pem",
	}) {
		t.Fatalf("lookup args = %v", args)
	}
	requireSecretFileMount(t, lookup, "lookup", "keycloak-subject-lookup", "/etc/noebs-keycloak-operation/email", "email")
	requireSecretFileMount(t, lookup, "credentials", "keycloak-reconciler-credentials", "/etc/noebs-keycloak-reconciler/config.yaml", "config.yaml")
	requireSecretFileMount(t, lookup, "transport-ca", "keycloak-transport-ca", "/etc/noebs-keycloak/ca.pem", "ca.pem")

	assignment := decodeCatalogWorkload(t, filepath.Join(operations, "memberships", "base", "job.yaml"))
	args := assignment.Spec.Template.Spec.Containers[0].Args
	for _, required := range []string{"assign-keycloak-memberships", "--memberships", "--desired-state", "--tenant-catalog", "--config", "--ca", "/etc/noebs-keycloak/ca.pem"} {
		if !slices.Contains(args, required) {
			t.Fatalf("assignment args %v do not contain %q", args, required)
		}
	}
	requireSecretFileMount(t, assignment, "memberships", "keycloak-membership-assignment", "/etc/noebs-keycloak-operation/memberships.yaml", "memberships.yaml")
	requireSecretFileMount(t, assignment, "credentials", "keycloak-reconciler-credentials", "/etc/noebs-keycloak-reconciler/config.yaml", "config.yaml")
	requireSecretFileMount(t, assignment, "transport-ca", "keycloak-transport-ca", "/etc/noebs-keycloak/ca.pem", "ca.pem")
	requireConfigMapFileMount(t, assignment, "desired-state", "keycloak-desired-state", "/etc/noebs-keycloak/desired-state.yaml", "keycloak-desired-state.yaml")
	requireConfigMapFileMount(t, assignment, "tenant-catalog", "tenant-catalog", "/etc/noebs-keycloak/tenant-catalog.yaml", "tenant-catalog.yaml")

	dryRun, err := os.ReadFile(filepath.Join(operations, "memberships", "dry-run", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dryRun), "value: --dry-run") {
		t.Fatal("dry-run operation does not add --dry-run")
	}
	base, err := os.ReadFile(filepath.Join("..", "deploy", "kubernetes", "base", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(base), "operations") || strings.Contains(string(base), "membership-assignment") {
		t.Fatal("operator-only membership Jobs were added to the Argo base")
	}
	membershipBase, err := os.ReadFile(filepath.Join(operations, "memberships", "base", "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(membershipBase), "../../../keycloak-authority") {
		t.Fatal("membership operation does not consume the canonical Keycloak authority generator")
	}
	runnerPath := filepath.Join(operations, "run-membership-job.sh")
	runner, err := os.ReadFile(runnerPath)
	if err != nil {
		t.Fatal(err)
	}
	runnerText := string(runner)
	for _, required := range []string{
		`git -C "$repo_root" diff --quiet`,
		`.spec.source.targetRevision == $revision`,
		`.spec.source.path == "deploy/kubernetes/overlays/current-host"`,
		`.status.sync.revision == $revision`,
		`argocd.argoproj.io/tracking-id`,
		`cmp -s "$source_file"`,
		`apply --dry-run=server -f "$job_manifest"`,
		`apply -f "$job_manifest"`,
	} {
		if !strings.Contains(runnerText, required) {
			t.Fatalf("membership operation runner missing %q", required)
		}
	}
	if strings.Contains(runnerText, "apply -k") || strings.Contains(runnerText, "delete -k") {
		t.Fatal("membership operation runner may apply or delete Argo-owned ConfigMaps")
	}
	info, err := os.Stat(runnerPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("membership operation runner is not executable")
	}
	readme, err := os.ReadFile(filepath.Join(operations, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(readme), "apply -k deploy/kubernetes/operations/memberships") {
		t.Fatal("membership runbook directly applies shared authority ConfigMaps")
	}

	assertMembershipAuthorityRenders(t,
		filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host"),
		filepath.Join(operations, "memberships", "dry-run"),
		filepath.Join(operations, "memberships", "apply"),
	)
}

type renderedMembershipObject struct {
	Kind      string            `yaml:"kind"`
	Immutable bool              `yaml:"immutable"`
	Data      map[string]string `yaml:"data"`
	Metadata  struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Labels    map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Args []string `yaml:"args"`
				} `yaml:"containers"`
				Volumes []struct {
					Name      string `yaml:"name"`
					ConfigMap *struct {
						Name string `yaml:"name"`
					} `yaml:"configMap"`
				} `yaml:"volumes"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func assertMembershipAuthorityRenders(t *testing.T, steadyPath, dryRunPath, applyPath string) {
	t.Helper()
	steady := renderMembershipKustomization(t, steadyPath)
	dryRun := renderMembershipKustomization(t, dryRunPath)
	apply := renderMembershipKustomization(t, applyPath)
	steadyAuthority := renderedMembershipAuthority(t, steady)

	for label, objects := range map[string][]renderedMembershipObject{
		"dry-run": dryRun,
		"apply":   apply,
	} {
		authority := renderedMembershipAuthority(t, objects)
		for logicalName, steadyConfigMap := range steadyAuthority {
			operationConfigMap := authority[logicalName]
			if operationConfigMap.Metadata.Name != steadyConfigMap.Metadata.Name {
				t.Fatalf("%s %s ConfigMap = %q, steady = %q", label, logicalName, operationConfigMap.Metadata.Name, steadyConfigMap.Metadata.Name)
			}
			if !reflect.DeepEqual(operationConfigMap.Data, steadyConfigMap.Data) {
				t.Fatalf("%s %s ConfigMap data differs from steady authority", label, logicalName)
			}
		}

		job := renderedMembershipJob(t, objects)
		volumeConfigMaps := make(map[string]string)
		for _, volume := range job.Spec.Template.Spec.Volumes {
			if volume.ConfigMap != nil {
				volumeConfigMaps[volume.Name] = volume.ConfigMap.Name
			}
		}
		if got := volumeConfigMaps["desired-state"]; got != authority["keycloak-desired-state"].Metadata.Name {
			t.Fatalf("%s desired-state volume = %q, authority = %q", label, got, authority["keycloak-desired-state"].Metadata.Name)
		}
		if got := volumeConfigMaps["tenant-catalog"]; got != authority["tenant-catalog"].Metadata.Name {
			t.Fatalf("%s tenant-catalog volume = %q, authority = %q", label, got, authority["tenant-catalog"].Metadata.Name)
		}
		if len(job.Spec.Template.Spec.Containers) != 1 {
			t.Fatalf("%s membership containers = %d", label, len(job.Spec.Template.Spec.Containers))
		}
		hasDryRun := slices.Contains(job.Spec.Template.Spec.Containers[0].Args, "--dry-run")
		if hasDryRun != (label == "dry-run") {
			t.Fatalf("%s membership --dry-run = %t", label, hasDryRun)
		}
	}
}

func renderMembershipKustomization(t *testing.T, path string) []renderedMembershipObject {
	t.Helper()
	output, err := exec.Command("kustomize", "build", path).CombinedOutput()
	if err != nil {
		t.Fatalf("kustomize build %s: %v\n%s", path, err, output)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(output)))
	var objects []renderedMembershipObject
	for {
		var object renderedMembershipObject
		if err := decoder.Decode(&object); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode kustomize build %s: %v", path, err)
		}
		if object.Kind != "" {
			objects = append(objects, object)
		}
	}
	return objects
}

func renderedMembershipAuthority(t *testing.T, objects []renderedMembershipObject) map[string]renderedMembershipObject {
	t.Helper()
	authority := make(map[string]renderedMembershipObject)
	for _, object := range objects {
		if object.Kind != "ConfigMap" {
			continue
		}
		for _, logicalName := range []string{"keycloak-desired-state", "tenant-catalog"} {
			if !strings.HasPrefix(object.Metadata.Name, logicalName+"-") {
				continue
			}
			if !object.Immutable || object.Metadata.Namespace != "noebs" || object.Metadata.Labels["app.kubernetes.io/part-of"] != "noebs" {
				t.Fatalf("rendered %s ConfigMap is not immutable and app-owned: %#v", logicalName, object.Metadata)
			}
			if _, exists := authority[logicalName]; exists {
				t.Fatalf("rendered duplicate %s ConfigMap", logicalName)
			}
			authority[logicalName] = object
		}
	}
	if len(authority) != 2 {
		t.Fatalf("rendered Keycloak authority ConfigMaps = %v", authority)
	}
	return authority
}

func renderedMembershipJob(t *testing.T, objects []renderedMembershipObject) renderedMembershipObject {
	t.Helper()
	for _, object := range objects {
		if object.Kind == "Job" && object.Metadata.Name == "noebs-keycloak-membership-assignment" {
			return object
		}
	}
	t.Fatal("rendered membership Job not found")
	return renderedMembershipObject{}
}

func readOperationImageDigest(t *testing.T, path string) string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kustomization struct {
		Images []struct {
			Name   string `yaml:"name"`
			Digest string `yaml:"digest"`
		} `yaml:"images"`
	}
	if err := yaml.Unmarshal(payload, &kustomization); err != nil {
		t.Fatal(err)
	}
	for _, image := range kustomization.Images {
		if image.Name == "ghcr.io/noebs/noebs" {
			return image.Digest
		}
	}
	t.Fatalf("%s does not pin ghcr.io/noebs/noebs", path)
	return ""
}

func writeCLIMembershipConfig(t *testing.T, baseURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	payload := `base_url: ` + baseURL + `
admin_realm: noebs
client_id: noebs-keycloak-reconciler
client_secret: steady-reconciler-secret
client_credentials:
  noebs-keycloak-reconciler:
    client_secret: steady-reconciler-secret
  noebs-backoffice:
    client_secret: backoffice-secret
identity_providers:
  google:
    client_id: google-client
    client_secret: google-secret
`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type cliMembershipFake struct {
	mu     sync.Mutex
	writes int
}

func newCLIMembershipFake() *cliMembershipFake { return &cliMembershipFake{} }

func (f *cliMembershipFake) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/realms/noebs/protocol/openid-connect/token" {
		writeCLIMembershipJSON(writer, http.StatusOK, map[string]string{"access_token": "admin-token", "token_type": "Bearer"})
		return
	}
	if request.Header.Get("Authorization") != "Bearer admin-token" {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodGet {
		f.mu.Lock()
		f.writes++
		f.mu.Unlock()
		http.Error(writer, "unexpected write", http.StatusInternalServerError)
		return
	}
	const base = "/admin/realms/noebs"
	switch {
	case request.URL.Path == base+"/clients" && request.URL.Query().Get("clientId") == "noebs-api":
		writeCLIMembershipJSON(writer, http.StatusOK, []map[string]any{{"id": "client-noebs-api", "clientId": "noebs-api"}})
	case request.URL.Path == base+"/clients/client-noebs-api/roles":
		writeCLIMembershipJSON(writer, http.StatusOK, cliMembershipClientRoles())
	case request.URL.Path == base+"/users":
		if request.URL.Query().Get("email") != "user@example.com" || request.URL.Query().Get("exact") != "true" {
			http.Error(writer, "lookup is not exact", http.StatusBadRequest)
			return
		}
		writeCLIMembershipJSON(writer, http.StatusOK, []map[string]string{{"id": cliMembershipSubject, "email": "user@example.com"}})
	case request.URL.Path == base+"/users/"+cliMembershipSubject:
		writeCLIMembershipJSON(writer, http.StatusOK, map[string]string{"id": cliMembershipSubject, "email": "user@example.com"})
	case request.URL.Path == base+"/organizations":
		writeCLIMembershipJSON(writer, http.StatusOK, cliMembershipOrganizations())
	case strings.Contains(request.URL.Path, "/organizations/") && strings.HasSuffix(request.URL.Path, "/children"):
		writeCLIMembershipJSON(writer, http.StatusOK, []map[string]any{})
	case strings.Contains(request.URL.Path, "/organizations/") && strings.HasSuffix(request.URL.Path, "/role-mappings"):
		parts := strings.Split(strings.TrimPrefix(request.URL.Path, base+"/organizations/"), "/")
		writeCLIMembershipJSON(writer, http.StatusOK, cliMembershipRoleMappings(parts[2]))
	case strings.HasSuffix(request.URL.Path, "/groups") && strings.Contains(request.URL.Path, "/members/"):
		http.Error(writer, "not a member", http.StatusNotFound)
	case strings.Contains(request.URL.Path, "/organizations/") && strings.HasSuffix(request.URL.Path, "/groups"):
		organizationID := strings.Split(strings.TrimPrefix(request.URL.Path, base+"/organizations/"), "/")[0]
		writeCLIMembershipJSON(writer, http.StatusOK, cliMembershipGroups(organizationID))
	case strings.Contains(request.URL.Path, "/organizations/") && strings.Contains(request.URL.Path, "/members/"):
		http.Error(writer, "not a member", http.StatusNotFound)
	default:
		http.Error(writer, "unexpected request "+request.URL.RequestURI(), http.StatusNotFound)
	}
}

func (f *cliMembershipFake) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func cliMembershipOrganizations() []map[string]any {
	return []map[string]any{
		{"id": "org-tenant-cutover", "alias": "tenant-cutover", "name": "Tenant Cutover", "enabled": true, "attributes": map[string][]string{"noebs.managed": {"true"}}},
		{"id": "org-tenant-sandbox", "alias": "tenant-sandbox", "name": "Tenant Sandbox", "enabled": true, "attributes": map[string][]string{"noebs.managed": {"true"}}},
	}
}

func cliMembershipGroups(organizationID string) []map[string]any {
	tenant := strings.TrimPrefix(organizationID, "org-")
	return []map[string]any{
		{"id": "group-" + tenant + "-user", "name": "user", "description": "Tenant users", "attributes": map[string][]string{"noebs.managed": {"true"}}},
		{"id": "group-" + tenant + "-backoffice", "name": "backoffice", "description": "Tenant back-office operators", "attributes": map[string][]string{"noebs.managed": {"true"}}},
		{"id": "group-" + tenant + "-tenant-admin", "name": "tenant-admin", "description": "Tenant administrators", "attributes": map[string][]string{"noebs.managed": {"true"}}},
	}
}

func cliMembershipClientRoles() []map[string]any {
	descriptions := map[string]string{
		"user":                    "Tenant user",
		"backoffice":              "Tenant back-office operator",
		"tenant-admin":            "Tenant administrator",
		"reporting:read":          "Read tenant reports",
		"wallet:read":             "Read tenant wallet state",
		"wallet:audit:read":       "Audit tenant wallet activity",
		"wallet:manual:create":    "Create manual operations",
		"wallet:fees:write":       "Change tenant fee configuration",
		"wallet:rates:write":      "Change tenant rate configuration",
		"wallet:workflow:approve": "Approve tenant workflows",
		"wallet:workflow:reject":  "Reject tenant workflows",
	}
	roles := make([]map[string]any, 0, len(descriptions))
	for name, description := range descriptions {
		roles = append(roles, cliMembershipClientRole(name, "[managed-by:noebs] "+description))
	}
	slices.SortFunc(roles, func(left, right map[string]any) int {
		return strings.Compare(left["name"].(string), right["name"].(string))
	})
	return roles
}

func cliMembershipRoleMappings(groupID string) map[string]any {
	var names []string
	switch {
	case strings.HasSuffix(groupID, "-user"):
		names = []string{"user"}
	case strings.HasSuffix(groupID, "-backoffice"):
		names = []string{"backoffice", "reporting:read", "wallet:read", "wallet:audit:read"}
	case strings.HasSuffix(groupID, "-tenant-admin"):
		names = []string{
			"tenant-admin", "reporting:read", "wallet:read", "wallet:audit:read", "wallet:manual:create",
			"wallet:fees:write", "wallet:rates:write", "wallet:workflow:approve", "wallet:workflow:reject",
		}
	default:
		panic("unexpected membership group " + groupID)
	}
	mappings := make([]map[string]any, 0, len(names))
	for _, name := range names {
		mappings = append(mappings, cliMembershipClientRole(name, ""))
	}
	return map[string]any{
		"realmMappings": []any{},
		"clientMappings": map[string]any{
			"noebs-api": map[string]any{"id": "client-noebs-api", "client": "noebs-api", "mappings": mappings},
		},
	}
}

func cliMembershipClientRole(name, description string) map[string]any {
	return map[string]any{
		"id":          "role-" + name,
		"name":        name,
		"description": description,
		"clientRole":  true,
		"containerId": "client-noebs-api",
	}
}

func writeCLIMembershipJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
