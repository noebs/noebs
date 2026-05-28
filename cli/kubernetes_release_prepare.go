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
	GoogleClientID                 string                          `yaml:"google_client_id"`
	GoogleClientSecret             string                          `yaml:"google_client_secret"`
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

type cutoverStringField struct {
	label      string
	legacyKeys []string
	input      string
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
	tenantID, err := r.releaseDefaultTenantID()
	if err != nil {
		return err
	}
	if _, err := validateTenantID(tenantID); err != nil {
		return fmt.Errorf("kubernetes release input default_tenant_id: %w", err)
	}
	for _, field := range r.cutoverStringFields() {
		if _, err := r.cutoverField(field); err != nil {
			return err
		}
	}
	psp, err := r.pspSecrets()
	if err != nil {
		return err
	}
	if err := validatePSPSecretMap(map[string]interface{}{"psp": psp, "default_tenant_id": tenantID}, tenantID); err != nil {
		return fmt.Errorf("kubernetes release input PSP secrets: %w", err)
	}
	for _, key := range []string{
		"db_url",
		"jwt_secret",
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
	temporalPostgresPassword, err := r.cutoverString("noebs.temporal_postgres_password", []string{"temporal_postgres_password"}, r.inputs.Noebs.TemporalPostgresPassword)
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/temporal-postgres-password.txt", temporalPostgresPassword+"\n"); err != nil {
		return err
	}
	keycloakPostgresPassword, err := r.cutoverString("noebs.keycloak_postgres_password", []string{"keycloak_postgres_password"}, r.inputs.Noebs.KeycloakPostgresPassword)
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/keycloak-postgres-password.txt", keycloakPostgresPassword+"\n"); err != nil {
		return err
	}
	keycloakConfig, err := r.keycloakConfig()
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/keycloak.conf", keycloakConfig); err != nil {
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
	tenantID, err := r.releaseDefaultTenantID()
	if err != nil {
		return nil, err
	}
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
	apiGateway["admin_key"], err = r.cutoverString("noebs.admin_key", []string{"admin_key"}, r.inputs.Noebs.AdminKey)
	if err != nil {
		return nil, err
	}
	apiGateway["admin_user"], err = r.cutoverString("noebs.admin_user", []string{"admin_user"}, r.inputs.Noebs.AdminUser)
	if err != nil {
		return nil, err
	}
	apiGateway["admin_password"], err = r.cutoverString("noebs.admin_password", []string{"admin_password"}, r.inputs.Noebs.AdminPassword)
	if err != nil {
		return nil, err
	}

	identityAuth, err := withDB("identity-auth")
	if err != nil {
		return nil, err
	}
	googleClientID, err := r.cutoverString("noebs.google_client_id", []string{"google_client_id"}, r.inputs.Noebs.GoogleClientID)
	if err != nil {
		return nil, err
	}
	googleClientSecret, err := r.cutoverString("noebs.google_client_secret", []string{"google_client_secret"}, r.inputs.Noebs.GoogleClientSecret)
	if err != nil {
		return nil, err
	}
	identityAuth["jwt_secret"] = jwtSecret
	identityAuth["sms_key"], err = r.cutoverString("noebs.sms_key", []string{"sms_key"}, r.inputs.Noebs.SMSKey)
	if err != nil {
		return nil, err
	}
	identityAuth["sms_sender"], err = r.cutoverString("noebs.sms_sender", []string{"sms_sender"}, r.inputs.Noebs.SMSSender)
	if err != nil {
		return nil, err
	}
	identityAuth["sms_gateway"], err = r.cutoverString("noebs.sms_gateway", []string{"sms_gateway"}, r.inputs.Noebs.SMSGateway)
	if err != nil {
		return nil, err
	}
	identityAuth["sms_message"], err = r.cutoverString("noebs.sms_message", []string{"sms_message"}, r.inputs.Noebs.SMSMessage)
	if err != nil {
		return nil, err
	}
	identityAuth["google_client_id"] = googleClientID
	identityAuth["google_client_secret"] = googleClientSecret
	identityAuth["google_redirect_url"], err = r.cutoverString("noebs.google_redirect_url", []string{"google_redirect_url"}, r.inputs.Noebs.GoogleRedirectURL)
	if err != nil {
		return nil, err
	}

	cardVault, err := withDB("card-vault")
	if err != nil {
		return nil, err
	}
	cardVault["data_key"], err = r.cutoverString("noebs.card_vault_data_key", []string{"data_key", "card_vault_data_key"}, r.inputs.Noebs.CardVaultDataKey)
	if err != nil {
		return nil, err
	}

	ebsAdapter, err := withDB("ebs-adapter")
	if err != nil {
		return nil, err
	}
	for _, field := range r.ebsCutoverStringFields() {
		key := strings.TrimPrefix(field.label, "noebs.ebs.")
		ebsAdapter[key], err = r.cutoverField(field)
		if err != nil {
			return nil, err
		}
	}

	pspWebhook, err := withDB("psp-webhook")
	if err != nil {
		return nil, err
	}
	psp, err := r.pspSecrets()
	if err != nil {
		return nil, err
	}
	pspWebhook["psp"] = psp

	walletWorker, err := withDB("wallet-ledger")
	if err != nil {
		return nil, err
	}
	walletWorker["psp"] = psp

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

func (r preparedKubernetesRelease) releaseDefaultTenantID() (string, error) {
	return r.cutoverString("noebs.default_tenant_id", []string{"default_tenant_id"}, r.inputs.Noebs.DefaultTenantID)
}

func (r preparedKubernetesRelease) cutoverStringFields() []cutoverStringField {
	fields := []cutoverStringField{
		{label: "noebs.admin_key", legacyKeys: []string{"admin_key"}, input: r.inputs.Noebs.AdminKey},
		{label: "noebs.admin_user", legacyKeys: []string{"admin_user"}, input: r.inputs.Noebs.AdminUser},
		{label: "noebs.admin_password", legacyKeys: []string{"admin_password"}, input: r.inputs.Noebs.AdminPassword},
		{label: "noebs.sms_key", legacyKeys: []string{"sms_key"}, input: r.inputs.Noebs.SMSKey},
		{label: "noebs.sms_sender", legacyKeys: []string{"sms_sender"}, input: r.inputs.Noebs.SMSSender},
		{label: "noebs.sms_gateway", legacyKeys: []string{"sms_gateway"}, input: r.inputs.Noebs.SMSGateway},
		{label: "noebs.sms_message", legacyKeys: []string{"sms_message"}, input: r.inputs.Noebs.SMSMessage},
		{label: "noebs.google_client_id", legacyKeys: []string{"google_client_id"}, input: r.inputs.Noebs.GoogleClientID},
		{label: "noebs.google_client_secret", legacyKeys: []string{"google_client_secret"}, input: r.inputs.Noebs.GoogleClientSecret},
		{label: "noebs.google_redirect_url", legacyKeys: []string{"google_redirect_url"}, input: r.inputs.Noebs.GoogleRedirectURL},
		{label: "noebs.card_vault_data_key", legacyKeys: []string{"data_key", "card_vault_data_key"}, input: r.inputs.Noebs.CardVaultDataKey},
		{label: "noebs.temporal_postgres_password", legacyKeys: []string{"temporal_postgres_password"}, input: r.inputs.Noebs.TemporalPostgresPassword},
		{label: "noebs.keycloak_postgres_password", legacyKeys: []string{"keycloak_postgres_password"}, input: r.inputs.Noebs.KeycloakPostgresPassword},
		{label: "noebs.keycloak_bootstrap_admin_username", legacyKeys: []string{"keycloak_bootstrap_admin_username"}, input: r.inputs.Noebs.KeycloakBootstrapAdminUsername},
		{label: "noebs.keycloak_bootstrap_admin_password", legacyKeys: []string{"keycloak_bootstrap_admin_password"}, input: r.inputs.Noebs.KeycloakBootstrapAdminPassword},
	}
	return append(fields, r.ebsCutoverStringFields()...)
}

func (r preparedKubernetesRelease) ebsCutoverStringFields() []cutoverStringField {
	return []cutoverStringField{
		{label: "noebs.ebs.consumer_endpoint", legacyKeys: []string{"consumer_endpoint"}, input: r.inputs.Noebs.EBS.ConsumerEndpoint},
		{label: "noebs.ebs.merchant_endpoint", legacyKeys: []string{"merchant_endpoint"}, input: r.inputs.Noebs.EBS.MerchantEndpoint},
		{label: "noebs.ebs.ipin_endpoint", legacyKeys: []string{"ipin_endpoint"}, input: r.inputs.Noebs.EBS.IPINEndpoint},
		{label: "noebs.ebs.consumer_app_id", legacyKeys: []string{"consumer_app_id"}, input: r.inputs.Noebs.EBS.ConsumerAppID},
		{label: "noebs.ebs.merchant_app_id", legacyKeys: []string{"merchant_app_id"}, input: r.inputs.Noebs.EBS.MerchantAppID},
		{label: "noebs.ebs.ipin_username", legacyKeys: []string{"ipin_username"}, input: r.inputs.Noebs.EBS.IPINUsername},
		{label: "noebs.ebs.ipin_password", legacyKeys: []string{"ipin_password"}, input: r.inputs.Noebs.EBS.IPINPassword},
		{label: "noebs.ebs.pub_key", legacyKeys: []string{"pub_key"}, input: r.inputs.Noebs.EBS.PublicKey},
		{label: "noebs.ebs.ipin_key", legacyKeys: []string{"ipin_key"}, input: r.inputs.Noebs.EBS.IPINKey},
		{label: "noebs.ebs.pan", legacyKeys: []string{"pan"}, input: r.inputs.Noebs.EBS.PAN},
		{label: "noebs.ebs.pin", legacyKeys: []string{"pin"}, input: r.inputs.Noebs.EBS.PIN},
		{label: "noebs.ebs.ipin", legacyKeys: []string{"ipin"}, input: r.inputs.Noebs.EBS.IPIN},
		{label: "noebs.ebs.exp_date", legacyKeys: []string{"exp_date"}, input: r.inputs.Noebs.EBS.Expiry},
	}
}

func (r preparedKubernetesRelease) cutoverField(field cutoverStringField) (string, error) {
	return r.cutoverString(field.label, field.legacyKeys, field.input)
}

func (r preparedKubernetesRelease) cutoverString(label string, legacyKeys []string, input string) (string, error) {
	legacyValue, legacyKey := r.firstLegacyString(legacyKeys...)
	input = strings.TrimSpace(input)
	switch {
	case legacyValue != "" && input != "":
		return "", fmt.Errorf("kubernetes release input %s duplicates current secret noebs.%s", label, legacyKey)
	case legacyValue != "":
		if strings.Contains(legacyValue, "REPLACE_WITH_") {
			return "", fmt.Errorf("current secret noebs.%s contains placeholder", legacyKey)
		}
		return legacyValue, nil
	case input != "":
		if strings.Contains(input, "REPLACE_WITH_") {
			return "", fmt.Errorf("kubernetes release input %s contains placeholder", label)
		}
		return input, nil
	default:
		return "", fmt.Errorf("missing kubernetes release input %s", label)
	}
}

func (r preparedKubernetesRelease) firstLegacyString(keys ...string) (string, string) {
	for _, key := range keys {
		value := strings.TrimSpace(firstString(r.legacy, key))
		if value != "" {
			return value, key
		}
	}
	return "", ""
}

func (r preparedKubernetesRelease) pspSecrets() (map[string]interface{}, error) {
	legacyPSP := getMap(r.legacy, "psp")
	inputPSP := pspInputsToMap(r.inputs.Noebs.PSP)
	hasLegacy := len(legacyPSP) != 0
	hasInput := len(inputPSP) != 0
	switch {
	case hasLegacy && hasInput:
		return nil, errors.New("kubernetes release input noebs.psp duplicates current secret noebs.psp")
	case hasLegacy:
		return legacyPSP, nil
	case hasInput:
		return inputPSP, nil
	default:
		return nil, errors.New("missing kubernetes release input noebs.psp")
	}
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

func (r preparedKubernetesRelease) keycloakConfig() (string, error) {
	postgresPassword, err := r.cutoverString("noebs.keycloak_postgres_password", []string{"keycloak_postgres_password"}, r.inputs.Noebs.KeycloakPostgresPassword)
	if err != nil {
		return "", err
	}
	adminUsername, err := r.cutoverString("noebs.keycloak_bootstrap_admin_username", []string{"keycloak_bootstrap_admin_username"}, r.inputs.Noebs.KeycloakBootstrapAdminUsername)
	if err != nil {
		return "", err
	}
	adminPassword, err := r.cutoverString("noebs.keycloak_bootstrap_admin_password", []string{"keycloak_bootstrap_admin_password"}, r.inputs.Noebs.KeycloakBootstrapAdminPassword)
	if err != nil {
		return "", err
	}
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
		postgresPassword,
		adminUsername,
		adminPassword,
	), nil
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
