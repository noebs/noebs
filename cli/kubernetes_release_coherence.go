package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/keycloakadmin"
	"github.com/adonese/noebs/internal/tenantcatalog"
)

type kubernetesReleaseServiceConfig struct {
	role  serviceRole
	noebs map[string]interface{}
	value ebs_fields.NoebsConfig
}

func validateKubernetesReleaseCoherence(root string, configMap map[string]interface{}, ageKeyPath string, decrypt deploymentDecryptFunc) error {
	catalog, err := tenantcatalog.LoadFile(filepath.Join(root, "tenant-catalog.yaml"))
	if err != nil {
		return err
	}
	postgresPassword, err := readRequiredSecretValue("Noebs Postgres password", filepath.Join(root, "platform", "postgres-password.txt"))
	if err != nil {
		return err
	}
	keycloakPostgresPassword, err := readRequiredSecretValue("Keycloak Postgres password", filepath.Join(root, "platform", "keycloak-postgres-password.txt"))
	if err != nil {
		return err
	}
	keycloakValues, err := readKeycloakConfigValues(filepath.Join(root, "platform", "keycloak.conf"))
	if err != nil {
		return err
	}
	if keycloakValues["db-password"] != keycloakPostgresPassword {
		return errors.New("keycloak config database password does not match the release credential")
	}
	reconciler, err := readSteadyKeycloakReconcilerConfig(filepath.Join(root, "platform", "keycloak-reconciler-config.yaml"))
	if err != nil {
		return err
	}
	if reconciler.BaseURL != "https://keycloak.noebs.svc.cluster.local:8443/auth" {
		return errors.New("steady Keycloak reconciler base_url must target the release Keycloak service")
	}
	if reconciler.ClientSecret != reconciler.ClientCredentials["noebs-keycloak-reconciler"].ClientSecret {
		return errors.New("steady Keycloak reconciler client secret does not match its managed credential")
	}
	gatewayDatabase, err := readGatewayAuthDatabaseCredentials(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	workloadDatabase, err := readWorkloadAuthDatabaseCredentials(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}
	transport, err := readInternalTransportPlatformCredentials(root, ageKeyPath, decrypt)
	if err != nil {
		return err
	}

	services := make(map[serviceRole]kubernetesReleaseServiceConfig, len(kubernetesSecretReleaseServiceNames))
	defaultTenantID := ""
	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		service, err := readKubernetesReleaseServiceConfig(root, configMap, serviceName, ageKeyPath, decrypt)
		if err != nil {
			return err
		}
		services[service.role] = service
		if _, err := catalog.Require(service.value.DefaultTenantID); err != nil {
			return fmt.Errorf("%s default_tenant_id: %w", serviceName, err)
		}
		if defaultTenantID == "" {
			defaultTenantID = service.value.DefaultTenantID
		} else if service.value.DefaultTenantID != defaultTenantID {
			return fmt.Errorf("%s default_tenant_id does not match the release default tenant", serviceName)
		}
		for tenantID := range getMap(service.noebs, "psp") {
			if _, err := catalog.Require(tenantID); err != nil {
				return fmt.Errorf("%s PSP tenant %q: %w", serviceName, tenantID, err)
			}
		}
		if ca := strings.TrimSpace(service.value.DatabaseCACertificate); ca != "" && ca != transport.CACertificate {
			return fmt.Errorf("%s database CA does not match the release transport CA", serviceName)
		}
		if ca := strings.TrimSpace(service.value.InternalTransport.CACertificate); ca != "" && ca != transport.CACertificate {
			return fmt.Errorf("%s internal transport CA does not match the release transport CA", serviceName)
		}
		expectedDatabaseURL, present := coherentServiceDatabaseURL(service.role, postgresPassword, gatewayDatabase, workloadDatabase)
		if present && service.value.DatabaseURL != expectedDatabaseURL {
			return fmt.Errorf("%s database URL does not match its release role credential", serviceName)
		}
		if roleReceivesSignedHTTP(service.role) {
			expected := workloadAuthDatabaseURL("workload_auth_runtime", workloadDatabase.runtimePassword)
			if service.value.WorkloadAuth.NonceDatabaseURL != expected {
				return fmt.Errorf("%s workload nonce database URL does not match the runtime role credential", serviceName)
			}
		}
	}

	apiGateway := services[serviceRoleAPIGateway].value
	if strings.TrimSpace(apiGateway.KeycloakCACertificate) != transport.CACertificate {
		return errors.New("api-gateway Keycloak CA does not match the release transport CA")
	}
	if apiGateway.BackofficeClientSecret != reconciler.ClientCredentials["noebs-backoffice"].ClientSecret {
		return errors.New("api-gateway back-office client secret does not match the Keycloak managed credential")
	}
	if apiGateway.OIDC.Issuer != "https://api.noebs.sd/auth/realms/noebs" ||
		apiGateway.OIDC.JWKSURL != "https://keycloak.noebs.svc.cluster.local:8443/auth/realms/noebs/protocol/openid-connect/certs" {
		return errors.New("api-gateway OIDC endpoints do not match the release Keycloak boundary")
	}
	if err := validateReleasePSPWebhookProjection(catalog, apiGateway, services[serviceRolePSPWebhook].noebs, services[serviceRoleWalletWorker].noebs); err != nil {
		return err
	}
	return validateReleaseWorkloadKeyProjection(services)
}

func validateReleasePSPWebhookProjection(catalog tenantcatalog.Catalog, apiGateway ebs_fields.NoebsConfig, pspWebhook, walletWorker map[string]interface{}) error {
	credentialPairs := make(map[string]struct{})
	for tenantID, rawProviders := range getMap(pspWebhook, "psp") {
		providers, ok := rawProviders.(map[string]interface{})
		if !ok {
			return fmt.Errorf("psp-webhook PSP tenant %s must contain providers", tenantID)
		}
		walletProviders := getMap(getMap(walletWorker, "psp"), tenantID)
		if len(walletProviders) != len(providers) {
			return fmt.Errorf("wallet-worker PSP providers do not match psp-webhook for tenant %s", tenantID)
		}
		for providerCode, providerCredential := range providers {
			walletCredential, present := walletProviders[providerCode]
			if !present || !reflect.DeepEqual(walletCredential, providerCredential) {
				return fmt.Errorf("wallet-worker PSP providers do not match psp-webhook for tenant %s", tenantID)
			}
			credentialPairs[tenantID+"\x00"+providerCode] = struct{}{}
		}
	}
	if len(apiGateway.PSPWebhookRoutes) != len(credentialPairs) {
		return errors.New("api-gateway PSP callback routes do not match release provider credentials")
	}
	for _, route := range apiGateway.PSPWebhookRoutes {
		if _, err := catalog.Require(route.TenantID); err != nil {
			return fmt.Errorf("api-gateway PSP callback tenant: %w", err)
		}
		pair := route.TenantID + "\x00" + route.ProviderCode
		if _, present := credentialPairs[pair]; !present {
			return fmt.Errorf("api-gateway PSP callback route %s/%s has no release provider credential", route.TenantID, route.ProviderCode)
		}
		delete(credentialPairs, pair)
	}
	if len(credentialPairs) != 0 {
		return errors.New("release PSP provider credential has no api-gateway callback route")
	}
	return nil
}

func readKubernetesReleaseServiceConfig(root string, configMap map[string]interface{}, serviceName, ageKeyPath string, decrypt deploymentDecryptFunc) (kubernetesReleaseServiceConfig, error) {
	servicePath := filepath.Join(root, "services", serviceName+".yaml")
	serviceMap, err := readYAMLMapFile(servicePath)
	if err != nil {
		return kubernetesReleaseServiceConfig{}, err
	}
	merged := mergeConfig(configMap, serviceMap).(map[string]interface{})
	secretPath := filepath.Join(root, "secrets", serviceSecretFileName(serviceName))
	payload, err := decrypt(secretPath, ageKeyPath)
	if err != nil {
		return kubernetesReleaseServiceConfig{}, fmt.Errorf("decrypt %s secrets: %w", serviceName, err)
	}
	secretMap, err := parseYAMLMap(secretPath, payload)
	if err != nil {
		return kubernetesReleaseServiceConfig{}, err
	}
	merged = mergeConfig(merged, secretMap).(map[string]interface{})
	noebs := getMap(merged, "noebs")
	if err := applyServiceDatabaseURL(noebs); err != nil {
		return kubernetesReleaseServiceConfig{}, fmt.Errorf("%s service database config: %w", serviceName, err)
	}
	role, err := parseServiceRole(firstString(noebs, "service_role"))
	if err != nil {
		return kubernetesReleaseServiceConfig{}, fmt.Errorf("%s service_role: %w", serviceName, err)
	}
	encoded, err := json.Marshal(noebs)
	if err != nil {
		return kubernetesReleaseServiceConfig{}, fmt.Errorf("%s encode merged config: %w", serviceName, err)
	}
	var value ebs_fields.NoebsConfig
	if err := json.Unmarshal(encoded, &value); err != nil {
		return kubernetesReleaseServiceConfig{}, fmt.Errorf("%s decode merged config: %w", serviceName, err)
	}
	return kubernetesReleaseServiceConfig{role: role, noebs: noebs, value: value}, nil
}

func coherentServiceDatabaseURL(role serviceRole, postgresPassword string, gateway preparedGatewayAuthRelease, workload preparedWorkloadDatabase) (string, bool) {
	switch role {
	case serviceRoleAPIGateway:
		return gatewayAuthDatabaseURL("gateway_auth_runtime", gateway.runtimePassword), true
	case serviceRoleGatewayAuthMigrate:
		return gatewayAuthDatabaseURL("gateway_auth_migrate", gateway.migratePassword), true
	case serviceRoleGatewayAuthCleanup:
		return gatewayAuthDatabaseURL("gateway_auth_cleanup", gateway.cleanupPassword), true
	case serviceRoleWorkloadAuthMigrate:
		return workloadAuthDatabaseURL("workload_auth_migrate", workload.migratePassword), true
	case serviceRoleWorkloadAuthCleanup:
		return workloadAuthDatabaseURL("workload_auth_cleanup", workload.cleanupPassword), true
	}
	owner, present := role.databaseOwnerRole()
	if !present {
		return "", false
	}
	databaseName := strings.ReplaceAll(string(owner), "-", "_")
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("noebs", postgresPassword),
		Host:     "postgres:5432",
		Path:     "/" + databaseName,
		RawQuery: "sslmode=verify-full",
	}).String(), true
}

func validateReleaseWorkloadKeyProjection(services map[serviceRole]kubernetesReleaseServiceConfig) error {
	type signer struct {
		keyID     string
		publicKey string
	}
	signers := make(map[string]signer, len(workloadAuthCallerRoles))
	for _, role := range workloadAuthCallerRoles {
		keyID, privateKey, present, err := services[role].value.WorkloadAuth.SigningKey()
		if err != nil || !present {
			return fmt.Errorf("%s workload signing authority is invalid", role)
		}
		publicKey := privateKey.Public().(ed25519.PublicKey)
		signers[string(role)] = signer{keyID: keyID, publicKey: base64.StdEncoding.EncodeToString(publicKey)}
	}
	for role, service := range services {
		expected := expectedWorkloadCallers(role)
		if !roleReceivesSignedHTTP(role) {
			continue
		}
		if len(service.value.WorkloadAuth.TrustedKeys) != len(expected) {
			return fmt.Errorf("%s workload trusted key set does not match release callers", role)
		}
		for caller := range expected {
			signer := signers[caller]
			trusted, present := service.value.WorkloadAuth.TrustedKeys[signer.keyID]
			if !present || trusted.Caller != caller || trusted.PublicKey != signer.publicKey {
				return fmt.Errorf("%s workload key for caller %s does not match its release signing authority", role, caller)
			}
		}
	}
	return nil
}

func readSteadyKeycloakReconcilerConfig(path string) (keycloakadmin.Config, error) {
	if err := requireReadableFile("Keycloak reconciler config", path); err != nil {
		return keycloakadmin.Config{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return keycloakadmin.Config{}, fmt.Errorf("open Keycloak reconciler config: %w", err)
	}
	config, err := keycloakadmin.LoadConfig(file)
	if closeErr := file.Close(); closeErr != nil && err == nil {
		return keycloakadmin.Config{}, fmt.Errorf("close Keycloak reconciler config: %w", closeErr)
	}
	if err != nil {
		return keycloakadmin.Config{}, err
	}
	if config.AdminRealm != "noebs" || config.ClientID != "noebs-keycloak-reconciler" {
		return keycloakadmin.Config{}, errors.New("steady Keycloak reconciler config must use the noebs realm-local client")
	}
	return config, nil
}
