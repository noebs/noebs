package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type kubernetesReleaseInputAudit struct {
	Ready         bool     `yaml:"ready"`
	CurrentSecret []string `yaml:"current_secret,omitempty"`
	CutoverInput  []string `yaml:"cutover_input,omitempty"`
	Missing       []string `yaml:"missing,omitempty"`
	Duplicate     []string `yaml:"duplicate,omitempty"`
	Invalid       []string `yaml:"invalid,omitempty"`
}

func isAuditKubernetesReleaseInputsCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "audit-kubernetes-release-inputs"
}

func auditKubernetesReleaseInputsCommand() error {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		return errors.New("usage: noebs audit-kubernetes-release-inputs <legacy-root> [inputs-yaml]")
	}
	inputsPath := ""
	if len(os.Args) == 4 {
		inputsPath = os.Args[3]
	}
	audit, err := auditKubernetesReleaseInputs(os.Args[2], inputsPath, decryptSopsFile)
	if err != nil {
		return err
	}
	if err := writeKubernetesReleaseInputAudit(os.Stdout, audit); err != nil {
		return err
	}
	if !audit.Ready {
		return errors.New("kubernetes release inputs are incomplete")
	}
	return nil
}

func auditKubernetesReleaseInputs(legacyRoot, inputsPath string, decrypt deploymentDecryptFunc) (kubernetesReleaseInputAudit, error) {
	legacyRoot, err := resolveDeploymentRoot(legacyRoot)
	if err != nil {
		return kubernetesReleaseInputAudit{}, err
	}
	if decrypt == nil {
		return kubernetesReleaseInputAudit{}, errors.New("deployment decrypt function is required")
	}
	ageKeyPath := filepath.Join(legacyRoot, ".sops", "age-key.txt")
	if err := requireReadableFile("SOPS age key", ageKeyPath); err != nil {
		return kubernetesReleaseInputAudit{}, err
	}
	legacy, err := readLegacyNoebsConfig(legacyRoot, ageKeyPath, decrypt)
	if err != nil {
		return kubernetesReleaseInputAudit{}, err
	}
	var inputs kubernetesReleaseInputs
	if strings.TrimSpace(inputsPath) != "" {
		inputs, err = readKubernetesReleaseInputs(inputsPath, ageKeyPath, decrypt)
		if err != nil {
			return kubernetesReleaseInputAudit{}, err
		}
	}
	release := preparedKubernetesRelease{
		legacy: legacy,
		inputs: inputs,
	}
	return release.auditInputs(), nil
}

func (r preparedKubernetesRelease) auditInputs() kubernetesReleaseInputAudit {
	audit := kubernetesReleaseInputAudit{}
	tenantID, tenantReady := audit.auditCutoverField(r, cutoverStringField{
		label:      "noebs.default_tenant_id",
		legacyKeys: []string{"default_tenant_id"},
		input:      r.inputs.Noebs.DefaultTenantID,
	})
	if tenantReady {
		if _, err := validateTenantID(tenantID); err != nil {
			audit.Invalid = append(audit.Invalid, fmt.Sprintf("noebs.default_tenant_id: %v", err))
		}
	}

	for _, field := range r.cutoverStringFields() {
		audit.auditCutoverField(r, field)
	}
	audit.auditPSPSecrets(r, tenantID, tenantReady)
	audit.auditCurrentSecretOnly(r, "noebs.db_url", "db_url")
	audit.auditCurrentSecretOnly(r, "noebs.jwt_secret", "jwt_secret")
	if _, err := r.serviceDatabaseURL("identity-auth"); err != nil {
		audit.Invalid = append(audit.Invalid, err.Error())
	}

	audit.normalize()
	return audit
}

func (a *kubernetesReleaseInputAudit) auditCutoverField(r preparedKubernetesRelease, field cutoverStringField) (string, bool) {
	legacyValue, legacyKey := r.firstLegacyString(field.legacyKeys...)
	input := strings.TrimSpace(field.input)
	switch {
	case legacyValue != "" && input != "":
		a.Duplicate = append(a.Duplicate, fmt.Sprintf("%s duplicates current secret noebs.%s", field.label, legacyKey))
		return "", false
	case legacyValue != "":
		if strings.Contains(legacyValue, "REPLACE_WITH_") {
			a.Invalid = append(a.Invalid, fmt.Sprintf("current secret noebs.%s contains placeholder", legacyKey))
			return "", false
		}
		a.CurrentSecret = append(a.CurrentSecret, fmt.Sprintf("%s from current secret noebs.%s", field.label, legacyKey))
		return legacyValue, true
	case input != "":
		if strings.Contains(input, "REPLACE_WITH_") {
			a.Invalid = append(a.Invalid, fmt.Sprintf("kubernetes release input %s contains placeholder", field.label))
			return "", false
		}
		a.CutoverInput = append(a.CutoverInput, field.label)
		return input, true
	default:
		a.Missing = append(a.Missing, field.label)
		return "", false
	}
}

func (a *kubernetesReleaseInputAudit) auditCurrentSecretOnly(r preparedKubernetesRelease, label, key string) {
	value := strings.TrimSpace(firstString(r.legacy, key))
	switch {
	case value == "":
		a.Missing = append(a.Missing, label+" from current secret")
	case strings.Contains(value, "REPLACE_WITH_"):
		a.Invalid = append(a.Invalid, "current secret "+label+" contains placeholder")
	default:
		a.CurrentSecret = append(a.CurrentSecret, label+" from current secret")
	}
}

func (a *kubernetesReleaseInputAudit) auditPSPSecrets(r preparedKubernetesRelease, tenantID string, tenantReady bool) {
	legacyPSP := getMap(r.legacy, "psp")
	inputPSP := pspInputsToMap(r.inputs.Noebs.PSP)
	hasLegacy := len(legacyPSP) != 0
	hasInput := len(inputPSP) != 0
	var psp map[string]interface{}
	switch {
	case hasLegacy && hasInput:
		a.Duplicate = append(a.Duplicate, "noebs.psp duplicates current secret noebs.psp")
		return
	case hasLegacy:
		a.CurrentSecret = append(a.CurrentSecret, "noebs.psp from current secret")
		psp = legacyPSP
	case hasInput:
		a.CutoverInput = append(a.CutoverInput, "noebs.psp")
		psp = inputPSP
	default:
		a.Missing = append(a.Missing, "noebs.psp")
		return
	}
	if !tenantReady {
		return
	}
	if err := validatePSPSecretMap(map[string]interface{}{"psp": psp, "default_tenant_id": tenantID}, tenantID); err != nil {
		a.Invalid = append(a.Invalid, "noebs.psp: "+err.Error())
	}
}

func (a *kubernetesReleaseInputAudit) normalize() {
	sort.Strings(a.CurrentSecret)
	sort.Strings(a.CutoverInput)
	sort.Strings(a.Missing)
	sort.Strings(a.Duplicate)
	sort.Strings(a.Invalid)
	a.Ready = len(a.Missing) == 0 && len(a.Duplicate) == 0 && len(a.Invalid) == 0
}

func writeKubernetesReleaseInputAudit(w io.Writer, audit kubernetesReleaseInputAudit) error {
	encoder := yaml.NewEncoder(w)
	defer func() {
		_ = encoder.Close()
	}()
	if err := encoder.Encode(audit); err != nil {
		return fmt.Errorf("write kubernetes release input audit: %w", err)
	}
	return nil
}
