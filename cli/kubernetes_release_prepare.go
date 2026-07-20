package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adonese/noebs/internal/keycloakadmin"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"gopkg.in/yaml.v3"
)

type kubernetesSecretEncryptFunc func(label string, payload []byte, ageKeyPath string) ([]byte, error)

type kubernetesReleaseInputs struct {
	Noebs kubernetesReleaseNoebsInputs `yaml:"noebs"`
}

type kubernetesReleaseNoebsInputs struct {
	DefaultTenantID          string                                   `yaml:"default_tenant_id"`
	PostgresPassword         string                                   `yaml:"postgres_password"`
	GoogleClientID           string                                   `yaml:"google_client_id"`
	GoogleClientSecret       string                                   `yaml:"google_client_secret"`
	CardVaultDataKey         string                                   `yaml:"card_vault_data_key"`
	TemporalPostgresPassword string                                   `yaml:"temporal_postgres_password"`
	KeycloakPostgresPassword string                                   `yaml:"keycloak_postgres_password"`
	GHCRDockerConfigJSON     string                                   `yaml:"ghcr_dockerconfigjson"`
	EBS                      kubernetesReleaseEBSInputs               `yaml:"ebs"`
	PSP                      map[string]map[string]pspSecret          `yaml:"psp"`
	Keycloak                 kubernetesReleaseKeycloakInputs          `yaml:"keycloak"`
	GatewayAuth              kubernetesReleaseGatewayAuthInputs       `yaml:"gateway_auth"`
	WorkloadAuth             kubernetesReleaseWorkloadAuthInputs      `yaml:"workload_auth"`
	InternalTransport        kubernetesReleaseInternalTransportInputs `yaml:"internal_transport"`
}

type kubernetesReleaseKeycloakInputs struct {
	ReconcilerClientSecret string `yaml:"reconciler_client_secret"`
	BackofficeClientSecret string `yaml:"backoffice_client_secret"`
}

type kubernetesReleaseGatewayAuthInputs struct {
	Database        kubernetesReleaseGatewayAuthDatabaseInputs `yaml:"database"`
	EncryptionKeyID string                                     `yaml:"encryption_key_id"`
	EncryptionKeys  map[string]string                          `yaml:"encryption_keys"`
}

type kubernetesReleaseGatewayAuthDatabaseInputs struct {
	MigratePassword string `yaml:"migrate_password"`
	RuntimePassword string `yaml:"runtime_password"`
	CleanupPassword string `yaml:"cleanup_password"`
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
	CallbackID       string `yaml:"callback_id"`
	APIKey           string `yaml:"api_key"`
	APISecret        string `yaml:"api_secret"`
	WebhookSecret    string `yaml:"webhook_secret"`
	WebhookPublicKey string `yaml:"webhook_public_key"`
}

type preparedKubernetesRelease struct {
	configData        map[string]string
	inputs            kubernetesReleaseInputs
	ageKeyPath        string
	tenantCatalog     []byte
	keycloak          preparedKeycloakRelease
	gatewayAuth       preparedGatewayAuthRelease
	workloadAuth      preparedWorkloadAuthRelease
	internalTransport preparedInternalTransportRelease
}

type preparedKeycloakRelease struct {
	reconcilerClientSecret string
	backofficeClientSecret string
}

type preparedGatewayAuthRelease struct {
	migratePassword string
	runtimePassword string
	cleanupPassword string
	encryptionKeyID string
	encryptionKeys  map[string]string
}

func prepareKubernetesReleaseCommand() error {
	if len(os.Args) != 6 {
		return errors.New("usage: noebs prepare-kubernetes-release <repo-root> <inputs-yaml> <age-key-file> <output-root>")
	}
	return prepareKubernetesRelease(os.Args[2], os.Args[3], os.Args[4], os.Args[5], decryptSopsFile, encryptSopsYAML)
}

func prepareKubernetesRelease(repoRoot, inputsPath, ageKeyPath, outputRoot string, decrypt deploymentDecryptFunc, encrypt kubernetesSecretEncryptFunc) error {
	repoRoot, err := resolveDeploymentRoot(repoRoot)
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

	ageKeyPath = strings.TrimSpace(ageKeyPath)
	if ageKeyPath == "" {
		return errors.New("SOPS age key path is required")
	}
	if err := requireReadableFile("SOPS age key", ageKeyPath); err != nil {
		return err
	}
	configData, err := readNoebsKubernetesConfigMapData(repoRoot)
	if err != nil {
		return err
	}
	tenantCatalogPath := filepath.Join(repoRoot, "deploy", "kubernetes", "keycloak-authority", "tenant-catalog.yaml")
	tenantCatalogPayload, err := os.ReadFile(tenantCatalogPath)
	if err != nil {
		return fmt.Errorf("read Kubernetes tenant catalog: %w", err)
	}
	inputs, err := readKubernetesReleaseInputs(inputsPath, ageKeyPath, decrypt)
	if err != nil {
		return err
	}

	preparedKeycloak, err := prepareKeycloakRelease(inputs.Noebs.Keycloak)
	if err != nil {
		return err
	}
	preparedGatewayAuth, err := prepareGatewayAuthRelease(inputs.Noebs.GatewayAuth)
	if err != nil {
		return err
	}
	if err := requireExplicitWorkloadAuthInputs(inputs.Noebs.WorkloadAuth); err != nil {
		return err
	}
	preparedWorkloadAuth, err := prepareWorkloadAuthRelease(inputs.Noebs.WorkloadAuth, rand.Reader)
	if err != nil {
		return err
	}
	if strings.TrimSpace(inputs.Noebs.InternalTransport.CACertificate) == "" || strings.TrimSpace(inputs.Noebs.InternalTransport.CAPrivateKey) == "" {
		return errors.New("kubernetes release inputs require internal_transport.ca_certificate and internal_transport.ca_private_key")
	}
	preparedInternalTransport, err := prepareInternalTransportRelease(inputs.Noebs.InternalTransport, rand.Reader, time.Now().UTC())
	if err != nil {
		return err
	}
	release := preparedKubernetesRelease{
		configData:        configData,
		inputs:            inputs,
		ageKeyPath:        ageKeyPath,
		tenantCatalog:     tenantCatalogPayload,
		keycloak:          preparedKeycloak,
		gatewayAuth:       preparedGatewayAuth,
		workloadAuth:      preparedWorkloadAuth,
		internalTransport: preparedInternalTransport,
	}
	if err := release.validate(); err != nil {
		return err
	}
	if err := release.write(outputRoot, encrypt); err != nil {
		return err
	}
	if err := writeKubernetesReleaseManifest(outputRoot); err != nil {
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
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return kubernetesReleaseInputs{}, errors.New("kubernetes release inputs must contain one YAML document")
		}
		return kubernetesReleaseInputs{}, fmt.Errorf("parse kubernetes release inputs: %w", err)
	}
	return inputs, nil
}

func (r preparedKubernetesRelease) validate() error {
	tenantID := r.inputs.Noebs.DefaultTenantID
	if _, err := validateTenantID(tenantID); err != nil {
		return fmt.Errorf("kubernetes release input default_tenant_id: %w", err)
	}
	catalog, err := tenantcatalog.Load(bytes.NewReader(r.tenantCatalog))
	if err != nil {
		return fmt.Errorf("load Kubernetes tenant catalog: %w", err)
	}
	if _, err := catalog.Require(tenantID); err != nil {
		return fmt.Errorf("kubernetes release input default_tenant_id: %w", err)
	}
	for label, value := range map[string]string{
		"noebs.postgres_password":          r.inputs.Noebs.PostgresPassword,
		"noebs.google_client_id":           r.inputs.Noebs.GoogleClientID,
		"noebs.google_client_secret":       r.inputs.Noebs.GoogleClientSecret,
		"noebs.card_vault_data_key":        r.inputs.Noebs.CardVaultDataKey,
		"noebs.temporal_postgres_password": r.inputs.Noebs.TemporalPostgresPassword,
		"noebs.keycloak_postgres_password": r.inputs.Noebs.KeycloakPostgresPassword,
		"noebs.ebs.consumer_endpoint":      r.inputs.Noebs.EBS.ConsumerEndpoint,
		"noebs.ebs.merchant_endpoint":      r.inputs.Noebs.EBS.MerchantEndpoint,
		"noebs.ebs.ipin_endpoint":          r.inputs.Noebs.EBS.IPINEndpoint,
		"noebs.ebs.consumer_app_id":        r.inputs.Noebs.EBS.ConsumerAppID,
		"noebs.ebs.merchant_app_id":        r.inputs.Noebs.EBS.MerchantAppID,
		"noebs.ebs.ipin_username":          r.inputs.Noebs.EBS.IPINUsername,
		"noebs.ebs.ipin_password":          r.inputs.Noebs.EBS.IPINPassword,
		"noebs.ebs.pub_key":                r.inputs.Noebs.EBS.PublicKey,
		"noebs.ebs.ipin_key":               r.inputs.Noebs.EBS.IPINKey,
		"noebs.ebs.pan":                    r.inputs.Noebs.EBS.PAN,
		"noebs.ebs.pin":                    r.inputs.Noebs.EBS.PIN,
		"noebs.ebs.ipin":                   r.inputs.Noebs.EBS.IPIN,
		"noebs.ebs.exp_date":               r.inputs.Noebs.EBS.Expiry,
	} {
		if _, err := requiredKubernetesReleaseInput(label, value); err != nil {
			return err
		}
	}
	if _, err := r.ghcrDockerConfigJSON(); err != nil {
		return err
	}
	psp, err := r.pspSecrets()
	if err != nil {
		return err
	}
	if err := validatePSPSecretMap(map[string]interface{}{"psp": psp, "default_tenant_id": tenantID}, tenantID); err != nil {
		return fmt.Errorf("kubernetes release input PSP secrets: %w", err)
	}
	for pspTenantID := range psp {
		if _, err := catalog.Require(pspTenantID); err != nil {
			return fmt.Errorf("kubernetes release input PSP tenant %q: %w", pspTenantID, err)
		}
	}
	if _, err := r.pspWebhookRoutes(); err != nil {
		return err
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
	if err := writeReleaseFile(outputRoot, "tenant-catalog.yaml", string(r.tenantCatalog)); err != nil {
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
	postgresPassword := strings.TrimSpace(r.inputs.Noebs.PostgresPassword)
	if err := writeReleaseFile(outputRoot, "platform/postgres-password.txt", postgresPassword+"\n"); err != nil {
		return err
	}
	temporalPostgresPassword := strings.TrimSpace(r.inputs.Noebs.TemporalPostgresPassword)
	if err := writeReleaseFile(outputRoot, "platform/temporal-postgres-password.txt", temporalPostgresPassword+"\n"); err != nil {
		return err
	}
	keycloakPostgresPassword := strings.TrimSpace(r.inputs.Noebs.KeycloakPostgresPassword)
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
	keycloakReconcilerConfig, err := r.keycloakReconcilerConfig()
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/keycloak-reconciler-config.yaml", keycloakReconcilerConfig); err != nil {
		return err
	}
	ghcrDockerConfig, err := r.ghcrDockerConfigJSON()
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/ghcr-dockerconfigjson", ghcrDockerConfig+"\n"); err != nil {
		return err
	}
	workloadDatabasePayload, err := yaml.Marshal(r.workloadAuth.databaseCredentialSecret())
	if err != nil {
		return fmt.Errorf("marshal workload authentication database credentials: %w", err)
	}
	encryptedWorkloadDatabase, err := encrypt("workload-auth-postgres-roles.secrets.yaml", workloadDatabasePayload, r.ageKeyPath)
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/workload-auth-postgres-roles.secrets.yaml", string(encryptedWorkloadDatabase)); err != nil {
		return err
	}
	gatewayDatabasePayload, err := yaml.Marshal(r.gatewayAuth.databaseCredentialSecret())
	if err != nil {
		return fmt.Errorf("marshal gateway authentication database credentials: %w", err)
	}
	encryptedGatewayDatabase, err := encrypt("gateway-auth-postgres-roles.secrets.yaml", gatewayDatabasePayload, r.ageKeyPath)
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/gateway-auth-postgres-roles.secrets.yaml", string(encryptedGatewayDatabase)); err != nil {
		return err
	}
	internalTransportPayload, err := yaml.Marshal(r.internalTransport.platformSecret())
	if err != nil {
		return fmt.Errorf("marshal internal transport platform credentials: %w", err)
	}
	encryptedInternalTransport, err := encrypt("internal-transport.secrets.yaml", internalTransportPayload, r.ageKeyPath)
	if err != nil {
		return err
	}
	if err := writeReleaseFile(outputRoot, "platform/internal-transport.secrets.yaml", string(encryptedInternalTransport)); err != nil {
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
	withDB := func(ownerName string) (map[string]interface{}, error) {
		dbURL, err := r.serviceDatabaseURL(ownerName)
		if err != nil {
			return nil, err
		}
		secret := base()
		secret["service_databases"] = map[string]interface{}{ownerName: dbURL}
		secret["database_ca_certificate"] = r.internalTransport.caCertificate
		return secret, nil
	}
	result := make(map[string]map[string]interface{}, len(kubernetesSecretReleaseServiceNames))
	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		result[serviceName+".secrets.yaml"] = base()
	}
	setSecret := func(serviceName string, secret map[string]interface{}) {
		if workloadConfig := r.workloadAuth.configForRole(serviceRole(serviceName)); len(workloadConfig) != 0 {
			secret["workload_auth"] = workloadConfig
			if _, receiver := workloadConfig["nonce_db_url"]; receiver {
				secret["database_ca_certificate"] = r.internalTransport.caCertificate
			}
		}
		if transportConfig := r.internalTransport.configForRole(serviceRole(serviceName)); len(transportConfig) != 0 {
			secret["internal_transport"] = transportConfig
		}
		result[serviceName+".secrets.yaml"] = secret
	}

	apiGateway := base()
	apiGateway["backoffice_client_secret"] = r.keycloak.backofficeClientSecret
	apiGateway["backoffice_encryption_key_id"] = r.gatewayAuth.encryptionKeyID
	apiGateway["backoffice_encryption_keys"] = r.gatewayAuth.encryptionKeys
	apiGateway["psp_webhook_routes"], err = r.pspWebhookRoutes()
	if err != nil {
		return nil, err
	}
	apiGateway["database_ca_certificate"] = r.internalTransport.caCertificate
	apiGateway["keycloak_ca_certificate"] = r.internalTransport.caCertificate
	apiGateway["service_databases"] = map[string]interface{}{
		"api-gateway": gatewayAuthDatabaseURL("gateway_auth_runtime", r.gatewayAuth.runtimePassword),
	}
	setSecret("api-gateway", apiGateway)

	identityAuth, err := withDB("identity-auth")
	if err != nil {
		return nil, err
	}
	setSecret("identity-auth", identityAuth)

	cardVault, err := withDB("card-vault")
	if err != nil {
		return nil, err
	}
	cardVault["data_key"], err = requiredKubernetesReleaseInput("noebs.card_vault_data_key", r.inputs.Noebs.CardVaultDataKey)
	if err != nil {
		return nil, err
	}
	setSecret("card-vault", cardVault)
	cardVaultMigrate, err := withDB("card-vault")
	if err != nil {
		return nil, err
	}
	cardVaultMigrate["data_key"] = cardVault["data_key"]
	setSecret("card-vault-migrate", cardVaultMigrate)

	ebsAdapter, err := withDB("ebs-adapter")
	if err != nil {
		return nil, err
	}
	for key, value := range map[string]string{
		"consumer_endpoint": r.inputs.Noebs.EBS.ConsumerEndpoint,
		"merchant_endpoint": r.inputs.Noebs.EBS.MerchantEndpoint,
		"ipin_endpoint":     r.inputs.Noebs.EBS.IPINEndpoint,
		"consumer_app_id":   r.inputs.Noebs.EBS.ConsumerAppID,
		"merchant_app_id":   r.inputs.Noebs.EBS.MerchantAppID,
		"ipin_username":     r.inputs.Noebs.EBS.IPINUsername,
		"ipin_password":     r.inputs.Noebs.EBS.IPINPassword,
		"pub_key":           r.inputs.Noebs.EBS.PublicKey,
		"ipin_key":          r.inputs.Noebs.EBS.IPINKey,
		"pan":               r.inputs.Noebs.EBS.PAN,
		"pin":               r.inputs.Noebs.EBS.PIN,
		"ipin":              r.inputs.Noebs.EBS.IPIN,
		"exp_date":          r.inputs.Noebs.EBS.Expiry,
	} {
		ebsAdapter[key], err = requiredKubernetesReleaseInput("noebs.ebs."+key, value)
		if err != nil {
			return nil, err
		}
	}
	setSecret("ebs-adapter", ebsAdapter)

	pspWebhook, err := withDB("psp-webhook")
	if err != nil {
		return nil, err
	}
	psp, err := r.pspSecrets()
	if err != nil {
		return nil, err
	}
	pspWebhook["psp"] = psp
	setSecret("psp-webhook", pspWebhook)

	walletWorker, err := withDB("wallet-ledger")
	if err != nil {
		return nil, err
	}
	walletWorker["psp"] = psp
	setSecret("wallet-worker", walletWorker)
	setSecret("wallet-api", base())
	for _, serviceName := range []string{
		"admin-reporting",
		"notification-chat",
		"wallet-ledger",
	} {
		secret, err := withDB(serviceName)
		if err != nil {
			return nil, err
		}
		setSecret(serviceName, secret)
	}
	for roleName, ownerName := range map[string]string{
		"identity-auth-migrate":     "identity-auth",
		"ebs-adapter-migrate":       "ebs-adapter",
		"ebs-adapter-events":        "ebs-adapter",
		"psp-webhook-migrate":       "psp-webhook",
		"admin-reporting-migrate":   "admin-reporting",
		"admin-reporting-projector": "admin-reporting",
		"notification-chat-migrate": "notification-chat",
		"wallet-ledger-migrate":     "wallet-ledger",
	} {
		secret, err := withDB(ownerName)
		if err != nil {
			return nil, err
		}
		setSecret(roleName, secret)
	}
	setSecret("workload-auth-migrate", map[string]interface{}{
		"default_tenant_id":       tenantID,
		"database_ca_certificate": r.internalTransport.caCertificate,
		"service_databases": map[string]interface{}{
			"workload-auth-migrate": workloadAuthDatabaseURL("workload_auth_migrate", r.workloadAuth.database.migratePassword),
		},
	})
	setSecret("workload-auth-cleanup", map[string]interface{}{
		"default_tenant_id":       tenantID,
		"database_ca_certificate": r.internalTransport.caCertificate,
		"service_databases": map[string]interface{}{
			"workload-auth-migrate": workloadAuthDatabaseURL("workload_auth_cleanup", r.workloadAuth.database.cleanupPassword),
		},
	})
	setSecret("gateway-auth-migrate", map[string]interface{}{
		"default_tenant_id":       tenantID,
		"database_ca_certificate": r.internalTransport.caCertificate,
		"service_databases": map[string]interface{}{
			"api-gateway": gatewayAuthDatabaseURL("gateway_auth_migrate", r.gatewayAuth.migratePassword),
		},
	})
	setSecret("gateway-auth-cleanup", map[string]interface{}{
		"default_tenant_id":       tenantID,
		"database_ca_certificate": r.internalTransport.caCertificate,
		"service_databases": map[string]interface{}{
			"api-gateway": gatewayAuthDatabaseURL("gateway_auth_cleanup", r.gatewayAuth.cleanupPassword),
		},
	})
	return result, nil
}

func (r preparedKubernetesRelease) serviceDatabaseURL(serviceName string) (string, error) {
	databaseName := strings.ReplaceAll(serviceName, "-", "_")
	password, err := requiredKubernetesReleaseInput("noebs.postgres_password", r.inputs.Noebs.PostgresPassword)
	if err != nil {
		return "", err
	}
	result := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("noebs", password),
		Host:     "postgres:5432",
		Path:     "/" + databaseName,
		RawQuery: "sslmode=verify-full",
	}
	return result.String(), nil
}

func (r preparedKubernetesRelease) releaseDefaultTenantID() (string, error) {
	return requiredKubernetesReleaseInput("noebs.default_tenant_id", r.inputs.Noebs.DefaultTenantID)
}

func (r preparedKubernetesRelease) pspSecrets() (map[string]interface{}, error) {
	inputPSP := pspInputsToMap(r.inputs.Noebs.PSP)
	if len(inputPSP) == 0 {
		return nil, errors.New("missing kubernetes release input noebs.psp")
	}
	return inputPSP, nil
}

func (r preparedKubernetesRelease) pspWebhookRoutes() (map[string]interface{}, error) {
	routes := make(map[string]interface{})
	for tenantID, providers := range r.inputs.Noebs.PSP {
		for providerCode, provider := range providers {
			parsedProvider, err := tenantcatalog.ParseID(providerCode)
			if err != nil || string(parsedProvider) != providerCode {
				return nil, fmt.Errorf("kubernetes release input PSP provider %q is invalid", providerCode)
			}
			callbackID, err := requireCanonicalReleaseSecret("PSP webhook callback ID for "+tenantID+"/"+providerCode, provider.CallbackID)
			if err != nil {
				return nil, err
			}
			if _, duplicate := routes[callbackID]; duplicate {
				return nil, errors.New("PSP webhook callback IDs must be unique")
			}
			routes[callbackID] = map[string]interface{}{
				"tenant_id":     tenantID,
				"provider_code": providerCode,
			}
		}
	}
	if len(routes) == 0 {
		return nil, errors.New("missing kubernetes release input noebs.psp webhook routes")
	}
	return routes, nil
}

func (r preparedKubernetesRelease) ghcrDockerConfigJSON() (string, error) {
	payload, err := requiredKubernetesReleaseInput("noebs.ghcr_dockerconfigjson", r.inputs.Noebs.GHCRDockerConfigJSON)
	if err != nil {
		return "", err
	}
	if err := validateDockerConfigJSONPayload("kubernetes release input noebs.ghcr_dockerconfigjson", payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload), nil
}

func (r preparedKubernetesRelease) keycloakConfig() (string, error) {
	postgresPassword, err := requiredKubernetesReleaseInput("noebs.keycloak_postgres_password", r.inputs.Noebs.KeycloakPostgresPassword)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`http-enabled=false
http-relative-path=/auth
https-port=8443
https-certificate-file=/opt/keycloak/conf/tls.crt
https-certificate-key-file=/opt/keycloak/conf/tls.key
https-protocols=TLSv1.3
http-management-scheme=http
http-management-port=9000
http-management-relative-path=/
hostname=https://api.noebs.sd/auth
hostname-strict=true
hostname-backchannel-dynamic=false
proxy-headers=xforwarded
proxy-trusted-addresses=10.42.0.1/32
health-enabled=true
metrics-enabled=true

db=postgres
db-url=jdbc:postgresql://keycloak-postgres:5432/keycloak
db-username=keycloak
db-password=%s
db-tls-mode=verify-server
db-tls-trust-store-file=/opt/keycloak/conf/db-ca.pem
`,
		postgresPassword,
	), nil
}

func prepareKeycloakRelease(inputs kubernetesReleaseKeycloakInputs) (preparedKeycloakRelease, error) {
	reconcilerSecret, err := requireCanonicalReleaseSecret("Keycloak reconciler client secret", inputs.ReconcilerClientSecret)
	if err != nil {
		return preparedKeycloakRelease{}, err
	}
	backofficeSecret, err := requireCanonicalReleaseSecret("Keycloak back-office client secret", inputs.BackofficeClientSecret)
	if err != nil {
		return preparedKeycloakRelease{}, err
	}
	if reconcilerSecret == backofficeSecret {
		return preparedKeycloakRelease{}, errors.New("keycloak client secrets must be distinct")
	}
	return preparedKeycloakRelease{
		reconcilerClientSecret: reconcilerSecret,
		backofficeClientSecret: backofficeSecret,
	}, nil
}

func prepareGatewayAuthRelease(inputs kubernetesReleaseGatewayAuthInputs) (preparedGatewayAuthRelease, error) {
	migratePassword, err := requireCanonicalReleaseSecret("gateway authentication migration database password", inputs.Database.MigratePassword)
	if err != nil {
		return preparedGatewayAuthRelease{}, err
	}
	runtimePassword, err := requireCanonicalReleaseSecret("gateway authentication runtime database password", inputs.Database.RuntimePassword)
	if err != nil {
		return preparedGatewayAuthRelease{}, err
	}
	cleanupPassword, err := requireCanonicalReleaseSecret("gateway authentication cleanup database password", inputs.Database.CleanupPassword)
	if err != nil {
		return preparedGatewayAuthRelease{}, err
	}
	if migratePassword == runtimePassword || migratePassword == cleanupPassword || runtimePassword == cleanupPassword {
		return preparedGatewayAuthRelease{}, errors.New("gateway authentication database passwords must be distinct")
	}
	keyID := strings.TrimSpace(inputs.EncryptionKeyID)
	if keyID == "" || keyID != inputs.EncryptionKeyID || strings.Contains(keyID, "REPLACE_WITH_") {
		return preparedGatewayAuthRelease{}, errors.New("gateway authentication encryption_key_id is invalid")
	}
	if len(inputs.EncryptionKeys) == 0 {
		return preparedGatewayAuthRelease{}, errors.New("gateway authentication encryption_keys is required")
	}
	encryptionKeys := make(map[string]string, len(inputs.EncryptionKeys))
	for id, encoded := range inputs.EncryptionKeys {
		if id == "" || id != strings.TrimSpace(id) {
			return preparedGatewayAuthRelease{}, errors.New("gateway authentication encryption key id is invalid")
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(decoded) != 32 || base64.StdEncoding.EncodeToString(decoded) != encoded {
			return preparedGatewayAuthRelease{}, fmt.Errorf("gateway authentication encryption key %q is invalid", id)
		}
		encryptionKeys[id] = encoded
	}
	if _, ok := encryptionKeys[keyID]; !ok {
		return preparedGatewayAuthRelease{}, errors.New("gateway authentication active encryption key is missing from encryption_keys")
	}
	return preparedGatewayAuthRelease{
		migratePassword: migratePassword,
		runtimePassword: runtimePassword,
		cleanupPassword: cleanupPassword,
		encryptionKeyID: keyID,
		encryptionKeys:  encryptionKeys,
	}, nil
}

func (r preparedGatewayAuthRelease) databaseCredentialSecret() map[string]interface{} {
	return map[string]interface{}{
		"migrate_password": r.migratePassword,
		"runtime_password": r.runtimePassword,
		"cleanup_password": r.cleanupPassword,
	}
}

func gatewayAuthDatabaseURL(username, password string) string {
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(username, password),
		Host:     "postgres:5432",
		Path:     "/gateway_auth",
		RawQuery: "sslmode=verify-full",
	}).String()
}

func requireCanonicalReleaseSecret(label, value string) (string, error) {
	if value != strings.TrimSpace(value) {
		return "", fmt.Errorf("%s must be canonical without surrounding whitespace", label)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return "", fmt.Errorf("%s must be an explicit canonical base64url encoding of 32 bytes", label)
	}
	return value, nil
}

func (r preparedKubernetesRelease) keycloakReconcilerConfig() (string, error) {
	googleClientID, err := requiredKubernetesReleaseInput("noebs.google_client_id", r.inputs.Noebs.GoogleClientID)
	if err != nil {
		return "", err
	}
	googleClientSecret, err := requiredKubernetesReleaseInput("noebs.google_client_secret", r.inputs.Noebs.GoogleClientSecret)
	if err != nil {
		return "", err
	}
	config := keycloakadmin.Config{
		BaseURL:      "https://keycloak.noebs.svc.cluster.local:8443/auth",
		AdminRealm:   "noebs",
		ClientID:     "noebs-keycloak-reconciler",
		ClientSecret: r.keycloak.reconcilerClientSecret,
		ClientCredentials: map[string]keycloakadmin.ClientCredential{
			"noebs-keycloak-reconciler": {ClientSecret: r.keycloak.reconcilerClientSecret},
			"noebs-backoffice":          {ClientSecret: r.keycloak.backofficeClientSecret},
		},
		IdentityProviders: map[string]keycloakadmin.IdentityProviderCredential{
			"google": {ClientID: googleClientID, ClientSecret: googleClientSecret},
		},
	}
	if err := config.Validate(); err != nil {
		return "", err
	}
	payload, err := yaml.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal Keycloak reconciler config: %w", err)
	}
	return string(payload), nil
}

func configMapDataValue(data map[string]string, key string) (string, error) {
	value := data[key]
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("noebs-config missing %s", key)
	}
	return value, nil
}

func requiredKubernetesReleaseInput(label, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("missing kubernetes release input %s", label)
	}
	if value != strings.TrimSpace(value) || strings.Contains(value, "REPLACE_WITH_") || strings.ContainsAny(value, "\r\x00") {
		return "", fmt.Errorf("kubernetes release input %s is invalid", label)
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
	sopsPath, err := sopsExecutable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(sopsPath, "--config", "/dev/null", "--encrypt", "--age", recipient, "--input-type", "yaml", "--output-type", "yaml", tmpPath)
	cmd.Env = []string{}
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
