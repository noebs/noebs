package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/adonese/noebs/internal/keycloakadmin"
	"gopkg.in/yaml.v3"
)

const keycloakBootstrapInputAPIVersion = "noebs.sd/keycloak-bootstrap/v1"

type keycloakBootstrapInput struct {
	APIVersion   string `yaml:"api_version"`
	ClientSecret string `yaml:"client_secret"`
}

func isRenderKeycloakBootstrapSecretsCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "render-keycloak-bootstrap-secrets"
}

func renderKeycloakBootstrapSecretsCommand() error {
	if len(os.Args) != 6 {
		return errors.New("usage: noebs render-keycloak-bootstrap-secrets <kubernetes-release-root> <namespace> <bootstrap-input-yaml> <output-yaml>")
	}
	return renderKeycloakBootstrapSecrets(os.Args[2], os.Args[3], os.Args[4], os.Args[5], decryptSopsFile)
}

func renderKeycloakBootstrapSecrets(root, namespace, inputPath, outputPath string, decrypt deploymentDecryptFunc) error {
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
	input, err := readKeycloakBootstrapInput(inputPath, filepath.Join(root, ".sops", "age-key.txt"), decrypt)
	if err != nil {
		return err
	}
	steady, err := readSteadyKeycloakReconcilerConfig(filepath.Join(root, "platform", "keycloak-reconciler-config.yaml"))
	if err != nil {
		return err
	}
	bootstrap := keycloakadmin.Config{
		BaseURL:           steady.BaseURL,
		AdminRealm:        "master",
		ClientID:          keycloakadmin.BootstrapClientID,
		ClientSecret:      input.ClientSecret,
		ClientCredentials: steady.ClientCredentials,
		IdentityProviders: steady.IdentityProviders,
	}
	if err := bootstrap.Validate(); err != nil {
		return err
	}
	configPayload, err := yaml.Marshal(bootstrap)
	if err != nil {
		return fmt.Errorf("marshal bootstrap Keycloak reconciler config: %w", err)
	}
	manifests := []kubernetesSecretManifest{
		newOpaqueSecret(namespace, "keycloak-bootstrap-admin", map[string]string{"client-secret": input.ClientSecret}),
		newOpaqueSecret(namespace, "keycloak-bootstrap-reconciler-credentials", map[string]string{"config.yaml": string(configPayload)}),
	}
	var payload bytes.Buffer
	if err := writeKubernetesSecretManifests(&payload, manifests); err != nil {
		return err
	}
	return writeExclusiveSecretFile(outputPath, payload.Bytes())
}

func readKeycloakBootstrapInput(path, ageKeyPath string, decrypt deploymentDecryptFunc) (keycloakBootstrapInput, error) {
	path = strings.TrimSpace(path)
	if err := requireReadableFile("Keycloak bootstrap input", path); err != nil {
		return keycloakBootstrapInput{}, err
	}
	payload, err := decrypt(path, ageKeyPath)
	if err != nil {
		return keycloakBootstrapInput{}, fmt.Errorf("decrypt Keycloak bootstrap input: %w", err)
	}
	var input keycloakBootstrapInput
	decoder := yaml.NewDecoder(bytes.NewReader(payload))
	decoder.KnownFields(true)
	if err := decoder.Decode(&input); err != nil {
		return keycloakBootstrapInput{}, fmt.Errorf("parse Keycloak bootstrap input: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return keycloakBootstrapInput{}, errors.New("keycloak bootstrap input must contain one YAML document")
		}
		return keycloakBootstrapInput{}, fmt.Errorf("parse Keycloak bootstrap input: %w", err)
	}
	if input.APIVersion != keycloakBootstrapInputAPIVersion {
		return keycloakBootstrapInput{}, fmt.Errorf("keycloak bootstrap input api_version must be %q", keycloakBootstrapInputAPIVersion)
	}
	input.ClientSecret, err = requireCanonicalReleaseSecret("Keycloak bootstrap client secret", input.ClientSecret)
	if err != nil {
		return keycloakBootstrapInput{}, err
	}
	return input, nil
}

func writeExclusiveSecretFile(path string, payload []byte) (err error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("secret output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create secret output directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create secret output file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close secret output file: %w", closeErr)
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err = file.Write(payload); err != nil {
		return fmt.Errorf("write secret output file: %w", err)
	}
	return nil
}
