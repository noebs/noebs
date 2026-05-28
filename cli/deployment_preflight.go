package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"gopkg.in/yaml.v3"
)

type deploymentDecryptFunc func(path, ageKeyFile string) ([]byte, error)

func validateDeploymentCommand() error {
	if len(os.Args) != 3 {
		return errors.New("usage: noebs validate-deployment <compose-root>")
	}
	return validateDeploymentRoot(os.Args[2])
}

func validateKubernetesDeploymentCommand() error {
	if len(os.Args) != 3 {
		return errors.New("usage: noebs validate-kubernetes-deployment <mounted-root>")
	}
	return validateKubernetesDeploymentRoot(os.Args[2])
}

func validateDeploymentRoot(root string) error {
	return validateDeploymentRootWithDecrypt(root, decryptSopsFile)
}

func validateKubernetesDeploymentRoot(root string) error {
	return validateKubernetesDeploymentRootWithDecrypt(root, decryptSopsFile)
}

func validateDeploymentRootWithDecrypt(root string, decrypt deploymentDecryptFunc) error {
	root, err := resolveDeploymentRoot(root)
	if err != nil {
		return err
	}
	if err := requireReadableFile("docker-compose.yml", filepath.Join(root, "docker-compose.yml")); err != nil {
		return err
	}
	if decrypt == nil {
		return errors.New("deployment decrypt function is required")
	}

	configPath := filepath.Join(root, "config.docker.yaml")
	ageKeyPath := filepath.Join(root, ".sops", "age-key.txt")
	if err := requireReadableFile("config.docker.yaml", configPath); err != nil {
		return err
	}
	if err := requireReadableFile("SOPS age key", ageKeyPath); err != nil {
		return err
	}

	configMap, err := readYAMLMapFile(configPath)
	if err != nil {
		return err
	}
	if err := validateDeploymentBootstrapSecret(root, decrypt, ageKeyPath); err != nil {
		return err
	}
	if err := validatePlainSecretFile("Temporal Postgres password", filepath.Join(root, "deploy", "docker", "temporal", "postgres-password.txt")); err != nil {
		return err
	}
	if err := validatePlainSecretFile("Keycloak Postgres password", filepath.Join(root, "deploy", "docker", "keycloak", "postgres-password.txt")); err != nil {
		return err
	}
	if err := validateKeycloakConfig(filepath.Join(root, "deploy", "docker", "keycloak", "keycloak.conf")); err != nil {
		return err
	}
	if err := validateExactDockerReleaseFiles(root); err != nil {
		return err
	}

	serviceFiles, err := filepath.Glob(filepath.Join(root, "deploy", "docker", "services", "*.yaml"))
	if err != nil {
		return fmt.Errorf("list service configs: %w", err)
	}
	if len(serviceFiles) == 0 {
		return errors.New("no service configs found under deploy/docker/services")
	}
	for _, serviceFile := range serviceFiles {
		if err := validateDeploymentService(root, configMap, serviceFile, ageKeyPath, decrypt); err != nil {
			return err
		}
	}
	return nil
}

func validateExactDockerReleaseFiles(root string) error {
	expectedServiceFiles := make(map[string]bool, len(kubernetesSecretReleaseServiceNames))
	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		expectedServiceFiles[serviceName+".yaml"] = true
	}
	serviceDir := filepath.Join(root, "deploy", "docker", "services")
	if err := rejectUnexpectedDeploymentEntries("Docker Compose", "service config file", serviceDir, expectedServiceFiles); err != nil {
		return err
	}
	for fileName := range expectedServiceFiles {
		if err := requireReadableFile("Docker Compose service config", filepath.Join(serviceDir, fileName)); err != nil {
			return err
		}
	}

	expectedSecretFiles := make(map[string]bool, len(kubernetesServiceSecretSources))
	for _, source := range kubernetesServiceSecretSources {
		expectedSecretFiles[source.fileName] = true
	}
	return rejectUnexpectedDeploymentFiles("Docker Compose", "service secret file", filepath.Join(root, "deploy", "docker", "secrets"), ".secrets.yaml", expectedSecretFiles)
}

func validateKubernetesDeploymentRootWithDecrypt(root string, decrypt deploymentDecryptFunc) error {
	root, err := resolveDeploymentRoot(root)
	if err != nil {
		return err
	}
	if decrypt == nil {
		return errors.New("deployment decrypt function is required")
	}

	configPath := filepath.Join(root, "config.yaml")
	ageKeyPath := filepath.Join(root, ".sops", "age-key.txt")
	if err := requireReadableFile("config.yaml", configPath); err != nil {
		return err
	}
	if err := requireReadableFile("SOPS age key", ageKeyPath); err != nil {
		return err
	}
	if err := validateKubernetesPlatformInputs(root); err != nil {
		return err
	}

	configMap, err := readYAMLMapFile(configPath)
	if err != nil {
		return err
	}
	if err := validateKubernetesReleaseServices(root, configMap, ageKeyPath, decrypt); err != nil {
		return err
	}
	return nil
}

func validateKubernetesPlatformInputs(root string) error {
	for _, requiredFile := range []struct {
		label string
		path  string
	}{
		{label: "Noebs Postgres password", path: filepath.Join(root, "platform", "postgres-password.txt")},
		{label: "Temporal Postgres password", path: filepath.Join(root, "platform", "temporal-postgres-password.txt")},
		{label: "Keycloak Postgres password", path: filepath.Join(root, "platform", "keycloak-postgres-password.txt")},
	} {
		if err := validatePlainSecretFile(requiredFile.label, requiredFile.path); err != nil {
			return err
		}
	}
	if err := validateDockerConfigJSONFile("GHCR Docker config JSON", filepath.Join(root, "platform", "ghcr-dockerconfigjson")); err != nil {
		return err
	}
	if err := validateKeycloakConfig(filepath.Join(root, "platform", "keycloak.conf")); err != nil {
		return err
	}
	return nil
}

func validateKubernetesReleaseServices(root string, configMap map[string]interface{}, ageKeyPath string, decrypt deploymentDecryptFunc) error {
	if err := validateExactKubernetesReleaseFiles(root); err != nil {
		return err
	}
	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		servicePath := filepath.Join(root, "services", serviceName+".yaml")
		secretPath := filepath.Join(root, "secrets", serviceSecretFileName(serviceName))
		if err := validateDeploymentServiceWithSecretPath(configMap, servicePath, secretPath, ageKeyPath, decrypt); err != nil {
			return err
		}
	}
	return nil
}

func validateExactKubernetesReleaseFiles(root string) error {
	expectedRootEntries := map[string]bool{
		"config.yaml": true,
		".sops":       true,
		"platform":    true,
		"secrets":     true,
		"services":    true,
	}
	if err := rejectUnexpectedDeploymentEntries("Kubernetes", "root entry", root, expectedRootEntries); err != nil {
		return err
	}

	expectedSOPSFiles := map[string]bool{
		"age-key.txt": true,
	}
	if err := rejectUnexpectedDeploymentEntries("Kubernetes", "SOPS file", filepath.Join(root, ".sops"), expectedSOPSFiles); err != nil {
		return err
	}

	expectedPlatformFiles := map[string]bool{
		"postgres-password.txt":          true,
		"temporal-postgres-password.txt": true,
		"keycloak-postgres-password.txt": true,
		"ghcr-dockerconfigjson":          true,
		"keycloak.conf":                  true,
	}
	if err := rejectUnexpectedDeploymentEntries("Kubernetes", "platform file", filepath.Join(root, "platform"), expectedPlatformFiles); err != nil {
		return err
	}

	expectedServiceFiles := make(map[string]bool, len(kubernetesSecretReleaseServiceNames))
	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		expectedServiceFiles[serviceName+".yaml"] = true
	}
	if err := rejectUnexpectedDeploymentEntries("Kubernetes", "service config file", filepath.Join(root, "services"), expectedServiceFiles); err != nil {
		return err
	}

	expectedSecretFiles := make(map[string]bool, len(kubernetesServiceSecretSources))
	for _, source := range kubernetesServiceSecretSources {
		expectedSecretFiles[source.fileName] = true
	}
	if err := rejectUnexpectedDeploymentEntries("Kubernetes", "service secret file", filepath.Join(root, "secrets"), expectedSecretFiles); err != nil {
		return err
	}
	return nil
}

func rejectUnexpectedDeploymentEntries(releaseLabel, entryLabel, dir string, expected map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s release %s directory %s: %w", releaseLabel, entryLabel, dir, err)
	}
	for _, entry := range entries {
		if !expected[entry.Name()] {
			return fmt.Errorf("unexpected %s release %s %s", releaseLabel, entryLabel, filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}

func rejectUnexpectedDeploymentFiles(releaseLabel, entryLabel, dir, suffix string, expected map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s release %s directory %s: %w", releaseLabel, entryLabel, dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		if !expected[entry.Name()] {
			return fmt.Errorf("unexpected %s release %s %s", releaseLabel, entryLabel, filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}

func resolveDeploymentRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("deployment root is required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve deployment root: %w", err)
	}
	return absoluteRoot, nil
}

func validateDeploymentService(root string, configMap map[string]interface{}, serviceFile, ageKeyPath string, decrypt deploymentDecryptFunc) error {
	serviceName := strings.TrimSuffix(filepath.Base(serviceFile), ".yaml")
	secretPath := filepath.Join(root, "deploy", "docker", "secrets", serviceSecretFileName(serviceName))
	return validateDeploymentServiceWithSecretPath(configMap, serviceFile, secretPath, ageKeyPath, decrypt)
}

func validateDeploymentServiceWithSecretPath(configMap map[string]interface{}, serviceFile, secretPath, ageKeyPath string, decrypt deploymentDecryptFunc) error {
	serviceName := strings.TrimSuffix(filepath.Base(serviceFile), ".yaml")
	serviceConfigMap, err := readYAMLMapFile(serviceFile)
	if err != nil {
		return err
	}
	merged := mergeConfig(configMap, serviceConfigMap).(map[string]interface{})
	noebs := getMap(merged, "noebs")
	if noebs == nil {
		return fmt.Errorf("%s missing noebs config", serviceFile)
	}
	role, err := parseServiceRole(firstString(noebs, "service_role"))
	if err != nil {
		return fmt.Errorf("%s service_role: %w", serviceFile, err)
	}
	if string(role) != serviceName {
		return fmt.Errorf("%s service_role = %q, want %q", serviceFile, role, serviceName)
	}

	if err := requireReadableFile(serviceName+" secrets", secretPath); err != nil {
		return err
	}
	secretPayload, err := decrypt(secretPath, ageKeyPath)
	if err != nil {
		return fmt.Errorf("decrypt %s secrets: %w", serviceName, err)
	}
	secretMap, err := parseYAMLMap(secretPath, secretPayload)
	if err != nil {
		return err
	}
	merged = mergeConfig(merged, secretMap).(map[string]interface{})
	noebs = getMap(merged, "noebs")
	if noebs == nil {
		return fmt.Errorf("%s merged config missing noebs", serviceName)
	}
	if err := validateMergedDeploymentService(serviceName, role, noebs); err != nil {
		return err
	}
	return nil
}

func validateMergedDeploymentService(serviceName string, role serviceRole, noebs map[string]interface{}) error {
	if err := applyServiceDatabaseURL(noebs); err != nil {
		return fmt.Errorf("%s service database config: %w", serviceName, err)
	}
	if err := rejectLegacyDatabasePath(noebs); err != nil {
		return fmt.Errorf("%s database config: %w", serviceName, err)
	}
	defaultTenantID := firstString(noebs, "default_tenant_id")
	if _, err := validateTenantID(defaultTenantID); err != nil {
		return fmt.Errorf("%s default_tenant_id: %w", serviceName, err)
	}
	if err := validateRoleDatabaseConfig(role, firstString(noebs, "db_url"), firstString(noebs, "db_driver")); err != nil {
		return fmt.Errorf("%s database config: %w", serviceName, err)
	}
	var cfg ebs_fields.NoebsConfig
	payload, err := json.Marshal(noebs)
	if err != nil {
		return fmt.Errorf("%s encode merged config: %w", serviceName, err)
	}
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return fmt.Errorf("%s decode merged config: %w", serviceName, err)
	}
	if err := validateServiceDiscoveryCatalog(serviceName, cfg); err != nil {
		return err
	}
	if roleRequiresDataKey(role) && strings.TrimSpace(cfg.DataKey) == "" {
		return fmt.Errorf("%s data_key: %w", serviceName, store.ErrMissingDataKey)
	}
	if !role.runsMigrations() {
		if err := validateRoleRuntimeConfig(role, cfg); err != nil {
			return fmt.Errorf("%s runtime config: %w", serviceName, err)
		}
	}
	if role == serviceRolePSPWebhook || role == serviceRoleWalletWorker {
		if err := validatePSPSecretMap(noebs, defaultTenantID); err != nil {
			return fmt.Errorf("%s PSP secrets: %w", serviceName, err)
		}
	}
	if err := rejectPlaceholders("noebs."+serviceName, noebs); err != nil {
		return err
	}
	return nil
}

func validatePSPSecretMap(noebs map[string]interface{}, tenantID string) error {
	pspMap := getMap(noebs, "psp")
	if len(pspMap) == 0 {
		return errors.New("missing noebs.psp")
	}
	tenantMap := getMap(pspMap, tenantID)
	if len(tenantMap) == 0 {
		return fmt.Errorf("missing noebs.psp.%s", tenantID)
	}
	for providerCode, rawProvider := range tenantMap {
		providerMap, ok := rawProvider.(map[string]interface{})
		if !ok {
			return fmt.Errorf("noebs.psp.%s.%s must be a map", tenantID, providerCode)
		}
		for _, key := range []string{"api_key", "api_secret", "webhook_secret", "webhook_public_key"} {
			if strings.TrimSpace(firstString(providerMap, key)) == "" {
				return fmt.Errorf("noebs.psp.%s.%s missing %s", tenantID, providerCode, key)
			}
		}
		return nil
	}
	return fmt.Errorf("missing noebs.psp.%s provider", tenantID)
}

func validateDeploymentBootstrapSecret(root string, decrypt deploymentDecryptFunc, ageKeyPath string) error {
	secretPath := filepath.Join(root, "deploy", "docker", "postgres", "bootstrap.secrets.yaml")
	if err := requireReadableFile("Postgres bootstrap secret", secretPath); err != nil {
		return err
	}
	payload, err := decrypt(secretPath, ageKeyPath)
	if err != nil {
		return fmt.Errorf("decrypt Postgres bootstrap secret: %w", err)
	}
	secretMap, err := parseYAMLMap(secretPath, payload)
	if err != nil {
		return err
	}
	noebs := getMap(secretMap, "noebs")
	if noebs == nil {
		return errors.New("postgres bootstrap secret missing noebs")
	}
	if err := rejectLegacyDatabasePath(noebs); err != nil {
		return fmt.Errorf("postgres bootstrap database config: %w", err)
	}
	dbURL := firstString(noebs, "db_url")
	if err := validateDatabaseURLPassword("Postgres bootstrap db_url", dbURL); err != nil {
		return err
	}
	return rejectPlaceholders("Postgres bootstrap secret", noebs)
}

func validateDatabaseURLPassword(label, value string) error {
	_, err := databaseURLPassword(label, value)
	return err
}

func databaseURLPassword(label, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%s parse: %w", label, err)
	}
	if parsed.User == nil {
		return "", fmt.Errorf("%s missing user info", label)
	}
	password, ok := parsed.User.Password()
	if !ok || strings.TrimSpace(password) == "" {
		return "", fmt.Errorf("%s missing password", label)
	}
	return password, nil
}

func validatePlainSecretFile(label, path string) error {
	if err := requireReadableFile(label, path); err != nil {
		return err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", label, err)
	}
	value := strings.TrimSpace(string(payload))
	if value == "" {
		return fmt.Errorf("%s is empty", label)
	}
	if strings.Contains(value, "REPLACE_WITH_") {
		return fmt.Errorf("%s contains placeholder", label)
	}
	return nil
}

type dockerConfigJSON struct {
	Auths map[string]struct {
		Auth string `json:"auth"`
	} `json:"auths"`
}

func validateDockerConfigJSONFile(label, path string) error {
	if err := requireReadableFile(label, path); err != nil {
		return err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", label, err)
	}
	return validateDockerConfigJSONPayload(label, string(payload))
}

func validateDockerConfigJSONPayload(label, payload string) error {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return fmt.Errorf("%s is empty", label)
	}
	if strings.Contains(payload, "REPLACE_WITH_") {
		return fmt.Errorf("%s contains placeholder", label)
	}
	var config dockerConfigJSON
	if err := json.Unmarshal([]byte(payload), &config); err != nil {
		return fmt.Errorf("parse %s: %w", label, err)
	}
	entry, ok := config.Auths["ghcr.io"]
	if !ok {
		return fmt.Errorf("%s missing auths.ghcr.io", label)
	}
	if strings.TrimSpace(entry.Auth) == "" {
		return fmt.Errorf("%s auths.ghcr.io.auth is empty", label)
	}
	return nil
}

func validateKeycloakConfig(path string) error {
	if err := requireReadableFile("Keycloak config", path); err != nil {
		return err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Keycloak config: %w", err)
	}
	if strings.Contains(string(payload), "REPLACE_WITH_") {
		return errors.New("keycloak config contains placeholder")
	}
	values := map[string]string{}
	for lineIndex, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("keycloak config line %d must be key=value", lineIndex+1)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return fmt.Errorf("keycloak config line %d has empty key or value", lineIndex+1)
		}
		values[key] = value
	}
	for _, key := range []string{
		"http-enabled",
		"http-port",
		"health-enabled",
		"metrics-enabled",
		"db",
		"db-url",
		"db-username",
		"db-password",
		"bootstrap-admin-username",
		"bootstrap-admin-password",
	} {
		if strings.TrimSpace(values[key]) == "" {
			return fmt.Errorf("keycloak config missing %s", key)
		}
	}
	return nil
}

func readYAMLMapFile(path string) (map[string]interface{}, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseYAMLMap(path, payload)
}

func parseYAMLMap(path string, payload []byte) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if err := yaml.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return result, nil
}

func requireReadableFile(label, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s not found at %s", label, path)
		}
		return fmt.Errorf("stat %s at %s: %w", label, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory at %s", label, path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s is empty at %s", label, path)
	}
	return nil
}

func serviceSecretFileName(serviceName string) string {
	switch serviceName {
	case "identity-auth-migrate":
		return "identity-auth.secrets.yaml"
	case "card-vault-migrate":
		return "card-vault.secrets.yaml"
	case "ebs-adapter-migrate":
		return "ebs-adapter.secrets.yaml"
	case "ebs-adapter-events":
		return "ebs-adapter.secrets.yaml"
	case "psp-webhook-migrate":
		return "psp-webhook.secrets.yaml"
	case "admin-reporting-migrate":
		return "admin-reporting.secrets.yaml"
	case "admin-reporting-projector":
		return "admin-reporting.secrets.yaml"
	case "notification-chat-migrate":
		return "notification-chat.secrets.yaml"
	case "consumer-beneficiary-migrate":
		return "consumer-beneficiary.secrets.yaml"
	case "wallet-ledger-migrate":
		return "wallet-ledger.secrets.yaml"
	default:
		return serviceName + ".secrets.yaml"
	}
}

func rejectPlaceholders(path string, value interface{}) error {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if err := rejectPlaceholders(path+"."+key, child); err != nil {
				return err
			}
		}
	case []interface{}:
		for index, child := range typed {
			if err := rejectPlaceholders(fmt.Sprintf("%s[%d]", path, index), child); err != nil {
				return err
			}
		}
	case string:
		if strings.Contains(typed, "REPLACE_WITH_") {
			return fmt.Errorf("%s contains placeholder", path)
		}
	}
	return nil
}
