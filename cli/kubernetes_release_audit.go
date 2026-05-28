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
	Ready              bool     `yaml:"ready"`
	CurrentSecret      []string `yaml:"current_secret,omitempty"`
	EmptyCurrentSecret []string `yaml:"empty_current_secret,omitempty"`
	CutoverInput       []string `yaml:"cutover_input,omitempty"`
	Missing            []string `yaml:"missing,omitempty"`
	Duplicate          []string `yaml:"duplicate,omitempty"`
	Invalid            []string `yaml:"invalid,omitempty"`
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

func renderKubernetesReleaseInputTemplateCommand() error {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		return errors.New("usage: noebs render-kubernetes-release-input-template <legacy-root> [inputs-yaml]")
	}
	inputsPath := ""
	if len(os.Args) == 4 {
		inputsPath = os.Args[3]
	}
	audit, err := auditKubernetesReleaseInputs(os.Args[2], inputsPath, decryptSopsFile)
	if err != nil {
		return err
	}
	return writeKubernetesReleaseInputTemplate(os.Stdout, audit)
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
	legacyValue, legacyKey, legacyPresent := r.firstLegacyValue(field.legacyKeys...)
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
		if legacyPresent {
			a.EmptyCurrentSecret = append(a.EmptyCurrentSecret, fmt.Sprintf("current secret noebs.%s is empty", legacyKey))
		}
		a.Missing = append(a.Missing, field.label)
		return "", false
	}
}

func (a *kubernetesReleaseInputAudit) auditCurrentSecretOnly(r preparedKubernetesRelease, label, key string) {
	value, _, present := r.firstLegacyValue(key)
	switch {
	case value == "":
		if present {
			a.EmptyCurrentSecret = append(a.EmptyCurrentSecret, "current secret "+label+" is empty")
		}
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
	sort.Strings(a.EmptyCurrentSecret)
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

type kubernetesReleaseTemplateField struct {
	label       string
	key         string
	placeholder string
	section     string
}

var kubernetesReleaseTemplateFields = []kubernetesReleaseTemplateField{
	{label: "noebs.default_tenant_id", key: "default_tenant_id", placeholder: "REPLACE_WITH_TENANT_ID"},
	{label: "noebs.admin_key", key: "admin_key", placeholder: "REPLACE_WITH_GATEWAY_ADMIN_KEY"},
	{label: "noebs.admin_user", key: "admin_user", placeholder: "REPLACE_WITH_GATEWAY_ADMIN_USER"},
	{label: "noebs.admin_password", key: "admin_password", placeholder: "REPLACE_WITH_GATEWAY_ADMIN_PASSWORD"},
	{label: "noebs.sms_key", key: "sms_key", placeholder: "REPLACE_WITH_SMS_API_KEY"},
	{label: "noebs.sms_sender", key: "sms_sender", placeholder: "REPLACE_WITH_SMS_SENDER"},
	{label: "noebs.sms_gateway", key: "sms_gateway", placeholder: "REPLACE_WITH_SMS_GATEWAY"},
	{label: "noebs.sms_message", key: "sms_message", placeholder: "REPLACE_WITH_SMS_MESSAGE"},
	{label: "noebs.google_client_id", key: "google_client_id", placeholder: "REPLACE_WITH_GOOGLE_CLIENT_ID"},
	{label: "noebs.google_client_secret", key: "google_client_secret", placeholder: "REPLACE_WITH_GOOGLE_CLIENT_SECRET"},
	{label: "noebs.google_redirect_url", key: "google_redirect_url", placeholder: "REPLACE_WITH_GOOGLE_REDIRECT_URL"},
	{label: "noebs.card_vault_data_key", key: "card_vault_data_key", placeholder: "REPLACE_WITH_CARD_VAULT_DATA_KEY"},
	{label: "noebs.temporal_postgres_password", key: "temporal_postgres_password", placeholder: "REPLACE_WITH_TEMPORAL_POSTGRES_PASSWORD"},
	{label: "noebs.keycloak_postgres_password", key: "keycloak_postgres_password", placeholder: "REPLACE_WITH_KEYCLOAK_POSTGRES_PASSWORD"},
	{label: "noebs.keycloak_bootstrap_admin_username", key: "keycloak_bootstrap_admin_username", placeholder: "REPLACE_WITH_KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME"},
	{label: "noebs.keycloak_bootstrap_admin_password", key: "keycloak_bootstrap_admin_password", placeholder: "REPLACE_WITH_KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD"},
	{label: "noebs.ghcr_dockerconfigjson", key: "ghcr_dockerconfigjson", placeholder: `{"auths":{"ghcr.io":{"auth":"REPLACE_WITH_GHCR_AUTH_BASE64"}}}`},
	{label: "noebs.ebs.consumer_endpoint", key: "consumer_endpoint", placeholder: "REPLACE_WITH_EBS_CONSUMER_ENDPOINT", section: "ebs"},
	{label: "noebs.ebs.merchant_endpoint", key: "merchant_endpoint", placeholder: "REPLACE_WITH_EBS_MERCHANT_ENDPOINT", section: "ebs"},
	{label: "noebs.ebs.ipin_endpoint", key: "ipin_endpoint", placeholder: "REPLACE_WITH_EBS_IPIN_ENDPOINT", section: "ebs"},
	{label: "noebs.ebs.consumer_app_id", key: "consumer_app_id", placeholder: "REPLACE_WITH_EBS_CONSUMER_APP_ID", section: "ebs"},
	{label: "noebs.ebs.merchant_app_id", key: "merchant_app_id", placeholder: "REPLACE_WITH_EBS_MERCHANT_APP_ID", section: "ebs"},
	{label: "noebs.ebs.ipin_username", key: "ipin_username", placeholder: "REPLACE_WITH_EBS_IPIN_USERNAME", section: "ebs"},
	{label: "noebs.ebs.ipin_password", key: "ipin_password", placeholder: "REPLACE_WITH_EBS_IPIN_PASSWORD", section: "ebs"},
	{label: "noebs.ebs.pub_key", key: "pub_key", placeholder: "REPLACE_WITH_EBS_CONSUMER_PUBLIC_KEY", section: "ebs"},
	{label: "noebs.ebs.ipin_key", key: "ipin_key", placeholder: "REPLACE_WITH_EBS_IPIN_PUBLIC_KEY", section: "ebs"},
	{label: "noebs.ebs.pan", key: "pan", placeholder: "REPLACE_WITH_BILL_INQUIRY_PAN", section: "ebs"},
	{label: "noebs.ebs.pin", key: "pin", placeholder: "REPLACE_WITH_BILL_INQUIRY_PIN", section: "ebs"},
	{label: "noebs.ebs.ipin", key: "ipin", placeholder: "REPLACE_WITH_BILL_INQUIRY_IPIN", section: "ebs"},
	{label: "noebs.ebs.exp_date", key: "exp_date", placeholder: "REPLACE_WITH_BILL_INQUIRY_EXPIRY", section: "ebs"},
}

func writeKubernetesReleaseInputTemplate(w io.Writer, audit kubernetesReleaseInputAudit) error {
	missing := make(map[string]bool, len(audit.Missing))
	for _, label := range audit.Missing {
		missing[label] = true
	}
	known := map[string]bool{"noebs.psp": true}
	for _, field := range kubernetesReleaseTemplateFields {
		known[field.label] = true
	}
	var unsupported []string
	for _, label := range audit.Missing {
		if !known[label] {
			unsupported = append(unsupported, label)
		}
	}
	if len(unsupported) != 0 {
		return fmt.Errorf("cannot template fields that must come from the current secret: %s", strings.Join(unsupported, ", "))
	}
	if len(audit.Duplicate) != 0 || len(audit.Invalid) != 0 {
		return fmt.Errorf("kubernetes release input audit has duplicate or invalid fields")
	}

	root := &yaml.Node{Kind: yaml.MappingNode}
	noebs := &yaml.Node{Kind: yaml.MappingNode}
	var ebs *yaml.Node
	for _, field := range kubernetesReleaseTemplateFields {
		if !missing[field.label] {
			continue
		}
		if field.section == "ebs" {
			if ebs == nil {
				ebs = &yaml.Node{Kind: yaml.MappingNode}
			}
			appendYAMLScalar(ebs, field.key, field.placeholder)
			continue
		}
		appendYAMLScalar(noebs, field.key, field.placeholder)
	}
	if ebs != nil {
		appendYAMLNode(noebs, "ebs", ebs)
	}
	if missing["noebs.psp"] {
		appendYAMLNode(noebs, "psp", kubernetesReleasePSPTemplateNode())
	}
	appendYAMLNode(root, "noebs", noebs)

	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(2)
	defer func() {
		_ = encoder.Close()
	}()
	if err := encoder.Encode(root); err != nil {
		return fmt.Errorf("write kubernetes release input template: %w", err)
	}
	return nil
}

func appendYAMLScalar(parent *yaml.Node, key, value string) {
	appendYAMLNode(parent, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func appendYAMLNode(parent *yaml.Node, key string, value *yaml.Node) {
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func kubernetesReleasePSPTemplateNode() *yaml.Node {
	provider := &yaml.Node{Kind: yaml.MappingNode}
	appendYAMLScalar(provider, "api_key", "REPLACE_WITH_PSP_API_KEY")
	appendYAMLScalar(provider, "api_secret", "REPLACE_WITH_PSP_API_SECRET")
	appendYAMLScalar(provider, "webhook_secret", "REPLACE_WITH_PSP_WEBHOOK_SECRET")
	appendYAMLScalar(provider, "webhook_public_key", "REPLACE_WITH_PSP_WEBHOOK_PUBLIC_KEY")

	tenant := &yaml.Node{Kind: yaml.MappingNode}
	appendYAMLNode(tenant, "REPLACE_WITH_PROVIDER_CODE", provider)

	psp := &yaml.Node{Kind: yaml.MappingNode}
	appendYAMLNode(psp, "REPLACE_WITH_TENANT_ID", tenant)
	return psp
}
