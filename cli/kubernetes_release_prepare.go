package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type kubernetesSecretEncryptFunc func(label string, payload []byte, ageKeyPath string) ([]byte, error)

type kubernetesReleaseInputs struct {
	Noebs kubernetesReleaseNoebsInputs `yaml:"noebs"`
}

type kubernetesReleaseNoebsInputs struct {
	DefaultTenantID                string                          `yaml:"default_tenant_id"`
	AdminKey                       string                          `yaml:"admin_key"`
	AdminUser                      string                          `yaml:"admin_user"`
	AdminPassword                  string                          `yaml:"admin_password"`
	SMSKey                         string                          `yaml:"sms_key"`
	SMSSender                      string                          `yaml:"sms_sender"`
	SMSGateway                     string                          `yaml:"sms_gateway"`
	SMSMessage                     string                          `yaml:"sms_message"`
	GoogleRedirectURL              string                          `yaml:"google_redirect_url"`
	CardVaultDataKey               string                          `yaml:"card_vault_data_key"`
	TemporalPostgresPassword       string                          `yaml:"temporal_postgres_password"`
	KeycloakPostgresPassword       string                          `yaml:"keycloak_postgres_password"`
	KeycloakBootstrapAdminUsername string                          `yaml:"keycloak_bootstrap_admin_username"`
	KeycloakBootstrapAdminPassword string                          `yaml:"keycloak_bootstrap_admin_password"`
	EBS                            kubernetesReleaseEBSInputs      `yaml:"ebs"`
	PSP                            map[string]map[string]pspSecret `yaml:"psp"`
}

type kubernetesReleaseEBSInputs struct {
	ConsumerEndpoint string `yaml:"consumer_endpoint"`
	MerchantEndpoint string `yaml:"merchant_endpoint"`
	IPINEndpoint     string `yaml:"ipin_endpoint"`
	ConsumerAppID    string `yaml:"consumer_app_id"`
	MerchantAppID    string `yaml:"merchant_app_id"`
	IPINUsername     string `yaml:"ipin_username"`
	IPINPassword     string `yaml:"ipin_password"`
	PublicKey        string `yaml:"pub_key"`
	IPINKey          string `yaml:"ipin_key"`
	PAN              string `yaml:"pan"`
	PIN              string `yaml:"pin"`
	IPIN             string `yaml:"ipin"`
	Expiry           string `yaml:"exp_date"`
}

type pspSecret struct {
	APIKey           string `yaml:"api_key"`
	APISecret        string `yaml:"api_secret"`
	WebhookSecret    string `yaml:"webhook_secret"`
	WebhookPublicKey string `yaml:"webhook_public_key"`
}

type preparedKubernetesRelease struct {
	configData map[string]string
	legacy     map[string]interface{}
	inputs     kubernetesReleaseInputs
	ageKeyPath string
}

func prepareKubernetesReleaseCommand() error {
	if len(os.Args) != 6 {
		return errors.New("usage: noebs prepare-kubernetes-release <repo-root> <legacy-root> <inputs-yaml> <output-root>")
	}
	return prepareKubernetesRelease(os.Args[2], os.Args[3], os.Args[4], os.Args[5], decryptSopsFile, encryptSopsYAML)
}

func prepareKubernetesRelease(repoRoot, legacyRoot, inputsPath, outputRoot string, decrypt deploymentDecryptFunc, encrypt kubernetesSecretEncryptFunc) error {
	repoRoot, err := resolveDeploymentRoot(repoRoot)
	if err != nil {
		return err
	}
	legacyRoot, err = resolveDeploymentRoot(legacyRoot)
	if err != nil {
		return err
	}
	outputRoot, err = resolveKubernetesReleaseOutputRoot(outputRoot)
	if err != nil {
		return err
	}
	inputsPath = strings.TrimSpace(inputsPath)
	if inputsPath == "" {
		return errors.New("kubernetes release inputs path is required")
	}
	if decrypt == nil {
		return errors.New("deployment decrypt function is required")
	}
	if encrypt == nil {
		return errors.New("kubernetes secret encrypt function is required")
	}

	ageKeyPath := filepath.Join(legacyRoot, ".sops", "age-key.txt")
	if err := requireReadableFile("SOPS age key", ageKeyPath); err != nil {
		return err
	}
	configData, err := readNoebsKubernetesConfigMapData(repoRoot)
	if err != nil {
		return err
	}
	legacy, err := readLegacyNoebsConfig(legacyRoot, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	inputs, err := readKubernetesReleaseInputs(inputsPath, ageKeyPath, decrypt)
	if err != nil {
		return err
	}

	release := preparedKubernetesRelease{
		configData: configData,
		legacy:     legacy,
		inputs:     inputs,
		ageKeyPath: ageKeyPath,
	}
	if err := release.validate(); err != nil {
		return err
	}
	if err := release.write(outputRoot, encrypt); err != nil {
		return err
	}
	return validateKubernetesSecretReleaseRootWithDecrypt(outputRoot, decrypt)
}

func resolveKubernetesReleaseOutputRoot(outputRoot string) (string, error) {
	outputRoot = strings.TrimSpace(outputRoot)
	if outputRoot == "" {
		return "", errors.New("kubernetes release output root is required")
	}
	absoluteRoot, err := filepath.Abs(outputRoot)
	if err != nil {
		return "", fmt.Errorf("resolve kubernetes release output root: %w", err)
	}
	entries, err := os.ReadDir(absoluteRoot)
	if err == nil && len(entries) != 0 {
		return "", fmt.Errorf("kubernetes release output root must be empty: %s", absoluteRoot)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read kubernetes release output root %s: %w", absoluteRoot, err)
	}
	return absoluteRoot, nil
}

func readNoebsKubernetesConfigMapData(repoRoot string) (map[string]string, error) {
	path := filepath.Join(repoRoot, "deploy", "kubernetes", "base", "configmap.yaml")
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes noebs configmap: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(payload))
	for {
		var object struct {
			Kind     string            `yaml:"kind"`
			Metadata kubernetesMeta    `yaml:"metadata"`
			Data     map[string]string `yaml:"data"`
		}
		if err := decoder.Decode(&object); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode Kubernetes noebs configmap: %w", err)
		}
		if object.Kind == "ConfigMap" && object.Metadata.Name == "noebs-config" {
			if len(object.Data) == 0 {
				return nil, errors.New("noebs-config ConfigMap has no data")
			}
			return object.Data, nil
		}
	}
	return nil, errors.New("noebs-config ConfigMap not found")
}

func readLegacyNoebsConfig(root, ageKeyPath string, decrypt deploymentDecryptFunc) (map[string]interface{}, error) {
	configMap, err := readYAMLMapFile(filepath.Join(root, "config.docker.yaml"))
	if err != nil {
		return nil, err
	}
	secretPath := filepath.Join(root, "secrets.yaml")
	if err := requireReadableFile("legacy noebs secrets", secretPath); err != nil {
		return nil, err
	}
	secretPayload, err := decrypt(secretPath, ageKeyPath)
	if err != nil {
		return nil, fmt.Errorf("decrypt legacy noebs secrets: %w", err)
	}
	secretMap, err := parseYAMLMap(secretPath, secretPayload)
	if err != nil {
		return nil, err
	}
	merged := mergeConfig(configMap, secretMap).(map[string]interface{})
	noebs := getMap(merged, "noebs")
	if noebs == nil {
		return nil, errors.New("legacy merged config missing noebs")
	}
	return noebs, nil
}

func readKubernetesReleaseInputs(path, ageKeyPath string, decrypt deploymentDecryptFunc) (kubernetesReleaseInputs, error) {
	path = strings.TrimSpace(path)
	if err := requireReadableFile("kubernetes release inputs", path); err != nil {
		return kubernetesReleaseInputs{}, err
	}
	payload, err := decrypt(path, ageKeyPath)
	if err != nil {
		return kubernetesReleaseInputs{}, fmt.Errorf("decrypt kubernetes release inputs: %w", err)
	}
	var inputs kubernetesReleaseInputs
	decoder := yaml.NewDecoder(bytes.NewReader(payload))
	decoder.KnownFields(true)
	if err := decoder.Decode(&inputs); err != nil {
		return kubernetesReleaseInputs{}, fmt.Errorf("parse kubernetes release inputs: %w", err)
	}
	return inputs, nil
}

func (r preparedKubernetesRelease) validate() error {
	if _, err := validateTenantID(r.inputs.Noebs.DefaultTenantID); err != nil {
		return fmt.Errorf("kubernetes release input default_tenant_id: %w", err)
	}
	for _, check := range []struct {
		label string
		value string
	}{
		{"noebs.admin_key", r.inputs.Noebs.AdminKey},
		{"noebs.admin_user", r.inputs.Noebs.AdminUser},
		{"noebs.admin_password", r.inputs.Noebs.AdminPassword},
		{"noebs.sms_key", r.inputs.Noebs.SMSKey},
		{"noebs.sms_sender", r.inputs.Noebs.SMSSender},
		{"noebs.sms_gateway", r.inputs.Noebs.SMSGateway},
		{"noebs.sms_message", r.inputs.Noebs.SMSMessage},
		{"noebs.google_redirect_url", r.inputs.Noebs.GoogleRedirectURL},
		{"noebs.card_vault_data_key", r.inputs.Noebs.CardVaultDataKey},
		{"noebs.temporal_postgres_password", r.inputs.Noebs.TemporalPostgresPassword},
		{"noebs.keycloak_postgres_password", r.inputs.Noebs.KeycloakPostgresPassword},
		{"noebs.keycloak_bootstrap_admin_username", r.inputs.Noebs.KeycloakBootstrapAdminUsername},
		{"noebs.keycloak_bootstrap_admin_password", r.inputs.Noebs.KeycloakBootstrapAdminPassword},
		{"noebs.ebs.consumer_endpoint", r.inputs.Noebs.EBS.ConsumerEndpoint},
		{"noebs.ebs.merchant_endpoint", r.inputs.Noebs.EBS.MerchantEndpoint},
		{"noebs.ebs.ipin_endpoint", r.inputs.Noebs.EBS.IPINEndpoint},
		{"noebs.ebs.consumer_app_id", r.inputs.Noebs.EBS.ConsumerAppID},
		{"noebs.ebs.merchant_app_id", r.inputs.Noebs.EBS.MerchantAppID},
		{"noebs.ebs.ipin_username", r.inputs.Noebs.EBS.IPINUsername},
		{"noebs.ebs.ipin_password", r.inputs.Noebs.EBS.IPINPassword},
		{"noebs.ebs.pub_key", r.inputs.Noebs.EBS.PublicKey},
		{"noebs.ebs.ipin_key", r.inputs.Noebs.EBS.IPINKey},
		{"noebs.ebs.pan", r.inputs.Noebs.EBS.PAN},
		{"noebs.ebs.pin", r.inputs.Noebs.EBS.PIN},
		{"noebs.ebs.ipin", r.inputs.Noebs.EBS.IPIN},
		{"noebs.ebs.exp_date", r.inputs.Noebs.EBS.Expiry},
	} {
		if strings.TrimSpace(check.value) == "" {
			return fmt.Errorf("missing kubernetes release input %s", check.label)
		}
		if strings.Contains(check.value, "REPLACE_WITH_") {
			return fmt.Errorf("kubernetes release input %s contains placeholder", check.label)
		}
	}
	if len(r.inputs.Noebs.PSP) == 0 {
		return errors.New("missing kubernetes release input noebs.psp")
	}
	if err := validatePSPSecretMap(map[string]interface{}{
		"psp":               pspInputsToMap(r.inputs.Noebs.PSP),
		"default_tenant_id": r.inputs.Noebs.DefaultTenantID,
	}, r.inputs.Noebs.DefaultTenantID); err != nil {
		return fmt.Errorf("kubernetes release input PSP secrets: %w", err)
	}
	for _, key := range []string{
		"db_url",
		"jwt_secret",
		"google_client_id",
		"google_client_secret",
	} {
		if _, err := r.requiredLegacyString(key); err != nil {
			return err
		}
	}
	if _, err := r.serviceDatabaseURL("identity-auth"); err != nil {
		return err
	}
	return nil
}

func (r preparedKubernetesRelease) write(outputRoot string, encrypt kubernetesSecretEncryptFunc) error {
	ageKey, err := readRequiredSecretText("SOPS age key", r.ageKeyPath)
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, ".sops/age-key.txt", ageKey); err != nil {
		return err
	}
	configPayload, err := configMapDataValue(r.configData, "config.yaml")
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "config.yaml", configPayload); err != nil {
		return err
	}
	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		configKey := serviceName + ".service.yaml"
		configPayload, err := configMapDataValue(r.configData, configKey)
		if err != nil {
			return err
		}
		if err := writeReleaseFile(outputRoot, filepath.Join("services", serviceName+".yaml"), configPayload); err != nil {
			return err
		}
	}
	postgresPassword, err := databaseURLPassword("legacy noebs.db_url", firstString(r.legacy, "db_url"))
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/postgres-password.txt", postgresPassword+"\n"); err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/temporal-postgres-password.txt", strings.TrimSpace(r.inputs.Noebs.TemporalPostgresPassword)+"\n"); err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/keycloak-postgres-password.txt", strings.TrimSpace(r.inputs.Noebs.KeycloakPostgresPassword)+"\n"); err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/keycloak.conf", r.keycloakConfig()); err != nil {
		return err
	}

	serviceSecrets, err := r.serviceSecrets()
	if err != nil {
		return err
	}
	for fileName, secretMap := range serviceSecrets {
		payload, err := yaml.Marshal(map[string]interface{}{"noebs": secretMap})
		if err != nil {
			return fmt.Errorf("marshal %s: %w", fileName, err)
		}
		encrypted, err := encrypt(fileName, payload, r.ageKeyPath)
		if err != nil {
			return err
		}
		if err := writeReleaseFile(outputRoot, filepath.Join("secrets", fileName), string(encrypted)); err != nil {
			return err
		}
	}
	return nil
}

func (r preparedKubernetesRelease) serviceSecrets() (map[string]map[string]interface{}, error) {
	tenantID := strings.TrimSpace(r.inputs.Noebs.DefaultTenantID)
	base := func() map[string]interface{} {
		return map[string]interface{}{"default_tenant_id": tenantID}
	}
	withDB := func(serviceName string) (map[string]interface{}, error) {
		dbURL, err := r.serviceDatabaseURL(serviceName)
		if err != nil {
			return nil, err
		}
		secret := base()
		secret["service_databases"] = map[string]interface{}{serviceName: dbURL}
		return secret, nil
	}

	apiGateway := base()
	jwtSecret, err := r.requiredLegacyString("jwt_secret")
	if err != nil {
		return nil, err
	}
	apiGateway["jwt_secret"] = jwtSecret
	apiGateway["admin_key"] = strings.TrimSpace(r.inputs.Noebs.AdminKey)
	apiGateway["admin_user"] = strings.TrimSpace(r.inputs.Noebs.AdminUser)
	apiGateway["admin_password"] = strings.TrimSpace(r.inputs.Noebs.AdminPassword)

	identityAuth, err := withDB("identity-auth")
	if err != nil {
		return nil, err
	}
	googleClientID, err := r.requiredLegacyString("google_client_id")
	if err != nil {
		return nil, err
	}
	googleClientSecret, err := r.requiredLegacyString("google_client_secret")
	if err != nil {
		return nil, err
	}
	identityAuth["jwt_secret"] = jwtSecret
	identityAuth["sms_key"] = strings.TrimSpace(r.inputs.Noebs.SMSKey)
	identityAuth["sms_sender"] = strings.TrimSpace(r.inputs.Noebs.SMSSender)
	identityAuth["sms_gateway"] = strings.TrimSpace(r.inputs.Noebs.SMSGateway)
	identityAuth["sms_message"] = strings.TrimSpace(r.inputs.Noebs.SMSMessage)
	identityAuth["google_client_id"] = googleClientID
	identityAuth["google_client_secret"] = googleClientSecret
	identityAuth["google_redirect_url"] = strings.TrimSpace(r.inputs.Noebs.GoogleRedirectURL)

	cardVault, err := withDB("card-vault")
	if err != nil {
		return nil, err
	}
	cardVault["data_key"] = strings.TrimSpace(r.inputs.Noebs.CardVaultDataKey)

	ebsAdapter, err := withDB("ebs-adapter")
	if err != nil {
		return nil, err
	}
	ebsAdapter["consumer_endpoint"] = strings.TrimSpace(r.inputs.Noebs.EBS.ConsumerEndpoint)
	ebsAdapter["merchant_endpoint"] = strings.TrimSpace(r.inputs.Noebs.EBS.MerchantEndpoint)
	ebsAdapter["ipin_endpoint"] = strings.TrimSpace(r.inputs.Noebs.EBS.IPINEndpoint)
	ebsAdapter["consumer_app_id"] = strings.TrimSpace(r.inputs.Noebs.EBS.ConsumerAppID)
	ebsAdapter["merchant_app_id"] = strings.TrimSpace(r.inputs.Noebs.EBS.MerchantAppID)
	ebsAdapter["ipin_username"] = strings.TrimSpace(r.inputs.Noebs.EBS.IPINUsername)
	ebsAdapter["ipin_password"] = strings.TrimSpace(r.inputs.Noebs.EBS.IPINPassword)
	ebsAdapter["pub_key"] = strings.TrimSpace(r.inputs.Noebs.EBS.PublicKey)
	ebsAdapter["ipin_key"] = strings.TrimSpace(r.inputs.Noebs.EBS.IPINKey)
	ebsAdapter["pan"] = strings.TrimSpace(r.inputs.Noebs.EBS.PAN)
	ebsAdapter["pin"] = strings.TrimSpace(r.inputs.Noebs.EBS.PIN)
	ebsAdapter["ipin"] = strings.TrimSpace(r.inputs.Noebs.EBS.IPIN)
	ebsAdapter["exp_date"] = strings.TrimSpace(r.inputs.Noebs.EBS.Expiry)

	pspWebhook, err := withDB("psp-webhook")
	if err != nil {
		return nil, err
	}
	pspWebhook["psp"] = pspInputsToMap(r.inputs.Noebs.PSP)

	walletWorker, err := withDB("wallet-ledger")
	if err != nil {
		return nil, err
	}
	walletWorker["psp"] = pspInputsToMap(r.inputs.Noebs.PSP)

	result := map[string]map[string]interface{}{
		"api-gateway.secrets.yaml":   apiGateway,
		"identity-auth.secrets.yaml": identityAuth,
		"card-vault.secrets.yaml":    cardVault,
		"ebs-adapter.secrets.yaml":   ebsAdapter,
		"psp-webhook.secrets.yaml":   pspWebhook,
		"wallet-api.secrets.yaml":    base(),
		"wallet-worker.secrets.yaml": walletWorker,
	}
	for _, serviceName := range []string{
		"admin-reporting",
		"notification-chat",
		"consumer-beneficiary",
		"wallet-ledger",
	} {
		secret, err := withDB(serviceName)
		if err != nil {
			return nil, err
		}
		result[serviceName+".secrets.yaml"] = secret
	}
	return result, nil
}

func (r preparedKubernetesRelease) serviceDatabaseURL(serviceName string) (string, error) {
	databaseName := strings.ReplaceAll(serviceName, "-", "_")
	source, err := r.requiredLegacyString("db_url")
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return "", fmt.Errorf("parse legacy noebs.db_url: %w", err)
	}
	if parsed.User == nil {
		return "", errors.New("legacy noebs.db_url missing user info")
	}
	password, ok := parsed.User.Password()
	if !ok || strings.TrimSpace(password) == "" {
		return "", errors.New("legacy noebs.db_url missing password")
	}
	result := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(parsed.User.Username(), password),
		Host:     "postgres:5432",
		Path:     "/" + databaseName,
		RawQuery: "sslmode=disable",
	}
	return result.String(), nil
}

func (r preparedKubernetesRelease) requiredLegacyString(key string) (string, error) {
	value := strings.TrimSpace(firstString(r.legacy, key))
	if value == "" {
		return "", fmt.Errorf("legacy noebs.%s is required to prepare Kubernetes release", key)
	}
	if strings.Contains(value, "REPLACE_WITH_") {
		return "", fmt.Errorf("legacy noebs.%s contains placeholder", key)
	}
	return value, nil
}

func (r preparedKubernetesRelease) keycloakConfig() string {
	return fmt.Sprintf(`http-enabled=true
http-port=8080
hostname-strict=false
proxy-headers=xforwarded
health-enabled=true
metrics-enabled=true

db=postgres
db-url=jdbc:postgresql://keycloak-postgres:5432/keycloak
db-username=keycloak
db-password=%s

bootstrap-admin-username=%s
bootstrap-admin-password=%s
`,
		strings.TrimSpace(r.inputs.Noebs.KeycloakPostgresPassword),
		strings.TrimSpace(r.inputs.Noebs.KeycloakBootstrapAdminUsername),
		strings.TrimSpace(r.inputs.Noebs.KeycloakBootstrapAdminPassword),
	)
}

func configMapDataValue(data map[string]string, key string) (string, error) {
	value := data[key]
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("noebs-config missing %s", key)
	}
	return value, nil
}

func writeReleaseFile(root, name, payload string) error {
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func pspInputsToMap(inputs map[string]map[string]pspSecret) map[string]interface{} {
	result := make(map[string]interface{}, len(inputs))
	for tenantID, providers := range inputs {
		providerMap := make(map[string]interface{}, len(providers))
		for providerCode, secret := range providers {
			providerMap[providerCode] = map[string]interface{}{
				"api_key":            strings.TrimSpace(secret.APIKey),
				"api_secret":         strings.TrimSpace(secret.APISecret),
				"webhook_secret":     strings.TrimSpace(secret.WebhookSecret),
				"webhook_public_key": strings.TrimSpace(secret.WebhookPublicKey),
			}
		}
		result[tenantID] = providerMap
	}
	return result
}

func encryptSopsYAML(label string, payload []byte, ageKeyPath string) ([]byte, error) {
	recipient, err := ageRecipientFromKeyFile(ageKeyPath)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "noebs-"+strings.ReplaceAll(label, string(filepath.Separator), "-")+"-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("create temporary %s plaintext: %w", label, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logrusLogger.WithError(err).Warn("remove temporary sops plaintext failed")
		}
	}()
	if _, err := tmp.Write(payload); err != nil {
		if closeErr := tmp.Close(); closeErr != nil {
			return nil, fmt.Errorf("close temporary %s plaintext after write failure: %w", label, closeErr)
		}
		return nil, fmt.Errorf("write temporary %s plaintext: %w", label, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close temporary %s plaintext: %w", label, err)
	}
	cmd := exec.Command("sops", "--config", "/dev/null", "--encrypt", "--age", recipient, "--input-type", "yaml", "--output-type", "yaml", tmpPath)
	cmd.Env = os.Environ()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	encrypted, err := cmd.Output()
	if err != nil {
		if text := strings.TrimSpace(stderr.String()); text != "" {
			return nil, fmt.Errorf("sops encrypt %s: %w: %s", label, err, text)
		}
		return nil, fmt.Errorf("sops encrypt %s: %w", label, err)
	}
	return encrypted, nil
}

func ageRecipientFromKeyFile(ageKeyPath string) (string, error) {
	payload, err := os.ReadFile(ageKeyPath)
	if err != nil {
		return "", fmt.Errorf("read SOPS age key: %w", err)
	}
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if recipient, ok := strings.CutPrefix(line, "# public key:"); ok {
			recipient = strings.TrimSpace(recipient)
			if recipient == "" {
				return "", errors.New("SOPS age key public recipient is empty")
			}
			return recipient, nil
		}
	}
	return "", errors.New("SOPS age key missing public recipient comment")
}
