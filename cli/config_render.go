package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath  = "/app/config.yaml"
	defaultSecretsPath = "/app/secrets.yaml"
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

	outputDir := filepath.Dir(configPath)
	outputDBPath := filepath.Join(outputDir, ".db_path")
	outputLitestream := litestreamOutputPath(outputDir)

	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	configMap := map[string]interface{}{}
	if err := yaml.Unmarshal(configData, &configMap); err != nil {
		return fmt.Errorf("parse config yaml: %w", err)
	}

	secretsMap := map[string]interface{}{}
	if secretsPath != "" {
		decrypted, err := decryptSopsFile(secretsPath)
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
	if isEmptyValue(noebs["db_path"]) {
		noebs["db_path"] = "/data/noebs.db"
	}

	if err := os.WriteFile(outputDBPath, []byte(fmt.Sprint(noebs["db_path"])), 0600); err != nil {
		return fmt.Errorf("write db path: %w", err)
	}

	if err := writeLitestreamConfig(merged, noebs, outputLitestream); err != nil {
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

func litestreamOutputPath(fallbackDir string) string {
	if os.Geteuid() == 0 {
		return "/etc/litestream.yml"
	}
	return filepath.Join(fallbackDir, "litestream.yml")
}

func decryptSopsFile(path string) ([]byte, error) {
	cmd := exec.Command("sops", "-d", path)
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

func isEmptyValue(value interface{}) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		return typed == ""
	case []interface{}:
		return len(typed) == 0
	default:
		return false
	}
}

func writeLitestreamConfig(merged map[string]interface{}, noebs map[string]interface{}, outputPath string) error {
	litestream := getMap(merged, "litestream")
	if litestream == nil {
		return nil
	}

	dbs, ok := litestream["dbs"].([]interface{})
	if !ok || len(dbs) == 0 {
		return nil
	}

	r2 := getMap(merged, "cloudflare_r2")
	if r2 == nil {
		r2 = getMap(merged, "cloudflare")
	}

	accessKey := firstString(r2, "access_key_id", "access-key-id")
	secretKey := firstString(r2, "secret_access_key", "secret-access-key")
	endpoint := firstString(r2, "endpoint")

	if accessKey == "" || secretKey == "" {
		return nil
	}

	dbPath := fmt.Sprint(noebs["db_path"])

	for _, dbEntry := range dbs {
		dbMap, ok := dbEntry.(map[string]interface{})
		if !ok {
			continue
		}
		if isEmptyValue(dbMap["path"]) {
			dbMap["path"] = dbPath
		}
		replicas, ok := dbMap["replicas"].([]interface{})
		if !ok {
			continue
		}
		for _, replicaEntry := range replicas {
			replica, ok := replicaEntry.(map[string]interface{})
			if !ok {
				continue
			}
			if replicaType, _ := replica["type"].(string); replicaType != "s3" {
				continue
			}
			if isEmptyValue(replica["access-key-id"]) {
				replica["access-key-id"] = accessKey
			}
			if isEmptyValue(replica["secret-access-key"]) {
				replica["secret-access-key"] = secretKey
			}
			if endpoint != "" && isEmptyValue(replica["endpoint"]) {
				replica["endpoint"] = endpoint
			}
		}
	}

	litestreamOut := map[string]interface{}{"dbs": dbs}
	payload, err := yaml.Marshal(litestreamOut)
	if err != nil {
		return fmt.Errorf("encode litestream config: %w", err)
	}
	if err := os.WriteFile(outputPath, payload, 0600); err != nil {
		return fmt.Errorf("write litestream config: %w", err)
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
