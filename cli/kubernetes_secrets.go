package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

var kubernetesServiceSecretSources = []kubernetesServiceSecretSource{
	{serviceName: "api-gateway", secretName: "api-gateway-secrets", fileName: "api-gateway.secrets.yaml"},
	{serviceName: "identity-auth", secretName: "identity-auth-secrets", fileName: "identity-auth.secrets.yaml"},
	{serviceName: "card-vault", secretName: "card-vault-secrets", fileName: "card-vault.secrets.yaml"},
	{serviceName: "ebs-adapter", secretName: "ebs-adapter-secrets", fileName: "ebs-adapter.secrets.yaml"},
	{serviceName: "psp-webhook", secretName: "psp-webhook-secrets", fileName: "psp-webhook.secrets.yaml"},
	{serviceName: "admin-reporting", secretName: "admin-reporting-secrets", fileName: "admin-reporting.secrets.yaml"},
	{serviceName: "notification-chat", secretName: "notification-chat-secrets", fileName: "notification-chat.secrets.yaml"},
	{serviceName: "consumer-beneficiary", secretName: "consumer-beneficiary-secrets", fileName: "consumer-beneficiary.secrets.yaml"},
	{serviceName: "wallet-api", secretName: "wallet-api-secrets", fileName: "wallet-api.secrets.yaml"},
	{serviceName: "wallet-ledger", secretName: "wallet-ledger-secrets", fileName: "wallet-ledger.secrets.yaml"},
	{serviceName: "wallet-worker", secretName: "wallet-worker-secrets", fileName: "wallet-worker.secrets.yaml"},
}

func renderKubernetesSecretsCommand() error {
	if len(os.Args) != 6 {
		return errors.New("usage: noebs render-kubernetes-secrets <release-root> <namespace> <tls-cert> <tls-key>")
	}
	return renderKubernetesSecrets(os.Stdout, os.Args[2], os.Args[3], os.Args[4], os.Args[5], decryptSopsFile)
}

func renderKubernetesSecrets(w io.Writer, root, namespace, tlsCertPath, tlsKeyPath string, decrypt deploymentDecryptFunc) error {
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
	if err := validateDeploymentRootWithDecrypt(root, decrypt); err != nil {
		return err
	}

	ageKeyPath := filepath.Join(root, ".sops", "age-key.txt")
	postgresPassword, err := readPostgresBootstrapPassword(root, decrypt, ageKeyPath)
	if err != nil {
		return err
	}
	temporalPostgresPassword, err := readRequiredSecretValue("Temporal Postgres password", filepath.Join(root, "deploy", "docker", "temporal", "postgres-password.txt"))
	if err != nil {
		return err
	}
	keycloakPostgresPassword, err := readRequiredSecretValue("Keycloak Postgres password", filepath.Join(root, "deploy", "docker", "keycloak", "postgres-password.txt"))
	if err != nil {
		return err
	}
	keycloakConfig, err := readRequiredSecretText("Keycloak config", filepath.Join(root, "deploy", "docker", "keycloak", "keycloak.conf"))
	if err != nil {
		return err
	}
	ageKey, err := readRequiredSecretText("SOPS age key", ageKeyPath)
	if err != nil {
		return err
	}
	tlsCert, tlsKey, err := readTLSSecretPair(tlsCertPath, tlsKeyPath)
	if err != nil {
		return err
	}

	manifests := make([]kubernetesSecretManifest, 0, len(kubernetesServiceSecretSources)+6)
	for _, source := range kubernetesServiceSecretSources {
		payload, err := readRequiredSecretText(source.serviceName+" secrets", filepath.Join(root, "deploy", "docker", "secrets", source.fileName))
		if err != nil {
			return err
		}
		manifests = append(manifests, newOpaqueSecret(namespace, source.secretName, map[string]string{
			"secrets.yaml": payload,
		}))
	}
	manifests = append(manifests,
		newOpaqueSecret(namespace, "sops-age-key", map[string]string{"age-key.txt": ageKey}),
		newOpaqueSecret(namespace, "postgres-credentials", map[string]string{"password": postgresPassword}),
		newOpaqueSecret(namespace, "temporal-postgres-credentials", map[string]string{"password": temporalPostgresPassword}),
		newOpaqueSecret(namespace, "keycloak-postgres-credentials", map[string]string{"password": keycloakPostgresPassword}),
		newOpaqueSecret(namespace, "keycloak-secrets", map[string]string{"keycloak.conf": keycloakConfig}),
		kubernetesSecretManifest{
			APIVersion: "v1",
			Kind:       "Secret",
			Type:       "kubernetes.io/tls",
			Metadata:   kubernetesMeta{Name: "noebs-tls", Namespace: namespace},
			StringData: map[string]string{"tls.crt": tlsCert, "tls.key": tlsKey},
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

func newOpaqueSecret(namespace, name string, data map[string]string) kubernetesSecretManifest {
	return kubernetesSecretManifest{
		APIVersion: "v1",
		Kind:       "Secret",
		Type:       "Opaque",
		Metadata:   kubernetesMeta{Name: name, Namespace: namespace},
		StringData: data,
	}
}

func readPostgresBootstrapPassword(root string, decrypt deploymentDecryptFunc, ageKeyPath string) (string, error) {
	secretPath := filepath.Join(root, "deploy", "docker", "postgres", "bootstrap.secrets.yaml")
	payload, err := decrypt(secretPath, ageKeyPath)
	if err != nil {
		return "", fmt.Errorf("decrypt Postgres bootstrap secret: %w", err)
	}
	secretMap, err := parseYAMLMap(secretPath, payload)
	if err != nil {
		return "", err
	}
	noebs := getMap(secretMap, "noebs")
	if noebs == nil {
		return "", errors.New("postgres bootstrap secret missing noebs")
	}
	if err := rejectLegacyDatabasePath(noebs); err != nil {
		return "", fmt.Errorf("postgres bootstrap database config: %w", err)
	}
	if err := rejectPlaceholders("Postgres bootstrap secret", noebs); err != nil {
		return "", err
	}
	return databaseURLPassword("Postgres bootstrap db_url", firstString(noebs, "db_url"))
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

func readTLSSecretPair(certPath, keyPath string) (string, string, error) {
	cert, err := readRequiredSecretText("TLS certificate", certPath)
	if err != nil {
		return "", "", err
	}
	key, err := readRequiredSecretText("TLS private key", keyPath)
	if err != nil {
		return "", "", err
	}
	if _, err := tls.X509KeyPair([]byte(cert), []byte(key)); err != nil {
		return "", "", fmt.Errorf("validate TLS certificate and key: %w", err)
	}
	return cert, key, nil
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
