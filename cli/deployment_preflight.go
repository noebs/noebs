package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/transportauth"
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
	return validateDeploymentRootWithDecrypt(root, readPlainDeploymentYAML)
}

func validateKubernetesDeploymentRoot(root string) error {
	return validateKubernetesDeploymentRootWithDecrypt(root, readPlainDeploymentYAML)
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
	if err := requireReadableFile("config.docker.yaml", configPath); err != nil {
		return err
	}

	configMap, err := readYAMLMapFile(configPath)
	if err != nil {
		return err
	}
	if err := validatePlainSecretFile("Temporal Postgres password", filepath.Join(root, "deploy", "docker", "temporal", "postgres-password.txt")); err != nil {
		return err
	}
	if err := validateDockerTemporalTLS(root); err != nil {
		return err
	}
	if err := validateDockerTemporalPostgresTLS(root); err != nil {
		return err
	}
	bootstrapSecret, err := readRequiredSecretValue("Temporal namespace bootstrap client secret", filepath.Join(root, "deploy", "docker", "temporal", "namespace-bootstrap-client-secret.txt"))
	if err != nil {
		return err
	}
	if _, err := requireCanonicalReleaseSecret("Temporal namespace bootstrap client secret", bootstrapSecret); err != nil {
		return err
	}
	if err := validatePlainSecretFile("Keycloak Postgres password", filepath.Join(root, "deploy", "docker", "keycloak", "postgres-password.txt")); err != nil {
		return err
	}
	if err := validateDockerKeycloakPostgresTLS(root); err != nil {
		return err
	}
	if err := validateDockerKeycloakTLS(root); err != nil {
		return err
	}
	databaseCA, err := validateDockerPostgresTLS(root)
	if err != nil {
		return err
	}
	if err := validateKeycloakConfig(filepath.Join(root, "deploy", "docker", "keycloak", "keycloak.conf"), false); err != nil {
		return err
	}
	if err := validatePostgresProvisioningSQLFile(filepath.Join(root, "deploy", "docker", "postgres", "001-service-databases.sql")); err != nil {
		return err
	}
	if err := validateExactDockerReleaseFiles(root); err != nil {
		return err
	}
	if err := validateDockerDatabaseRoleCredentials(root, configMap, "", databaseCA, decrypt); err != nil {
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
		if err := validateDeploymentService(root, configMap, serviceFile, "", decrypt); err != nil {
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
	tenantCatalogPath := filepath.Join(root, "tenant-catalog.yaml")
	if err := requireReadableFile("config.yaml", configPath); err != nil {
		return err
	}
	if err := requireReadableFile("tenant catalog", tenantCatalogPath); err != nil {
		return err
	}
	if err := validateKubernetesPlatformInputs(root, "", decrypt); err != nil {
		return err
	}

	configMap, err := readYAMLMapFile(configPath)
	if err != nil {
		return err
	}
	if err := validateRenderedKubernetesReleaseServices(root, configMap, decrypt); err != nil {
		return err
	}
	return validateKubernetesReleaseManifest(root)
}

func readPlainDeploymentYAML(path, _ string) ([]byte, error) {
	return readPlaintextSecrets(path)
}

func validateKubernetesPlatformInputs(root, ageKeyPath string, decrypt deploymentDecryptFunc) error {
	for _, requiredFile := range []struct {
		label string
		path  string
	}{
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
	if err := validateKeycloakConfig(filepath.Join(root, "platform", "keycloak.conf"), true); err != nil {
		return err
	}
	if err := validatePostgresProvisioningSQLFile(filepath.Join(root, "platform", "postgres-provisioning.sql")); err != nil {
		return err
	}
	if err := validateSteadyKeycloakReconcilerConfig(filepath.Join(root, "platform", "keycloak-reconciler-config.yaml")); err != nil {
		return err
	}
	workload, err := readWorkloadAuthDatabaseCredentials(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	gateway, err := readGatewayAuthDatabaseCredentials(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	service, err := readServicePostgresCredentials(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	if err := validateAllPostgresRolePasswords(service, workload, gateway); err != nil {
		return err
	}
	if _, err := readInternalTransportPlatformCredentials(root, ageKeyPath, decrypt); err != nil {
		return err
	}
	return nil
}

type internalTransportPlatformCredentials struct {
	CACertificate               string `yaml:"ca_certificate"`
	PostgresCertificate         string `yaml:"postgres_certificate"`
	PostgresPrivateKey          string `yaml:"postgres_private_key"`
	KeycloakPostgresCertificate string `yaml:"keycloak_postgres_certificate"`
	KeycloakPostgresPrivateKey  string `yaml:"keycloak_postgres_private_key"`
	KeycloakCertificate         string `yaml:"keycloak_certificate"`
	KeycloakPrivateKey          string `yaml:"keycloak_private_key"`
	TemporalPostgresCertificate string `yaml:"temporal_postgres_certificate"`
	TemporalPostgresPrivateKey  string `yaml:"temporal_postgres_private_key"`
	TemporalCertificate         string `yaml:"temporal_certificate"`
	TemporalPrivateKey          string `yaml:"temporal_private_key"`
	EdgeCertificate             string `yaml:"edge_certificate"`
	EdgePrivateKey              string `yaml:"edge_private_key"`
}

func readInternalTransportPlatformCredentials(root, ageKeyPath string, decrypt deploymentDecryptFunc) (internalTransportPlatformCredentials, error) {
	path := filepath.Join(root, "platform", "internal-transport.secrets.yaml")
	if err := requireReadableFile("internal transport platform credentials", path); err != nil {
		return internalTransportPlatformCredentials{}, err
	}
	payload, err := decrypt(path, ageKeyPath)
	if err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("decrypt internal transport platform credentials: %w", err)
	}
	var values internalTransportPlatformCredentials
	decoder := yaml.NewDecoder(strings.NewReader(string(payload)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&values); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("parse internal transport platform credentials: %w", err)
	}
	postgres := transportauth.Config{
		CACertificate: strings.TrimSpace(values.CACertificate),
		Certificate:   values.PostgresCertificate,
		PrivateKey:    values.PostgresPrivateKey,
	}
	if err := postgres.ValidateIdentity("postgres"); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate Postgres transport identity: %w", err)
	}
	if err := validateServerCertificateIdentity(values.PostgresCertificate, "postgres", "postgres.noebs.svc", "postgres.noebs.svc.cluster.local"); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate Postgres transport identity: %w", err)
	}
	keycloakPostgres := transportauth.Config{
		CACertificate: strings.TrimSpace(values.CACertificate),
		Certificate:   values.KeycloakPostgresCertificate,
		PrivateKey:    values.KeycloakPostgresPrivateKey,
	}
	if err := keycloakPostgres.ValidateIdentity("keycloak-postgres"); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate Keycloak Postgres transport identity: %w", err)
	}
	if err := validateServerCertificateIdentity(values.KeycloakPostgresCertificate, "keycloak-postgres", "keycloak-postgres.noebs.svc", "keycloak-postgres.noebs.svc.cluster.local"); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate Keycloak Postgres transport identity: %w", err)
	}
	keycloak := transportauth.Config{
		CACertificate: strings.TrimSpace(values.CACertificate),
		Certificate:   values.KeycloakCertificate,
		PrivateKey:    values.KeycloakPrivateKey,
	}
	if err := keycloak.ValidateIdentity("keycloak"); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate Keycloak transport identity: %w", err)
	}
	if err := validateServerCertificateIdentity(values.KeycloakCertificate, "keycloak", "keycloak.noebs.svc", "keycloak.noebs.svc.cluster.local"); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate Keycloak transport identity: %w", err)
	}
	temporalPostgres := transportauth.Config{
		CACertificate: strings.TrimSpace(values.CACertificate),
		Certificate:   values.TemporalPostgresCertificate,
		PrivateKey:    values.TemporalPostgresPrivateKey,
	}
	if err := temporalPostgres.ValidateIdentity("temporal-postgres"); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate Temporal Postgres transport identity: %w", err)
	}
	if err := validateServerCertificateIdentity(values.TemporalPostgresCertificate, "temporal-postgres", "temporal-postgres.noebs.svc", "temporal-postgres.noebs.svc.cluster.local"); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate Temporal Postgres transport identity: %w", err)
	}
	temporal := transportauth.Config{
		CACertificate: strings.TrimSpace(values.CACertificate),
		Certificate:   values.TemporalCertificate,
		PrivateKey:    values.TemporalPrivateKey,
	}
	if err := temporal.ValidateIdentity("temporal-frontend"); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate Temporal transport identity: %w", err)
	}
	if err := validateServerCertificateIdentity(values.TemporalCertificate, "temporal-frontend", "temporal-frontend.noebs.svc", "temporal-frontend.noebs.svc.cluster.local"); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate Temporal transport identity: %w", err)
	}
	edge := transportauth.Config{
		CACertificate: strings.TrimSpace(values.CACertificate),
		Certificate:   values.EdgeCertificate,
		PrivateKey:    values.EdgePrivateKey,
	}
	if err := edge.ValidateIdentity(edgeTransportIdentity); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate edge transport identity: %w", err)
	}
	if err := validateClientCertificateIdentity(values.EdgeCertificate, edgeTransportIdentity); err != nil {
		return internalTransportPlatformCredentials{}, fmt.Errorf("validate edge transport identity: %w", err)
	}
	values.CACertificate = postgres.CACertificate
	return values, nil
}

func validateDockerKeycloakPostgresTLS(root string) error {
	caCertificate, err := readDockerKeycloakFile(root, "Keycloak transport CA certificate", "ca.pem")
	if err != nil {
		return err
	}
	certificate, err := readDockerKeycloakFile(root, "Keycloak Postgres TLS certificate", "postgres-tls.crt")
	if err != nil {
		return err
	}
	privateKey, err := readDockerKeycloakFile(root, "Keycloak Postgres TLS private key", "postgres-tls.key")
	if err != nil {
		return err
	}
	config := transportauth.Config{
		CACertificate: caCertificate,
		Certificate:   certificate,
		PrivateKey:    privateKey,
	}
	if err := config.ValidateIdentity("keycloak-postgres"); err != nil {
		return fmt.Errorf("validate Docker Keycloak Postgres transport identity: %w", err)
	}
	if err := validateServerCertificateIdentity(certificate, "keycloak-postgres"); err != nil {
		return fmt.Errorf("validate Docker Keycloak Postgres transport identity: %w", err)
	}
	return nil
}

func validateDockerTemporalTLS(root string) error {
	directory := filepath.Join(root, "deploy", "docker", "temporal")
	ca, err := readRequiredSecretText("Temporal transport CA certificate", filepath.Join(directory, "ca.pem"))
	if err != nil {
		return err
	}
	certificate, err := readRequiredSecretText("Temporal TLS certificate", filepath.Join(directory, "tls.crt"))
	if err != nil {
		return err
	}
	privateKey, err := readRequiredSecretText("Temporal TLS private key", filepath.Join(directory, "tls.key"))
	if err != nil {
		return err
	}
	config := transportauth.Config{CACertificate: ca, Certificate: certificate, PrivateKey: privateKey}
	if err := config.ValidateIdentity("temporal-frontend"); err != nil {
		return fmt.Errorf("validate Docker Temporal transport identity: %w", err)
	}
	if err := validateServerCertificateIdentity(certificate, "temporal-frontend"); err != nil {
		return fmt.Errorf("validate Docker Temporal transport identity: %w", err)
	}
	return nil
}

func validateDockerTemporalPostgresTLS(root string) error {
	directory := filepath.Join(root, "deploy", "docker", "temporal")
	ca, err := readRequiredSecretText("Temporal transport CA certificate", filepath.Join(directory, "ca.pem"))
	if err != nil {
		return err
	}
	certificate, err := readRequiredSecretText("Temporal Postgres TLS certificate", filepath.Join(directory, "postgres-tls.crt"))
	if err != nil {
		return err
	}
	privateKey, err := readRequiredSecretText("Temporal Postgres TLS private key", filepath.Join(directory, "postgres-tls.key"))
	if err != nil {
		return err
	}
	config := transportauth.Config{CACertificate: ca, Certificate: certificate, PrivateKey: privateKey}
	if err := config.ValidateIdentity("temporal-postgres"); err != nil {
		return fmt.Errorf("validate Docker Temporal Postgres transport identity: %w", err)
	}
	if err := validateServerCertificateIdentity(certificate, "temporal-postgres"); err != nil {
		return fmt.Errorf("validate Docker Temporal Postgres transport identity: %w", err)
	}
	return nil
}

func validateDockerKeycloakTLS(root string) error {
	caCertificate, err := readDockerKeycloakFile(root, "Keycloak transport CA certificate", "ca.pem")
	if err != nil {
		return err
	}
	certificate, err := readDockerKeycloakFile(root, "Keycloak TLS certificate", "tls.crt")
	if err != nil {
		return err
	}
	privateKey, err := readDockerKeycloakFile(root, "Keycloak TLS private key", "tls.key")
	if err != nil {
		return err
	}
	config := transportauth.Config{CACertificate: caCertificate, Certificate: certificate, PrivateKey: privateKey}
	if err := config.ValidateIdentity("keycloak"); err != nil {
		return fmt.Errorf("validate Docker Keycloak transport identity: %w", err)
	}
	if err := validateServerCertificateIdentity(certificate, "keycloak"); err != nil {
		return fmt.Errorf("validate Docker Keycloak transport identity: %w", err)
	}
	postgresCertificate, err := readDockerKeycloakFile(root, "Keycloak Postgres TLS certificate", "postgres-tls.crt")
	if err != nil {
		return err
	}
	postgresPrivateKey, err := readDockerKeycloakFile(root, "Keycloak Postgres TLS private key", "postgres-tls.key")
	if err != nil {
		return err
	}
	if strings.TrimSpace(certificate) == strings.TrimSpace(postgresCertificate) || strings.TrimSpace(privateKey) == strings.TrimSpace(postgresPrivateKey) {
		return errors.New("transport identities for Keycloak and Keycloak Postgres must be distinct")
	}
	return nil
}

func readDockerKeycloakFile(root, label, name string) (string, error) {
	path := filepath.Join(root, "deploy", "docker", "keycloak", name)
	if err := requireReadableFile(label, path); err != nil {
		return "", err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	return string(payload), nil
}

func readWorkloadAuthDatabaseCredentials(root, ageKeyPath string, decrypt deploymentDecryptFunc) (preparedWorkloadDatabase, error) {
	path := filepath.Join(root, "platform", "workload-auth-postgres-roles.secrets.yaml")
	if err := requireReadableFile("workload authentication Postgres role credentials", path); err != nil {
		return preparedWorkloadDatabase{}, err
	}
	payload, err := decrypt(path, ageKeyPath)
	if err != nil {
		return preparedWorkloadDatabase{}, fmt.Errorf("decrypt workload authentication Postgres role credentials: %w", err)
	}
	var values struct {
		MigratePassword string `yaml:"migrate_password"`
		RuntimePassword string `yaml:"runtime_password"`
		CleanupPassword string `yaml:"cleanup_password"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(payload)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&values); err != nil {
		return preparedWorkloadDatabase{}, fmt.Errorf("parse workload authentication Postgres role credentials: %w", err)
	}
	credentials := preparedWorkloadDatabase{
		migratePassword: values.MigratePassword,
		runtimePassword: values.RuntimePassword,
		cleanupPassword: values.CleanupPassword,
	}
	for label, value := range map[string]string{
		"migrate_password": credentials.migratePassword,
		"runtime_password": credentials.runtimePassword,
		"cleanup_password": credentials.cleanupPassword,
	} {
		if _, err := prepareWorkloadDatabasePassword(label, value); err != nil {
			return preparedWorkloadDatabase{}, err
		}
	}
	if credentials.migratePassword == credentials.runtimePassword ||
		credentials.migratePassword == credentials.cleanupPassword ||
		credentials.runtimePassword == credentials.cleanupPassword {
		return preparedWorkloadDatabase{}, errors.New("workload authentication database passwords must be distinct")
	}
	return credentials, nil
}

func readGatewayAuthDatabaseCredentials(root, ageKeyPath string, decrypt deploymentDecryptFunc) (preparedGatewayAuthRelease, error) {
	path := filepath.Join(root, "platform", "gateway-auth-postgres-roles.secrets.yaml")
	if err := requireReadableFile("gateway authentication Postgres role credentials", path); err != nil {
		return preparedGatewayAuthRelease{}, err
	}
	payload, err := decrypt(path, ageKeyPath)
	if err != nil {
		return preparedGatewayAuthRelease{}, fmt.Errorf("decrypt gateway authentication Postgres role credentials: %w", err)
	}
	var values struct {
		MigratePassword string `yaml:"migrate_password"`
		RuntimePassword string `yaml:"runtime_password"`
		CleanupPassword string `yaml:"cleanup_password"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(payload)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&values); err != nil {
		return preparedGatewayAuthRelease{}, fmt.Errorf("parse gateway authentication Postgres role credentials: %w", err)
	}
	credentials := preparedGatewayAuthRelease{
		migratePassword: values.MigratePassword,
		runtimePassword: values.RuntimePassword,
		cleanupPassword: values.CleanupPassword,
	}
	for label, value := range map[string]string{
		"migrate_password": credentials.migratePassword,
		"runtime_password": credentials.runtimePassword,
		"cleanup_password": credentials.cleanupPassword,
	} {
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != value {
			return preparedGatewayAuthRelease{}, fmt.Errorf("gateway authentication database %s is invalid", label)
		}
	}
	if credentials.migratePassword == credentials.runtimePassword ||
		credentials.migratePassword == credentials.cleanupPassword ||
		credentials.runtimePassword == credentials.cleanupPassword {
		return preparedGatewayAuthRelease{}, errors.New("gateway authentication database passwords must be distinct")
	}
	return credentials, nil
}

func readServicePostgresCredentials(root, ageKeyPath string, decrypt deploymentDecryptFunc) (map[string]string, error) {
	path := filepath.Join(root, "platform", "service-postgres-roles.secrets.yaml")
	if err := requireReadableFile("service Postgres role credentials", path); err != nil {
		return nil, err
	}
	payload, err := decrypt(path, ageKeyPath)
	if err != nil {
		return nil, fmt.Errorf("decrypt service Postgres role credentials: %w", err)
	}
	var values struct {
		Passwords map[string]string `yaml:"passwords"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(payload)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("parse service Postgres role credentials: %w", err)
	}
	passwords, err := prepareServicePostgresPasswords(values.Passwords)
	if err != nil {
		return nil, err
	}
	return passwords, nil
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
	return validateKubernetesReleaseCoherence(root, configMap, ageKeyPath, decrypt)
}

func validateRenderedKubernetesReleaseServices(root string, configMap map[string]interface{}, read deploymentDecryptFunc) error {
	if err := validateExactRenderedKubernetesReleaseFiles(root); err != nil {
		return err
	}
	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		servicePath := filepath.Join(root, "services", serviceName+".yaml")
		secretPath := filepath.Join(root, "secrets", serviceSecretFileName(serviceName))
		if err := validateDeploymentServiceWithSecretPath(configMap, servicePath, secretPath, "", read); err != nil {
			return err
		}
	}
	return validateKubernetesReleaseCoherence(root, configMap, "", read)
}

func validateExactKubernetesReleaseFiles(root string) error {
	expectedRootEntries := map[string]bool{
		"config.yaml":           true,
		"tenant-catalog.yaml":   true,
		"release-manifest.yaml": true,
		".sops":                 true,
		"platform":              true,
		"secrets":               true,
		"services":              true,
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
		"temporal-postgres-password.txt":            true,
		"keycloak-postgres-password.txt":            true,
		"ghcr-dockerconfigjson":                     true,
		"keycloak.conf":                             true,
		"keycloak-reconciler-config.yaml":           true,
		"workload-auth-postgres-roles.secrets.yaml": true,
		"gateway-auth-postgres-roles.secrets.yaml":  true,
		"service-postgres-roles.secrets.yaml":       true,
		"postgres-provisioning.sql":                 true,
		"internal-transport.secrets.yaml":           true,
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

func validateExactRenderedKubernetesReleaseFiles(root string) error {
	expectedRootEntries := map[string]bool{
		"config.yaml":           true,
		"tenant-catalog.yaml":   true,
		"release-manifest.yaml": true,
		"platform":              true,
		"secrets":               true,
		"services":              true,
	}
	if err := rejectUnexpectedDeploymentEntries("rendered Kubernetes", "root entry", root, expectedRootEntries); err != nil {
		return err
	}

	expectedPlatformFiles := map[string]bool{
		"temporal-postgres-password.txt":            true,
		"keycloak-postgres-password.txt":            true,
		"ghcr-dockerconfigjson":                     true,
		"keycloak.conf":                             true,
		"keycloak-reconciler-config.yaml":           true,
		"workload-auth-postgres-roles.secrets.yaml": true,
		"gateway-auth-postgres-roles.secrets.yaml":  true,
		"service-postgres-roles.secrets.yaml":       true,
		"postgres-provisioning.sql":                 true,
		"internal-transport.secrets.yaml":           true,
	}
	if err := rejectUnexpectedDeploymentEntries("rendered Kubernetes", "platform file", filepath.Join(root, "platform"), expectedPlatformFiles); err != nil {
		return err
	}

	expectedServiceFiles := make(map[string]bool, len(kubernetesSecretReleaseServiceNames))
	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		expectedServiceFiles[serviceName+".yaml"] = true
	}
	if err := rejectUnexpectedDeploymentEntries("rendered Kubernetes", "service config file", filepath.Join(root, "services"), expectedServiceFiles); err != nil {
		return err
	}

	expectedSecretFiles := make(map[string]bool, len(kubernetesServiceSecretSources))
	for _, source := range kubernetesServiceSecretSources {
		expectedSecretFiles[source.fileName] = true
	}
	return rejectUnexpectedDeploymentEntries("rendered Kubernetes", "service secret file", filepath.Join(root, "secrets"), expectedSecretFiles)
}

func validateSteadyKeycloakReconcilerConfig(path string) error {
	_, err := readSteadyKeycloakReconcilerConfig(path)
	return err
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
	if err := rejectRetiredHumanAuthConfig(noebs); err != nil {
		return fmt.Errorf("%s config: %w", serviceName, err)
	}
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
	if err := validateWorkloadAuthRuntimeConfig(role, cfg); err != nil {
		return fmt.Errorf("%s workload authentication config: %w", serviceName, err)
	}
	if err := validateDatabaseTransportRuntimeConfig(role, cfg); err != nil {
		return fmt.Errorf("%s database transport config: %w", serviceName, err)
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

func validatePSPSecretMap(noebs map[string]interface{}, defaultTenantID string) error {
	pspMap := getMap(noebs, "psp")
	if len(pspMap) == 0 {
		return errors.New("missing noebs.psp")
	}
	if len(getMap(pspMap, defaultTenantID)) == 0 {
		return fmt.Errorf("missing noebs.psp.%s", defaultTenantID)
	}

	for _, tenantID := range sortedMapKeys(pspMap) {
		tenantMap, ok := pspMap[tenantID].(map[string]interface{})
		if !ok || len(tenantMap) == 0 {
			return fmt.Errorf("noebs.psp.%s must contain providers", tenantID)
		}
		for _, providerCode := range sortedMapKeys(tenantMap) {
			providerMap, ok := tenantMap[providerCode].(map[string]interface{})
			if !ok {
				return fmt.Errorf("noebs.psp.%s.%s must be a map", tenantID, providerCode)
			}
			for _, key := range []string{"api_key", "api_secret", "webhook_secret", "webhook_public_key"} {
				if strings.TrimSpace(firstString(providerMap, key)) == "" {
					return fmt.Errorf("noebs.psp.%s.%s missing %s", tenantID, providerCode, key)
				}
			}
		}
	}
	return nil
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(entry.Auth))
	if err != nil {
		return fmt.Errorf("%s auths.ghcr.io.auth must be base64 username:token", label)
	}
	username, token, ok := strings.Cut(string(decoded), ":")
	if !ok || strings.TrimSpace(username) == "" || strings.TrimSpace(token) == "" {
		return fmt.Errorf("%s auths.ghcr.io.auth must decode to username:token", label)
	}
	return nil
}

func validateKeycloakConfig(path string, requirePublicContract bool) error {
	values, err := readKeycloakConfigValues(path)
	if err != nil {
		return err
	}
	for _, key := range []string{
		"http-enabled",
		"https-port",
		"https-certificate-file",
		"https-certificate-key-file",
		"https-protocols",
		"http-management-scheme",
		"http-management-port",
		"health-enabled",
		"metrics-enabled",
		"db",
		"db-url",
		"db-username",
		"db-password",
		"db-tls-mode",
		"db-tls-trust-store-file",
	} {
		if strings.TrimSpace(values[key]) == "" {
			return fmt.Errorf("keycloak config missing %s", key)
		}
	}
	for key, expected := range map[string]string{
		"http-enabled":                  "false",
		"https-port":                    "8443",
		"https-certificate-file":        "/opt/keycloak/conf/tls.crt",
		"https-certificate-key-file":    "/opt/keycloak/conf/tls.key",
		"https-protocols":               "TLSv1.3",
		"http-management-scheme":        "http",
		"http-management-port":          "9000",
		"http-management-relative-path": "/",
	} {
		if values[key] != expected {
			return fmt.Errorf("keycloak config %s must be %q", key, expected)
		}
	}
	if _, exists := values["http-port"]; exists {
		return errors.New("keycloak config must not declare an HTTP application port")
	}
	for _, key := range []string{
		"bootstrap-admin-username",
		"bootstrap-admin-password",
		"bootstrap-admin-client-id",
		"bootstrap-admin-client-secret",
	} {
		if _, exists := values[key]; exists {
			return fmt.Errorf("steady Keycloak config must not contain %s", key)
		}
	}
	if !requirePublicContract {
		return validateKeycloakDatabaseConfig(values)
	}
	for key, expected := range map[string]string{
		"http-relative-path":           "/auth",
		"hostname":                     "https://api.noebs.sd/auth",
		"hostname-strict":              "true",
		"hostname-backchannel-dynamic": "false",
		"proxy-headers":                "xforwarded",
		"proxy-trusted-addresses":      "10.42.0.1/32",
	} {
		if values[key] != expected {
			return fmt.Errorf("keycloak config %s must be %q", key, expected)
		}
	}
	return validateKeycloakDatabaseConfig(values)
}

func validateKeycloakDatabaseConfig(values map[string]string) error {
	for _, setting := range []struct {
		key   string
		value string
	}{
		{key: "db", value: "postgres"},
		{key: "db-url", value: "jdbc:postgresql://keycloak-postgres:5432/keycloak"},
		{key: "db-username", value: "keycloak"},
		{key: "db-tls-mode", value: "verify-server"},
		{key: "db-tls-trust-store-file", value: "/opt/keycloak/conf/db-ca.pem"},
	} {
		if values[setting.key] != setting.value {
			return fmt.Errorf("keycloak config %s must be %q", setting.key, setting.value)
		}
	}
	return nil
}

func readKeycloakConfigValues(path string) (map[string]string, error) {
	if err := requireReadableFile("Keycloak config", path); err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Keycloak config: %w", err)
	}
	if strings.Contains(string(payload), "REPLACE_WITH_") {
		return nil, errors.New("keycloak config contains placeholder")
	}
	values := map[string]string{}
	for lineIndex, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("keycloak config line %d must be key=value", lineIndex+1)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return nil, fmt.Errorf("keycloak config line %d has empty key or value", lineIndex+1)
		}
		if _, duplicate := values[key]; duplicate {
			return nil, fmt.Errorf("keycloak config contains duplicate %s", key)
		}
		values[key] = value
	}
	return values, nil
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
	return serviceName + ".secrets.yaml"
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
