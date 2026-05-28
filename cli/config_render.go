package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath        = "/app/config.yaml"
	defaultServiceConfigPath = "/app/service.yaml"
	defaultSecretsPath       = "/app/secrets.yaml"
)

var errMissingSopsAgeKeyFile = errors.New("missing SOPS age key file")

func isRenderConfigCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "render-config"
}

func isRenderDatabasePasswordCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "render-db-password"
}

func isValidateDeploymentCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "validate-deployment"
}

func isValidateKubernetesDeploymentCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "validate-kubernetes-deployment"
}

func isRenderKubernetesSecretsCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "render-kubernetes-secrets"
}

func isPrepareKubernetesReleaseCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "prepare-kubernetes-release"
}

func isConfigUtilityCommand() bool {
	return isRenderConfigCommand() ||
		isRenderDatabasePasswordCommand() ||
		isValidateDeploymentCommand() ||
		isValidateKubernetesDeploymentCommand() ||
		isRenderKubernetesSecretsCommand() ||
		isPrepareKubernetesReleaseCommand() ||
		isAuditKubernetesReleaseInputsCommand()
}

func renderConfigFiles() error {
	noebs, configPath, err := loadMergedConfigForRender(true)
	if err != nil {
		return err
	}

	role, err := parseServiceRole(firstString(noebs, "service_role"))
	if err != nil {
		return err
	}
	if err := applyServiceDatabaseURL(noebs); err != nil {
		return err
	}
	if err := rejectLegacyDatabasePath(noebs); err != nil {
		return err
	}
	if err := validateRoleDatabaseConfig(role, firstString(noebs, "db_url"), firstString(noebs, "db_driver")); err != nil {
		return err
	}
	outputDir := filepath.Dir(configPath)
	if runtimeDir := firstString(noebs, "runtime_dir"); runtimeDir != "" {
		outputDir = runtimeDir
	}
	defaultTenantID, _ := noebs["default_tenant_id"].(string)
	if _, err := validateTenantID(defaultTenantID); err != nil {
		return fmt.Errorf("runtime config default_tenant_id: %w", err)
	}

	if err := os.MkdirAll(outputDir, 0700); err != nil {
		return fmt.Errorf("create runtime config dir: %w", err)
	}

	if err := writeDatabasePassword(noebs); err != nil {
		return err
	}

	return nil
}

func renderDatabasePasswordFile() error {
	noebs, _, err := loadMergedConfigForRender(false)
	if err != nil {
		return err
	}
	if firstString(noebs, "render_db_password_file") == "" {
		return errors.New("render_db_password_file is required")
	}
	return writeDatabasePassword(noebs)
}

func loadMergedConfigForRender(requireServiceConfig bool) (map[string]interface{}, string, error) {
	configPath := defaultConfigPath
	if isTestRun() {
		configPath = "./config.yaml"
	}
	if _, err := requiredExistingPath("config", configPath); err != nil {
		return nil, "", err
	}

	secretsPath := defaultSecretsPath
	if isTestRun() {
		secretsPath = "./secrets.yaml"
	}
	secretsPath, err := optionalExistingPath(secretsPath)
	if err != nil {
		return nil, "", err
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("read config: %w", err)
	}

	configMap := map[string]interface{}{}
	if err := yaml.Unmarshal(configData, &configMap); err != nil {
		return nil, "", fmt.Errorf("parse config yaml: %w", err)
	}
	serviceConfigPath := defaultServiceConfigPath
	if isTestRun() {
		serviceConfigPath = "./service.yaml"
	}
	if requireServiceConfig {
		if _, err := requiredExistingPath("service config", serviceConfigPath); err != nil {
			return nil, "", err
		}
	}
	serviceConfigPath, err = optionalExistingPath(serviceConfigPath)
	if err != nil {
		return nil, "", err
	}
	if serviceConfigPath != "" {
		serviceConfigData, err := os.ReadFile(serviceConfigPath)
		if err != nil {
			return nil, "", fmt.Errorf("read service config: %w", err)
		}
		serviceConfigMap := map[string]interface{}{}
		if err := yaml.Unmarshal(serviceConfigData, &serviceConfigMap); err != nil {
			return nil, "", fmt.Errorf("parse service config yaml: %w", err)
		}
		configMap = mergeConfig(configMap, serviceConfigMap).(map[string]interface{})
	}
	configNoebs := getMap(configMap, "noebs")

	secretsMap := map[string]interface{}{}
	if secretsPath != "" {
		decrypted, err := decryptSopsFile(secretsPath, firstString(configNoebs, "sops_age_key_file"))
		if err != nil {
			return nil, "", err
		}
		if err := yaml.Unmarshal(decrypted, &secretsMap); err != nil {
			return nil, "", fmt.Errorf("parse secrets yaml: %w", err)
		}
	}

	merged := mergeConfig(configMap, secretsMap).(map[string]interface{})
	noebs := getMap(merged, "noebs")
	if noebs == nil {
		noebs = map[string]interface{}{}
	}
	return noebs, configPath, nil
}

func requiredExistingPath(label, path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%s not found at %s", label, path)
		}
		return "", fmt.Errorf("stat %s %s: %w", label, path, err)
	}
	return path, nil
}

func optionalExistingPath(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	return path, nil
}

func sopsExecutable() (string, error) {
	path, err := exec.LookPath("sops")
	if err != nil {
		return "", fmt.Errorf("find sops executable: %w", err)
	}
	return path, nil
}

func mergeConfig(base, override interface{}) interface{} {
	if override == nil {
		return nil
	}

	switch overrideTyped := override.(type) {
	case map[string]interface{}:
		baseMap, ok := base.(map[string]interface{})
		if !ok {
			baseMap = map[string]interface{}{}
		}
		result := map[string]interface{}{}
		for key, value := range baseMap {
			result[key] = value
		}
		for key, value := range overrideTyped {
			result[key] = mergeConfig(result[key], value)
		}
		return result
	case []interface{}:
		return overrideTyped
	case string:
		return overrideTyped
	default:
		return override
	}
}

func applyServiceDatabaseURL(noebs map[string]interface{}) error {
	if noebs == nil {
		return nil
	}
	rawDatabases, ok := noebs["service_databases"]
	if !ok || rawDatabases == nil {
		return nil
	}
	databases, ok := rawDatabases.(map[string]interface{})
	if !ok {
		return errors.New("noebs.service_databases must be a map")
	}
	if err := validateServiceDatabaseOwners(databases); err != nil {
		return err
	}
	role := firstString(noebs, "service_role")
	if role == "" {
		if firstString(noebs, "db_url") != "" {
			return nil
		}
		return errors.New("noebs.service_role is required when noebs.service_databases is set")
	}
	parsedRole, err := parseServiceRole(role)
	if err != nil {
		return err
	}
	ownerRole, opensDatabase := parsedRole.databaseOwnerRole()
	if !opensDatabase {
		return validateRoleDatabaseConfig(parsedRole, firstString(noebs, "db_url"), firstString(noebs, "db_driver"))
	}
	rawDBURL, ok := databases[string(ownerRole)]
	if !ok {
		return fmt.Errorf("noebs.service_databases missing %q", ownerRole)
	}
	dbURL, ok := rawDBURL.(string)
	if !ok || strings.TrimSpace(dbURL) == "" {
		return fmt.Errorf("noebs.service_databases.%s must be a non-empty db_url", ownerRole)
	}
	noebs["db_url"] = strings.TrimSpace(dbURL)
	return nil
}

func validateServiceDatabaseOwners(databases map[string]interface{}) error {
	for key := range databases {
		role, err := parseServiceRole(key)
		if err != nil {
			return fmt.Errorf("noebs.service_databases.%s: %w", key, err)
		}
		ownerRole, opensDatabase := role.databaseOwnerRole()
		if !opensDatabase {
			return fmt.Errorf("%w: noebs.service_databases.%s", errDatabaseNotAllowed, role)
		}
		if ownerRole != role {
			return fmt.Errorf("%w: noebs.service_databases.%s belongs to %s", errDatabaseOwnerKey, role, ownerRole)
		}
	}
	return nil
}

func rejectLegacyDatabasePath(noebs map[string]interface{}) error {
	if firstString(noebs, "db_path") != "" {
		return fmt.Errorf("%w: noebs.db_path", errDatabaseNotAllowed)
	}
	return nil
}

func getMap(source map[string]interface{}, key string) map[string]interface{} {
	if source == nil {
		return nil
	}
	value, ok := source[key]
	if !ok {
		return nil
	}
	if typed, ok := value.(map[string]interface{}); ok {
		return typed
	}
	return nil
}

func writeDatabasePassword(noebs map[string]interface{}) error {
	outputPath := firstString(noebs, "render_db_password_file")
	if outputPath == "" {
		return nil
	}
	dbURL := strings.TrimSpace(fmt.Sprint(noebs["db_url"]))
	if dbURL == "" {
		return fmt.Errorf("db_url is required to render database password")
	}
	parsed, err := url.Parse(dbURL)
	if err != nil {
		return fmt.Errorf("parse db_url: %w", err)
	}
	if parsed.User == nil {
		return fmt.Errorf("db_url missing user info")
	}
	password, ok := parsed.User.Password()
	if !ok || password == "" {
		return fmt.Errorf("db_url missing password")
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0700); err != nil {
		return fmt.Errorf("create db password dir: %w", err)
	}
	if err := os.WriteFile(outputPath, []byte(password), 0600); err != nil {
		return fmt.Errorf("write db password: %w", err)
	}
	return nil
}

func firstString(source map[string]interface{}, keys ...string) string {
	if source == nil {
		return ""
	}
	for _, key := range keys {
		value, ok := source[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok && text != "" {
			return text
		}
	}
	return ""
}
