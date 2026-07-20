package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adonese/noebs/internal/keycloakadmin"
	"gopkg.in/yaml.v3"
)

var kubernetesNamespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type kubernetesServiceSecretSource struct {
	serviceName string
	secretName  string
	fileName    string
}

type kubernetesSecretManifest struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Type       string            `yaml:"type,omitempty"`
	Metadata   kubernetesMeta    `yaml:"metadata"`
	StringData map[string]string `yaml:"stringData"`
}

type kubernetesMeta struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace"`
	Labels    map[string]string `yaml:"labels,omitempty"`
}

var kubernetesServiceSecretSources = []kubernetesServiceSecretSource{
	{serviceName: "api-gateway", secretName: "api-gateway-secrets", fileName: "api-gateway.secrets.yaml"},
	{serviceName: "identity-auth", secretName: "identity-auth-secrets", fileName: "identity-auth.secrets.yaml"},
	{serviceName: "card-vault", secretName: "card-vault-secrets", fileName: "card-vault.secrets.yaml"},
	{serviceName: "ebs-adapter", secretName: "ebs-adapter-secrets", fileName: "ebs-adapter.secrets.yaml"},
	{serviceName: "ebs-adapter-events", secretName: "ebs-adapter-events-secrets", fileName: "ebs-adapter-events.secrets.yaml"},
	{serviceName: "psp-webhook", secretName: "psp-webhook-secrets", fileName: "psp-webhook.secrets.yaml"},
	{serviceName: "admin-reporting", secretName: "admin-reporting-secrets", fileName: "admin-reporting.secrets.yaml"},
	{serviceName: "admin-reporting-projector", secretName: "admin-reporting-projector-secrets", fileName: "admin-reporting-projector.secrets.yaml"},
	{serviceName: "notification-chat", secretName: "notification-chat-secrets", fileName: "notification-chat.secrets.yaml"},
	{serviceName: "wallet-api", secretName: "wallet-api-secrets", fileName: "wallet-api.secrets.yaml"},
	{serviceName: "wallet-ledger", secretName: "wallet-ledger-secrets", fileName: "wallet-ledger.secrets.yaml"},
	{serviceName: "wallet-worker", secretName: "wallet-worker-secrets", fileName: "wallet-worker.secrets.yaml"},
	{serviceName: "workload-auth-migrate", secretName: "workload-auth-migrate-secrets", fileName: "workload-auth-migrate.secrets.yaml"},
	{serviceName: "workload-auth-cleanup", secretName: "workload-auth-cleanup-secrets", fileName: "workload-auth-cleanup.secrets.yaml"},
	{serviceName: "gateway-auth-migrate", secretName: "gateway-auth-migrate-secrets", fileName: "gateway-auth-migrate.secrets.yaml"},
	{serviceName: "gateway-auth-cleanup", secretName: "gateway-auth-cleanup-secrets", fileName: "gateway-auth-cleanup.secrets.yaml"},
	{serviceName: "identity-auth-migrate", secretName: "identity-auth-migrate-secrets", fileName: "identity-auth-migrate.secrets.yaml"},
	{serviceName: "card-vault-migrate", secretName: "card-vault-migrate-secrets", fileName: "card-vault-migrate.secrets.yaml"},
	{serviceName: "ebs-adapter-migrate", secretName: "ebs-adapter-migrate-secrets", fileName: "ebs-adapter-migrate.secrets.yaml"},
	{serviceName: "admin-reporting-migrate", secretName: "admin-reporting-migrate-secrets", fileName: "admin-reporting-migrate.secrets.yaml"},
	{serviceName: "notification-chat-migrate", secretName: "notification-chat-migrate-secrets", fileName: "notification-chat-migrate.secrets.yaml"},
	{serviceName: "wallet-ledger-migrate", secretName: "wallet-ledger-migrate-secrets", fileName: "wallet-ledger-migrate.secrets.yaml"},
}

var kubernetesSecretReleaseServiceNames = []string{
	"api-gateway",
	"identity-auth",
	"card-vault",
	"ebs-adapter",
	"ebs-adapter-events",
	"psp-webhook",
	"admin-reporting",
	"admin-reporting-projector",
	"notification-chat",
	"wallet-api",
	"wallet-ledger",
	"wallet-worker",
	"workload-auth-migrate",
	"workload-auth-cleanup",
	"gateway-auth-migrate",
	"gateway-auth-cleanup",
	"identity-auth-migrate",
	"card-vault-migrate",
	"ebs-adapter-migrate",
	"admin-reporting-migrate",
	"notification-chat-migrate",
	"wallet-ledger-migrate",
}

func renderKubernetesSecretsCommand() error {
	if len(os.Args) != 4 {
		return errors.New("usage: noebs render-kubernetes-secrets <kubernetes-release-root> <namespace>")
	}
	return renderKubernetesSecrets(os.Stdout, os.Args[2], os.Args[3], decryptSopsFile)
}

func renderEdgeInternalTransportCommand() error {
	if len(os.Args) != 4 {
		return errors.New("usage: noebs render-edge-internal-transport <kubernetes-release-root> <namespace>")
	}
	return renderEdgeInternalTransport(os.Stdout, os.Args[2], os.Args[3], decryptSopsFile)
}

func renderEdgeInternalTransport(w io.Writer, root, namespace string, decrypt deploymentDecryptFunc) error {
	root, err := resolveDeploymentRoot(root)
	if err != nil {
		return err
	}
	namespace = strings.TrimSpace(namespace)
	if err := validateKubernetesNamespace(namespace); err != nil {
		return err
	}
	if decrypt == nil {
		return errors.New("deployment decrypt function is required")
	}
	if err := validateKubernetesSecretReleaseRootWithDecrypt(root, decrypt); err != nil {
		return err
	}
	platform, err := readInternalTransportPlatformCredentials(
		root,
		filepath.Join(root, ".sops", "age-key.txt"),
		decrypt,
	)
	if err != nil {
		return err
	}
	return writeKubernetesSecretManifests(w, []kubernetesSecretManifest{
		newEdgeInternalTransportSecret(namespace, platform),
	})
}

func renderKubernetesSecrets(w io.Writer, root, namespace string, decrypt deploymentDecryptFunc) error {
	root, err := resolveDeploymentRoot(root)
	if err != nil {
		return err
	}
	namespace = strings.TrimSpace(namespace)
	if err := validateKubernetesNamespace(namespace); err != nil {
		return err
	}
	if decrypt == nil {
		return errors.New("deployment decrypt function is required")
	}
	if err := validateKubernetesSecretReleaseRootWithDecrypt(root, decrypt); err != nil {
		return err
	}

	ageKeyPath := filepath.Join(root, ".sops", "age-key.txt")
	temporalPostgresPassword, err := readRequiredSecretValue("Temporal Postgres password", filepath.Join(root, "platform", "temporal-postgres-password.txt"))
	if err != nil {
		return err
	}
	keycloakPostgresPassword, err := readRequiredSecretValue("Keycloak Postgres password", filepath.Join(root, "platform", "keycloak-postgres-password.txt"))
	if err != nil {
		return err
	}
	keycloakConfig, err := readRequiredSecretText("Keycloak config", filepath.Join(root, "platform", "keycloak.conf"))
	if err != nil {
		return err
	}
	keycloakReconcilerConfig, err := readRequiredSecretText(
		"Keycloak reconciler config",
		filepath.Join(root, "platform", "keycloak-reconciler-config.yaml"),
	)
	if err != nil {
		return err
	}
	steadyKeycloak, err := keycloakadmin.LoadConfig(strings.NewReader(keycloakReconcilerConfig))
	if err != nil {
		return err
	}
	ghcrDockerConfig, err := readRequiredSecretText("GHCR Docker config JSON", filepath.Join(root, "platform", "ghcr-dockerconfigjson"))
	if err != nil {
		return err
	}
	if err := validateDockerConfigJSONPayload("GHCR Docker config JSON", ghcrDockerConfig); err != nil {
		return err
	}
	workloadDatabase, err := readWorkloadAuthDatabaseCredentials(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	workloadDatabasePayload, err := decrypt(filepath.Join(root, "platform", "workload-auth-postgres-roles.secrets.yaml"), ageKeyPath)
	if err != nil {
		return err
	}
	gatewayDatabase, err := readGatewayAuthDatabaseCredentials(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	gatewayDatabasePayload, err := decrypt(filepath.Join(root, "platform", "gateway-auth-postgres-roles.secrets.yaml"), ageKeyPath)
	if err != nil {
		return err
	}
	serviceDatabase, err := readServicePostgresCredentials(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	serviceDatabasePayload, err := decrypt(filepath.Join(root, "platform", "service-postgres-roles.secrets.yaml"), ageKeyPath)
	if err != nil {
		return err
	}
	postgresProvisioningSQL, err := readRequiredSecretText(
		"Postgres provisioning SQL",
		filepath.Join(root, "platform", "postgres-provisioning.sql"),
	)
	if err != nil {
		return err
	}
	allDatabasePasswords := make(map[string]string, len(serviceDatabase)+6)
	for role, password := range serviceDatabase {
		allDatabasePasswords[role] = password
	}
	for role, password := range map[string]string{
		"workload_auth_migrate": workloadDatabase.migratePassword,
		"workload_auth_runtime": workloadDatabase.runtimePassword,
		"workload_auth_cleanup": workloadDatabase.cleanupPassword,
		"gateway_auth_migrate":  gatewayDatabase.migratePassword,
		"gateway_auth_runtime":  gatewayDatabase.runtimePassword,
		"gateway_auth_cleanup":  gatewayDatabase.cleanupPassword,
	} {
		allDatabasePasswords[role] = password
	}
	postgresRolePasswordFile, err := encodeServicePostgresPasswordFile(allDatabasePasswords)
	if err != nil {
		return err
	}
	internalTransportPlatform, err := readInternalTransportPlatformCredentials(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	internalTransportPayload, err := decrypt(filepath.Join(root, "platform", "internal-transport.secrets.yaml"), ageKeyPath)
	if err != nil {
		return err
	}
	releaseManifest, err := renderDecryptedKubernetesReleaseManifest(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	manifests := make([]kubernetesSecretManifest, 0, len(kubernetesServiceSecretSources)+12)
	for _, source := range kubernetesServiceSecretSources {
		payload, err := decrypt(filepath.Join(root, "secrets", source.fileName), ageKeyPath)
		if err != nil {
			return fmt.Errorf("decrypt %s secrets: %w", source.serviceName, err)
		}
		manifests = append(manifests, newOpaqueSecret(namespace, source.secretName, map[string]string{
			"secrets.yaml": string(payload),
		}))
	}
	manifests = append(manifests,
		newOpaqueSecret(namespace, "noebs-release-manifest", map[string]string{kubernetesReleaseManifestFile: releaseManifest}),
		newOpaqueSecret(namespace, "postgres-credentials", map[string]string{
			"ca.pem":  internalTransportPlatform.CACertificate,
			"tls.crt": internalTransportPlatform.PostgresCertificate,
			"tls.key": internalTransportPlatform.PostgresPrivateKey,
		}),
		newOpaqueSecret(namespace, "workload-auth-postgres-roles", map[string]string{
			"roles.yaml": string(workloadDatabasePayload),
		}),
		newOpaqueSecret(namespace, "gateway-auth-postgres-roles", map[string]string{
			"roles.yaml": string(gatewayDatabasePayload),
		}),
		newOpaqueSecret(namespace, "service-postgres-roles", map[string]string{
			"passwords.env": postgresRolePasswordFile,
			"bootstrap.sql": postgresProvisioningSQL,
			"roles.yaml":    string(serviceDatabasePayload),
		}),
		newOpaqueSecret(namespace, "internal-transport-platform", map[string]string{
			"credentials.yaml": string(internalTransportPayload),
		}),
		newOpaqueSecret(namespace, "temporal-postgres-credentials", map[string]string{
			"password": temporalPostgresPassword,
			"ca.pem":   internalTransportPlatform.CACertificate,
			"tls.crt":  internalTransportPlatform.TemporalPostgresCertificate,
			"tls.key":  internalTransportPlatform.TemporalPostgresPrivateKey,
		}),
		newOpaqueSecret(namespace, "temporal-server-credentials", map[string]string{
			"ca.pem":  internalTransportPlatform.CACertificate,
			"tls.crt": internalTransportPlatform.TemporalCertificate,
			"tls.key": internalTransportPlatform.TemporalPrivateKey,
		}),
		newOpaqueSecret(namespace, "temporal-namespace-bootstrap-credentials", map[string]string{
			"ca.pem":        internalTransportPlatform.CACertificate,
			"client-secret": steadyKeycloak.ClientCredentials[temporalBootstrapClientID].ClientSecret,
		}),
		newOpaqueSecret(namespace, "keycloak-postgres-credentials", map[string]string{
			"password": keycloakPostgresPassword,
			"tls.crt":  internalTransportPlatform.KeycloakPostgresCertificate,
			"tls.key":  internalTransportPlatform.KeycloakPostgresPrivateKey,
		}),
		newOpaqueSecret(namespace, "keycloak-secrets", map[string]string{
			"keycloak.conf": keycloakConfig,
			"db-ca.pem":     internalTransportPlatform.CACertificate,
			"tls.crt":       internalTransportPlatform.KeycloakCertificate,
			"tls.key":       internalTransportPlatform.KeycloakPrivateKey,
		}),
		newKeycloakTransportCASecret(namespace, internalTransportPlatform.CACertificate),
		newOpaqueSecret(namespace, "keycloak-reconciler-credentials", map[string]string{"config.yaml": keycloakReconcilerConfig}),
		kubernetesSecretManifest{
			APIVersion: "v1",
			Kind:       "Secret",
			Type:       "kubernetes.io/dockerconfigjson",
			Metadata:   kubernetesMeta{Name: "ghcr-credentials", Namespace: namespace},
			StringData: map[string]string{".dockerconfigjson": ghcrDockerConfig},
		},
	)
	return writeKubernetesSecretManifests(w, manifests)
}

func validateKubernetesNamespace(namespace string) error {
	if namespace == "" {
		return errors.New("kubernetes namespace is required")
	}
	if len(namespace) > 63 || !kubernetesNamespacePattern.MatchString(namespace) {
		return fmt.Errorf("invalid kubernetes namespace %q", namespace)
	}
	return nil
}

func validateKubernetesSecretReleaseRootWithDecrypt(root string, decrypt deploymentDecryptFunc) error {
	root, err := resolveDeploymentRoot(root)
	if err != nil {
		return err
	}
	if decrypt == nil {
		return errors.New("deployment decrypt function is required")
	}

	configPath := filepath.Join(root, "config.yaml")
	tenantCatalogPath := filepath.Join(root, "tenant-catalog.yaml")
	ageKeyPath := filepath.Join(root, ".sops", "age-key.txt")
	if err := requireReadableFile("config.yaml", configPath); err != nil {
		return err
	}
	if err := requireReadableFile("SOPS age key", ageKeyPath); err != nil {
		return err
	}
	if err := requireReadableFile("tenant catalog", tenantCatalogPath); err != nil {
		return err
	}
	if err := validateKubernetesPlatformInputs(root, ageKeyPath, decrypt); err != nil {
		return err
	}

	configMap, err := readYAMLMapFile(configPath)
	if err != nil {
		return err
	}
	if err := validateKubernetesReleaseServices(root, configMap, ageKeyPath, decrypt); err != nil {
		return err
	}
	return validateKubernetesReleaseManifest(root)
}

func newOpaqueSecret(namespace, name string, data map[string]string) kubernetesSecretManifest {
	return kubernetesSecretManifest{
		APIVersion: "v1",
		Kind:       "Secret",
		Type:       "Opaque",
		Metadata:   kubernetesMeta{Name: name, Namespace: namespace},
		StringData: data,
	}
}

func newKeycloakTransportCASecret(namespace, caCertificate string) kubernetesSecretManifest {
	secret := newOpaqueSecret(namespace, "keycloak-transport-ca", map[string]string{"ca.pem": caCertificate})
	secret.Metadata.Labels = map[string]string{
		"app.kubernetes.io/managed-by": "noebs-release-renderer",
		"app.kubernetes.io/part-of":    "noebs",
	}
	return secret
}

func newEdgeInternalTransportSecret(namespace string, platform internalTransportPlatformCredentials) kubernetesSecretManifest {
	secret := newOpaqueSecret(namespace, "edge-internal-transport", map[string]string{
		"ca.pem":  platform.CACertificate,
		"tls.crt": platform.EdgeCertificate,
		"tls.key": platform.EdgePrivateKey,
	})
	secret.Metadata.Labels = map[string]string{
		"app.kubernetes.io/managed-by": "noebs-release-renderer",
		"app.kubernetes.io/part-of":    "noebs",
	}
	return secret
}

func readRequiredSecretValue(label, path string) (string, error) {
	payload, err := readRequiredSecretText(label, path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(payload)
	if value == "" {
		return "", fmt.Errorf("%s is empty", label)
	}
	return value, nil
}

func readRequiredSecretText(label, path string) (string, error) {
	if err := requireReadableFile(label, path); err != nil {
		return "", err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	text := string(payload)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s is empty", label)
	}
	if strings.Contains(text, "REPLACE_WITH_") {
		return "", fmt.Errorf("%s contains placeholder", label)
	}
	return text, nil
}

func writeKubernetesSecretManifests(w io.Writer, manifests []kubernetesSecretManifest) error {
	for index, manifest := range manifests {
		if index > 0 {
			if _, err := fmt.Fprintln(w, "---"); err != nil {
				return fmt.Errorf("write kubernetes secret separator: %w", err)
			}
		}
		payload, err := yaml.Marshal(manifest)
		if err != nil {
			return fmt.Errorf("marshal kubernetes secret %s: %w", manifest.Metadata.Name, err)
		}
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write kubernetes secret %s: %w", manifest.Metadata.Name, err)
		}
	}
	return nil
}
