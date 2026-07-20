package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	kubernetesReleaseManifestAPIVersion = "noebs.sd/kubernetes-release/v1"
	kubernetesReleaseManifestFile       = "release-manifest.yaml"
)

type kubernetesReleaseManifest struct {
	APIVersion  string            `yaml:"api_version"`
	Fingerprint string            `yaml:"fingerprint"`
	Artifacts   map[string]string `yaml:"artifacts"`
}

func writeKubernetesReleaseManifest(root string) error {
	artifacts, fingerprint, err := calculateKubernetesReleaseManifest(root)
	if err != nil {
		return err
	}
	payload, err := yaml.Marshal(kubernetesReleaseManifest{
		APIVersion:  kubernetesReleaseManifestAPIVersion,
		Fingerprint: fingerprint,
		Artifacts:   artifacts,
	})
	if err != nil {
		return fmt.Errorf("marshal Kubernetes release manifest: %w", err)
	}
	return writeReleaseFile(root, kubernetesReleaseManifestFile, string(payload))
}

func renderDecryptedKubernetesReleaseManifest(root, ageKeyPath string, decrypt deploymentDecryptFunc) (string, error) {
	payloads := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative == ".sops" {
				return filepath.SkipDir
			}
			return nil
		}
		if relative == kubernetesReleaseManifestFile {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("kubernetes release artifact must be a regular file: %s", relative)
		}
		var payload []byte
		if renderedKubernetesArtifactIsEncrypted(relative) {
			payload, err = decrypt(path, ageKeyPath)
		} else {
			payload, err = os.ReadFile(path)
		}
		if err != nil {
			return err
		}
		payloads[relative] = payload
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("inventory rendered Kubernetes release artifacts: %w", err)
	}
	artifacts, fingerprint, err := calculateKubernetesReleaseManifestPayloads(payloads)
	if err != nil {
		return "", err
	}
	payload, err := yaml.Marshal(kubernetesReleaseManifest{
		APIVersion:  kubernetesReleaseManifestAPIVersion,
		Fingerprint: fingerprint,
		Artifacts:   artifacts,
	})
	if err != nil {
		return "", fmt.Errorf("marshal rendered Kubernetes release manifest: %w", err)
	}
	return string(payload), nil
}

func renderedKubernetesArtifactIsEncrypted(relative string) bool {
	if strings.HasPrefix(relative, "secrets/") {
		return true
	}
	switch relative {
	case "platform/workload-auth-postgres-roles.secrets.yaml",
		"platform/gateway-auth-postgres-roles.secrets.yaml",
		"platform/service-postgres-roles.secrets.yaml",
		"platform/internal-transport.secrets.yaml":
		return true
	default:
		return false
	}
}

func validateKubernetesReleaseManifest(root string) error {
	path := filepath.Join(root, kubernetesReleaseManifestFile)
	payload, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Kubernetes release manifest: %w", err)
	}
	var manifest kubernetesReleaseManifest
	decoder := yaml.NewDecoder(bytes.NewReader(payload))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("parse Kubernetes release manifest: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("kubernetes release manifest must contain one YAML document")
		}
		return fmt.Errorf("parse Kubernetes release manifest: %w", err)
	}
	if manifest.APIVersion != kubernetesReleaseManifestAPIVersion {
		return fmt.Errorf("kubernetes release manifest api_version must be %q", kubernetesReleaseManifestAPIVersion)
	}
	artifacts, fingerprint, err := calculateKubernetesReleaseManifest(root)
	if err != nil {
		return err
	}
	if manifest.Fingerprint != fingerprint {
		return errors.New("kubernetes release fingerprint does not match mounted artifacts")
	}
	if len(manifest.Artifacts) != len(artifacts) {
		return errors.New("kubernetes release manifest artifact set does not match mounted artifacts")
	}
	for name, digest := range artifacts {
		if manifest.Artifacts[name] != digest {
			return fmt.Errorf("kubernetes release artifact %s does not match its manifest digest", name)
		}
	}
	return nil
}

func calculateKubernetesReleaseManifest(root string) (map[string]string, string, error) {
	payloads := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == kubernetesReleaseManifestFile {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("kubernetes release artifact must be a regular file: %s", relative)
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		payloads[relative] = payload
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("inventory Kubernetes release artifacts: %w", err)
	}
	return calculateKubernetesReleaseManifestPayloads(payloads)
}

func calculateKubernetesReleaseManifestPayloads(payloads map[string][]byte) (map[string]string, string, error) {
	if len(payloads) == 0 {
		return nil, "", errors.New("kubernetes release contains no artifacts")
	}
	artifacts := make(map[string]string, len(payloads))
	for name, payload := range payloads {
		digest := sha256.Sum256(payload)
		artifacts[name] = "sha256:" + hex.EncodeToString(digest[:])
	}
	names := make([]string, 0, len(artifacts))
	for name := range artifacts {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(strings.TrimPrefix(artifacts[name], "sha256:")))
		_, _ = hash.Write([]byte{'\n'})
	}
	return artifacts, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
