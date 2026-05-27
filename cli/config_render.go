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

func isRenderConfigCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "render-config"
}

func renderConfigFiles() error {
	configPath := firstExistingPath(defaultConfigPath, "./config.yaml")
	if configPath == "" {
		return errors.New("config.yaml not found")
	}

	secretsPath := firstExistingPath(defaultSecretsPath, "./secrets.yaml")

	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	configMap := map[string]interface{}{}
	if err := yaml.Unmarshal(configData, &configMap); err != nil {
		return fmt.Errorf("parse config yaml: %w", err)
	}
	if serviceConfigPath := firstExistingPath(defaultServiceConfigPath, "./service.yaml"); serviceConfigPath != "" {
		serviceConfigData, err := os.ReadFile(serviceConfigPath)
		if err != nil {
			return fmt.Errorf("read service config: %w", err)
		}
		serviceConfigMap := map[string]interface{}{}
		if err := yaml.Unmarshal(serviceConfigData, &serviceConfigMap); err != nil {
			return fmt.Errorf("parse service config yaml: %w", err)
		}
		configMap = mergeConfig(configMap, serviceConfigMap).(map[string]interface{})
	}
	configNoebs := getMap(configMap, "noebs")

	secretsMap := map[string]interface{}{}
	if secretsPath != "" {
		decrypted, err := decryptSopsFile(secretsPath, firstString(configNoebs, "sops_age_key_file"))
		if err != nil {
			return err
		}
		if err := yaml.Unmarshal(decrypted, &secretsMap); err != nil {
			return fmt.Errorf("parse secrets yaml: %w", err)
		}
	}

	merged := mergeConfig(configMap, secretsMap).(map[string]interface{})
	noebs := getMap(merged, "noebs")
	if noebs == nil {
		noebs = map[string]interface{}{}
	}
	if err := applyServiceDatabaseURL(noebs); err != nil {
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

func firstExistingPath(paths ...string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func decryptSopsFile(path, ageKeyFile string) ([]byte, error) {
	cmd := exec.Command("sops", "-d", path)
	if ageKeyFile != "" {
		cmd.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+ageKeyFile)
	}
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sops -d %s: %w", path, err)
	}
	return output, nil
}

func mergeConfig(base, override interface{}) interface{} {
	if override == nil {
		return base
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
		if len(overrideTyped) == 0 {
			return base
		}
		return overrideTyped
	case string:
		if overrideTyped == "" {
			return base
		}
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
		return validateRoleDatabaseConfig(parsedRole, firstString(noebs, "db_url"), firstString(noebs, "db_path"), firstString(noebs, "db_driver"))
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
