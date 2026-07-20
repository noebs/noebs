package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/keycloakadmin"
	"gopkg.in/yaml.v3"
)

func TestPrepareKubernetesReleaseUsesOnlyExplicitAuthority(t *testing.T) {
	inputRoot := t.TempDir()
	inputsPath := writeKubernetesReleaseInputsFile(t, inputRoot, "tenant-cutover")
	outputRoot := filepath.Join(t.TempDir(), "release")

	err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err != nil {
		t.Fatalf("prepareKubernetesRelease() error = %v", err)
	}

	for _, name := range []string{
		"config.yaml",
		"tenant-catalog.yaml",
		"release-manifest.yaml",
		"services/api-gateway.yaml",
		"platform/keycloak.conf",
		"platform/keycloak-reconciler-config.yaml",
		"platform/gateway-auth-postgres-roles.secrets.yaml",
		"platform/workload-auth-postgres-roles.secrets.yaml",
		"platform/service-postgres-roles.secrets.yaml",
		"platform/postgres-provisioning.sql",
		"platform/internal-transport.secrets.yaml",
		"secrets/api-gateway.secrets.yaml",
	} {
		requirePreparedFile(t, outputRoot, name)
	}
	for path, want := range map[string]string{
		"platform/temporal-postgres-password.txt": "temporal-postgres-password",
		"platform/keycloak-postgres-password.txt": "keycloak-postgres-password",
	} {
		if got := readPreparedFile(t, outputRoot, path); got != want {
			t.Fatalf("prepared %s = %q, want exact mounted password bytes", path, got)
		}
	}
	keycloakConfig := readPreparedFile(t, outputRoot, "platform/keycloak.conf")
	if !strings.Contains(keycloakConfig, "proxy-trusted-addresses=10.42.0.1/32\n") {
		t.Fatal("prepared Keycloak config must trust only the observed host-network Caddy source")
	}
	for _, required := range []string{
		"db-tls-mode=verify-server\n",
		"db-tls-trust-store-file=/opt/keycloak/conf/db-ca.pem\n",
	} {
		if !strings.Contains(keycloakConfig, required) {
			t.Fatalf("prepared Keycloak config missing %q", required)
		}
	}
	for _, forbidden := range []string{"proxy-trusted-addresses=10.42.0.0/16", "proxy-trusted-addresses=213.199.63.78/32"} {
		if strings.Contains(keycloakConfig, forbidden) {
			t.Fatalf("prepared Keycloak config contains overbroad or incorrect proxy trust %q", forbidden)
		}
	}

	var reconciler keycloakadmin.Config
	payload := readPreparedFile(t, outputRoot, "platform/keycloak-reconciler-config.yaml")
	if err := yaml.Unmarshal([]byte(payload), &reconciler); err != nil {
		t.Fatalf("parse reconciler config: %v", err)
	}
	wantBackoffice := testCanonicalReleaseSecret(2)
	wantWalletAuthorizer := testCanonicalReleaseSecret(12)
	if reconciler.ClientSecret != testCanonicalReleaseSecret(1) {
		t.Fatalf("reconciler secret did not come from release inputs")
	}
	if reconciler.ClientCredentials["noebs-backoffice"].ClientSecret != wantBackoffice {
		t.Fatalf("back-office secret did not come from release inputs")
	}
	if reconciler.ClientCredentials["noebs-wallet-authorizer"].ClientSecret != wantWalletAuthorizer {
		t.Fatalf("wallet authorizer secret did not come from release inputs")
	}
	apiSecret := readYAMLMapFileMust(t, filepath.Join(outputRoot, "secrets", "api-gateway.secrets.yaml"))
	apiNoebs := getMap(apiSecret, "noebs")
	if got := firstString(apiNoebs, "backoffice_client_secret"); got != wantBackoffice {
		t.Fatalf("api-gateway back-office secret = %q", got)
	}
	if got := firstString(apiNoebs, "wallet_authorizer_client_secret"); got != wantWalletAuthorizer {
		t.Fatalf("api-gateway wallet authorizer secret = %q", got)
	}
	routes := getMap(apiNoebs, "psp_webhook_routes")
	if route := getMap(routes, testCanonicalReleaseSecret(11)); firstString(route, "tenant_id") != "tenant-cutover" || firstString(route, "provider_code") != "test-provider" {
		t.Fatalf("api-gateway PSP webhook routes = %#v", routes)
	}
	pspSecret := getMap(readYAMLMapFileMust(t, filepath.Join(outputRoot, "secrets", "psp-webhook.secrets.yaml")), "noebs")
	if _, present := pspSecret["psp_webhook_routes"]; present {
		t.Fatal("psp-webhook secret contains gateway callback routes")
	}
	provider := getMap(getMap(getMap(pspSecret, "psp"), "tenant-cutover"), "test-provider")
	if _, present := provider["callback_id"]; present {
		t.Fatal("PSP provider credential map contains public callback authority")
	}
	if err := validateKubernetesReleaseManifest(outputRoot); err != nil {
		t.Fatalf("validateKubernetesReleaseManifest() error = %v", err)
	}
}

func TestPrepareKubernetesReleaseRejectsDuplicatePSPCallbackID(t *testing.T) {
	inputRoot := t.TempDir()
	inputs := newTestKubernetesReleaseInputs(t, "tenant-cutover")
	inputs.Noebs.PSP["tenant-cutover"]["second-provider"] = pspSecret{
		CallbackID: testCanonicalReleaseSecret(11), APIKey: "key", APISecret: "secret", WebhookSecret: "webhook", WebhookPublicKey: "public",
	}
	inputsPath := writeKubernetesReleaseInputs(t, inputRoot, inputs)

	err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), filepath.Join(t.TempDir(), "release"), readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "callback IDs must be unique") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want duplicate callback rejection", err)
	}
}

func TestPrepareKubernetesReleaseRejectsInvalidPSPProviderCode(t *testing.T) {
	inputRoot := t.TempDir()
	inputs := newTestKubernetesReleaseInputs(t, "tenant-cutover")
	provider := inputs.Noebs.PSP["tenant-cutover"]["test-provider"]
	delete(inputs.Noebs.PSP["tenant-cutover"], "test-provider")
	inputs.Noebs.PSP["tenant-cutover"]["test_provider"] = provider
	inputsPath := writeKubernetesReleaseInputs(t, inputRoot, inputs)

	err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), filepath.Join(t.TempDir(), "release"), readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "PSP provider") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want canonical provider rejection", err)
	}
}

func TestPrepareKubernetesReleaseValidatesEveryPSPProvider(t *testing.T) {
	inputRoot := t.TempDir()
	inputs := newTestKubernetesReleaseInputs(t, "tenant-cutover")
	inputs.Noebs.PSP["tenant-cutover"]["a-provider"] = pspSecret{
		CallbackID:       testCanonicalReleaseSecret(13),
		APIKey:           "api-key",
		APISecret:        "api-secret",
		WebhookSecret:    "webhook-secret",
		WebhookPublicKey: "webhook-public-key",
	}
	inputs.Noebs.PSP["tenant-cutover"]["z-provider"] = pspSecret{
		CallbackID:    testCanonicalReleaseSecret(14),
		APIKey:        "api-key",
		APISecret:     "api-secret",
		WebhookSecret: "webhook-secret",
	}
	inputsPath := writeKubernetesReleaseInputs(t, inputRoot, inputs)

	err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), filepath.Join(t.TempDir(), "release"), readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "noebs.psp.tenant-cutover.z-provider missing webhook_public_key") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want incomplete second PSP provider rejection", err)
	}
}

func TestPrepareKubernetesReleaseValidatesNonDefaultTenantPSPProvider(t *testing.T) {
	inputRoot := t.TempDir()
	inputs := newTestKubernetesReleaseInputs(t, "tenant-cutover")
	inputs.Noebs.PSP["tenant-sandbox"] = map[string]pspSecret{
		"sandbox-provider": {
			CallbackID:       testCanonicalReleaseSecret(13),
			APIKey:           "api-key",
			WebhookSecret:    "webhook-secret",
			WebhookPublicKey: "webhook-public-key",
		},
	}
	inputsPath := writeKubernetesReleaseInputs(t, inputRoot, inputs)

	err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), filepath.Join(t.TempDir(), "release"), readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "noebs.psp.tenant-sandbox.sandbox-provider missing api_secret") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want incomplete non-default-tenant PSP provider rejection", err)
	}
}

func TestPrepareKubernetesReleaseRejectsUnknownTenant(t *testing.T) {
	inputRoot := t.TempDir()
	inputsPath := writeKubernetesReleaseInputsFile(t, inputRoot, "tenant-unknown")
	outputRoot := filepath.Join(t.TempDir(), "release")

	err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "unknown tenant") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want unknown tenant", err)
	}
	assertPathMissing(t, outputRoot)
}

func TestPrepareKubernetesReleaseRejectsNoncanonicalTenant(t *testing.T) {
	inputRoot := t.TempDir()
	inputsPath := writeKubernetesReleaseInputsFile(t, inputRoot, " tenant-cutover ")
	outputRoot := filepath.Join(t.TempDir(), "release")

	err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "invalid tenant") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want noncanonical tenant rejection", err)
	}
	assertPathMissing(t, outputRoot)
}

func TestPrepareKubernetesReleaseRejectsMissingExplicitAuthority(t *testing.T) {
	tests := []struct {
		name string
		edit func(*kubernetesReleaseInputs)
		want string
	}{
		{
			name: "service database password",
			edit: func(inputs *kubernetesReleaseInputs) {
				delete(inputs.Noebs.ServiceDatabasePasswords, "identity_auth_runtime")
			},
			want: "identity_auth_runtime password is required",
		},
		{
			name: "keycloak client secret",
			edit: func(inputs *kubernetesReleaseInputs) { inputs.Noebs.Keycloak.ReconcilerClientSecret = "" },
			want: "Keycloak reconciler client secret",
		},
		{
			name: "wallet authorizer client secret",
			edit: func(inputs *kubernetesReleaseInputs) { inputs.Noebs.Keycloak.WalletAuthorizerClientSecret = "" },
			want: "Keycloak wallet authorizer client secret",
		},
		{
			name: "gateway database password",
			edit: func(inputs *kubernetesReleaseInputs) { inputs.Noebs.GatewayAuth.Database.RuntimePassword = "" },
			want: "gateway authentication runtime database password",
		},
		{
			name: "workload signing key",
			edit: func(inputs *kubernetesReleaseInputs) {
				delete(inputs.Noebs.WorkloadAuth.Callers, string(serviceRoleAPIGateway))
			},
			want: "workload_auth.callers.api-gateway",
		},
		{
			name: "transport CA",
			edit: func(inputs *kubernetesReleaseInputs) { inputs.Noebs.InternalTransport.CAPrivateKey = "" },
			want: "internal_transport.ca_certificate and internal_transport.ca_private_key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputRoot := t.TempDir()
			inputs := newTestKubernetesReleaseInputs(t, "tenant-cutover")
			tt.edit(&inputs)
			inputsPath := writeKubernetesReleaseInputs(t, inputRoot, inputs)
			outputRoot := filepath.Join(t.TempDir(), "release")

			err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("prepareKubernetesRelease() error = %v, want %q", err, tt.want)
			}
			assertPathMissing(t, outputRoot)
		})
	}
}

func TestPrepareKubernetesReleaseRejectsNonEmptyOutputRoot(t *testing.T) {
	inputRoot := t.TempDir()
	inputsPath := writeKubernetesReleaseInputsFile(t, inputRoot, "tenant-cutover")
	outputRoot := t.TempDir()
	writePreflightFile(t, outputRoot, "stale", "do not overwrite")

	err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt)
	if err == nil || !strings.Contains(err.Error(), "output root must be empty") {
		t.Fatalf("prepareKubernetesRelease() error = %v, want non-empty output rejection", err)
	}
}

func TestKubernetesReleaseInputsExampleMatchesStrictSchema(t *testing.T) {
	path := filepath.Join("..", "deploy", "kubernetes", "overlays", "current-host", "kubernetes-release.inputs.yaml.example")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read input example: %v", err)
	}
	var inputs kubernetesReleaseInputs
	decoder := yaml.NewDecoder(bytes.NewReader(payload))
	decoder.KnownFields(true)
	if err := decoder.Decode(&inputs); err != nil {
		t.Fatalf("decode input example: %v", err)
	}
	for label, value := range map[string]string{
		"keycloak.reconciler_client_secret":         inputs.Noebs.Keycloak.ReconcilerClientSecret,
		"keycloak.backoffice_client_secret":         inputs.Noebs.Keycloak.BackofficeClientSecret,
		"keycloak.wallet_authorizer_client_secret":  inputs.Noebs.Keycloak.WalletAuthorizerClientSecret,
		"keycloak.temporal_ledger_client_secret":    inputs.Noebs.Keycloak.TemporalLedgerClientSecret,
		"keycloak.temporal_worker_client_secret":    inputs.Noebs.Keycloak.TemporalWorkerClientSecret,
		"keycloak.temporal_bootstrap_client_secret": inputs.Noebs.Keycloak.TemporalBootstrapClientSecret,
		"gateway_auth.database.runtime":             inputs.Noebs.GatewayAuth.Database.RuntimePassword,
		"gateway_auth.encryption_key_id":            inputs.Noebs.GatewayAuth.EncryptionKeyID,
		"workload_auth.database.runtime":            inputs.Noebs.WorkloadAuth.Database.RuntimePassword,
		"internal_transport.ca_certificate":         inputs.Noebs.InternalTransport.CACertificate,
		"internal_transport.ca_private_key":         inputs.Noebs.InternalTransport.CAPrivateKey,
	} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("input example missing %s", label)
		}
	}
	for _, role := range servicePostgresRoleNames() {
		if strings.TrimSpace(inputs.Noebs.ServiceDatabasePasswords[role]) == "" {
			t.Fatalf("input example missing service database password %s", role)
		}
	}
	for _, role := range workloadAuthCallerRoles {
		caller := inputs.Noebs.WorkloadAuth.Callers[string(role)]
		if caller.KeyID == "" || caller.PrivateKey == "" {
			t.Fatalf("input example missing workload caller %s", role)
		}
	}
	provider := inputs.Noebs.PSP["REPLACE_WITH_TENANT_ID"]["REPLACE_WITH_PROVIDER_CODE"]
	if provider.CallbackID == "" {
		t.Fatal("input example missing PSP callback_id")
	}
}

func TestKubernetesReleaseManifestRejectsSplicedArtifact(t *testing.T) {
	inputRoot := t.TempDir()
	inputsPath := writeKubernetesReleaseInputsFile(t, inputRoot, "tenant-cutover")
	outputRoot := filepath.Join(t.TempDir(), "release")
	if err := prepareKubernetesRelease("..", inputsPath, kubernetesReleaseTestAgeKeyPath(inputRoot), outputRoot, readPlainPreflightSecret, plainKubernetesSecretEncrypt); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(outputRoot, "platform", "postgres-provisioning.sql")
	if err := os.WriteFile(path, []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateKubernetesReleaseManifest(outputRoot); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("validateKubernetesReleaseManifest() error = %v, want fingerprint mismatch", err)
	}
}

func TestEncryptSopsYAMLScrubsAmbientEnvironment(t *testing.T) {
	tmp := t.TempDir()
	fakeSOPS := filepath.Join(tmp, "sops")
	if err := os.WriteFile(fakeSOPS, []byte(`#!/bin/sh
printf 'age_key=%s\n' "${SOPS_AGE_KEY_FILE-unset}"
printf 'ambient=%s\n' "${AMBIENT_SECRET-unset}"
printf 'args=%s\n' "$*"
`), 0o700); err != nil {
		t.Fatalf("write fake sops: %v", err)
	}
	ageKeyFile := filepath.Join(tmp, "age-key.txt")
	if err := os.WriteFile(ageKeyFile, []byte("# public key: age1testrecipient\nAGE-SECRET-KEY-1LOCAL\n"), 0o600); err != nil {
		t.Fatalf("write age key: %v", err)
	}
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SOPS_AGE_KEY_FILE", "/ambient/key.txt")
	t.Setenv("AMBIENT_SECRET", "must-not-leak")

	encrypted, err := encryptSopsYAML("test-secret", []byte("noebs:\n  secret: value\n"), ageKeyFile)
	if err != nil {
		t.Fatalf("encryptSopsYAML() error = %v", err)
	}
	text := string(encrypted)
	if !strings.Contains(text, "age_key=unset\n") || !strings.Contains(text, "ambient=unset\n") {
		t.Fatalf("encryptSopsYAML output retained ambient environment: %q", text)
	}
	if strings.Contains(text, "must-not-leak") || strings.Contains(text, "/ambient/key.txt") {
		t.Fatalf("encryptSopsYAML output leaked ambient environment: %q", text)
	}
}

func writeKubernetesReleaseInputsFile(t *testing.T, root, tenantID string) string {
	t.Helper()
	return writeKubernetesReleaseInputs(t, root, newTestKubernetesReleaseInputs(t, tenantID))
}

func writeKubernetesReleaseInputs(t *testing.T, root string, inputs kubernetesReleaseInputs) string {
	t.Helper()
	payload, err := yaml.Marshal(inputs)
	if err != nil {
		t.Fatalf("marshal Kubernetes release inputs: %v", err)
	}
	path := filepath.Join(root, "kubernetes-release.inputs.yaml")
	writePreflightFile(t, root, filepath.Base(path), string(payload))
	writePreflightFile(t, root, "age-key.txt", "# public key: age1testrecipient\nAGE-SECRET-KEY-1LOCAL\n")
	return path
}

func newTestKubernetesReleaseInputs(t *testing.T, tenantID string) kubernetesReleaseInputs {
	t.Helper()
	workload, err := prepareWorkloadAuthRelease(kubernetesReleaseWorkloadAuthInputs{Database: kubernetesReleaseWorkloadDatabaseInput{
		MigratePassword: testCanonicalReleaseSecret(6),
		RuntimePassword: testCanonicalReleaseSecret(7),
		CleanupPassword: testCanonicalReleaseSecret(8),
	}}, rand.Reader)
	if err != nil {
		t.Fatalf("prepare workload authority: %v", err)
	}
	callers := make(map[string]kubernetesReleaseWorkloadCallerInput, len(workload.callers))
	for role, caller := range workload.callers {
		callers[string(role)] = kubernetesReleaseWorkloadCallerInput{KeyID: caller.keyID, PrivateKey: caller.privateKey}
	}
	internalTransport := newTestInternalTransportInputs(t, time.Now().UTC())
	serviceDatabasePasswords := make(map[string]string, len(servicePostgresRoleSpecs))
	for index, spec := range servicePostgresRoleSpecs {
		serviceDatabasePasswords[spec.username] = testCanonicalReleaseSecret(byte(20 + index))
	}
	return kubernetesReleaseInputs{Noebs: kubernetesReleaseNoebsInputs{
		DefaultTenantID:          tenantID,
		ServiceDatabasePasswords: serviceDatabasePasswords,
		GoogleClientID:           "google-client-id",
		GoogleClientSecret:       "google-client-secret",
		CardVaultDataKey:         "card-vault-data-key",
		TemporalPostgresPassword: "temporal-postgres-password",
		KeycloakPostgresPassword: "keycloak-postgres-password",
		GHCRDockerConfigJSON:     `{"auths":{"ghcr.io":{"auth":"` + base64.StdEncoding.EncodeToString([]byte("noebs:test-token")) + `"}}}`,
		Keycloak: kubernetesReleaseKeycloakInputs{
			ReconcilerClientSecret:        testCanonicalReleaseSecret(1),
			BackofficeClientSecret:        testCanonicalReleaseSecret(2),
			WalletAuthorizerClientSecret:  testCanonicalReleaseSecret(12),
			TemporalLedgerClientSecret:    testCanonicalReleaseSecret(13),
			TemporalWorkerClientSecret:    testCanonicalReleaseSecret(14),
			TemporalBootstrapClientSecret: testCanonicalReleaseSecret(15),
		},
		GatewayAuth: kubernetesReleaseGatewayAuthInputs{
			Database: kubernetesReleaseGatewayAuthDatabaseInputs{
				MigratePassword: testCanonicalReleaseSecret(3),
				RuntimePassword: testCanonicalReleaseSecret(4),
				CleanupPassword: testCanonicalReleaseSecret(5),
			},
			EncryptionKeyID: "gateway-key-1",
			EncryptionKeys: map[string]string{
				"gateway-key-1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32)),
			},
		},
		WorkloadAuth: kubernetesReleaseWorkloadAuthInputs{
			Callers: callers,
			Database: kubernetesReleaseWorkloadDatabaseInput{
				MigratePassword: workload.database.migratePassword,
				RuntimePassword: workload.database.runtimePassword,
				CleanupPassword: workload.database.cleanupPassword,
			},
		},
		InternalTransport: internalTransport,
		EBS: kubernetesReleaseEBSInputs{
			ConsumerEndpoint: "https://consumer.input.example", MerchantEndpoint: "https://merchant.input.example",
			IPINEndpoint: "https://ipin.input.example", ConsumerAppID: "consumer-app", MerchantAppID: "merchant-app",
			IPINUsername: "ipin-user", IPINPassword: "ipin-password", PublicKey: "consumer-public-key",
			IPINKey: "ipin-public-key", PAN: "1234567890123456", PIN: "1234", IPIN: "123456", Expiry: "0129",
		},
		PSP: map[string]map[string]pspSecret{
			tenantID: {
				"test-provider": {CallbackID: testCanonicalReleaseSecret(11), APIKey: "psp-api-key", APISecret: "psp-api-secret", WebhookSecret: "psp-webhook-secret", WebhookPublicKey: "psp-webhook-public-key"},
			},
		},
	}}
}

func testCanonicalReleaseSecret(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

func kubernetesReleaseTestAgeKeyPath(root string) string {
	return filepath.Join(root, "age-key.txt")
}

func plainKubernetesSecretEncrypt(_ string, payload []byte, _ string) ([]byte, error) {
	return payload, nil
}

func readPreparedFile(t *testing.T, root, name string) string {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(root, name), err)
	}
	return string(payload)
}

func requirePreparedFile(t *testing.T, root, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatalf("stat %s: %v", filepath.Join(root, name), err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s error = %v, want not exist", path, err)
	}
}

func readYAMLMapFileMust(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	result, err := readYAMLMapFile(path)
	if err != nil {
		t.Fatalf("readYAMLMapFile(%s): %v", path, err)
	}
	return result
}
