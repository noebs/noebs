package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"gopkg.in/yaml.v3"
)

func TestValidateServiceDiscoveryCatalogAcceptsExactCatalog(t *testing.T) {
	if err := validateServiceDiscoveryCatalog("api-gateway", exactServiceDiscoveryConfig()); err != nil {
		t.Fatalf("validateServiceDiscoveryCatalog() error = %v", err)
	}
}

func TestValidateServiceDiscoveryCatalogRejectsMissingHTTPEntry(t *testing.T) {
	cfg := exactServiceDiscoveryConfig()
	delete(cfg.ServiceDiscovery, string(serviceRoleCardVault))

	err := validateServiceDiscoveryCatalog("api-gateway", cfg)
	if !errors.Is(err, errMissingServiceDiscoveryEntry) {
		t.Fatalf("validateServiceDiscoveryCatalog() error = %v, want %v", err, errMissingServiceDiscoveryEntry)
	}
}

func TestValidateServiceDiscoveryCatalogRejectsUnexpectedHTTPEntry(t *testing.T) {
	cfg := exactServiceDiscoveryConfig()
	cfg.ServiceDiscovery["monolith"] = "http://monolith:8080"

	err := validateServiceDiscoveryCatalog("api-gateway", cfg)
	if !errors.Is(err, errUnexpectedServiceDiscoveryEntry) {
		t.Fatalf("validateServiceDiscoveryCatalog() error = %v, want %v", err, errUnexpectedServiceDiscoveryEntry)
	}
}

func TestValidateServiceDiscoveryCatalogRejectsInvalidHTTPEntry(t *testing.T) {
	cfg := exactServiceDiscoveryConfig()
	cfg.ServiceDiscovery[string(serviceRoleEBSAdapter)] = "ebs-adapter:8080"

	err := validateServiceDiscoveryCatalog("api-gateway", cfg)
	if !errors.Is(err, errInvalidServiceDiscoveryEntry) {
		t.Fatalf("validateServiceDiscoveryCatalog() error = %v, want %v", err, errInvalidServiceDiscoveryEntry)
	}
}

func TestValidateServiceDiscoveryCatalogRejectsMissingGRPCEntry(t *testing.T) {
	cfg := exactServiceDiscoveryConfig()
	delete(cfg.GRPCServiceDiscovery, string(serviceRoleWalletLedger))

	err := validateServiceDiscoveryCatalog("api-gateway", cfg)
	if !errors.Is(err, errMissingServiceDiscoveryEntry) {
		t.Fatalf("validateServiceDiscoveryCatalog() error = %v, want %v", err, errMissingServiceDiscoveryEntry)
	}
}

func TestValidateServiceDiscoveryCatalogRejectsUnexpectedGRPCEntry(t *testing.T) {
	cfg := exactServiceDiscoveryConfig()
	cfg.GRPCServiceDiscovery["wallet-worker"] = "wallet-worker:9090"

	err := validateServiceDiscoveryCatalog("api-gateway", cfg)
	if !errors.Is(err, errUnexpectedServiceDiscoveryEntry) {
		t.Fatalf("validateServiceDiscoveryCatalog() error = %v, want %v", err, errUnexpectedServiceDiscoveryEntry)
	}
}

func TestValidateDeploymentRootRejectsUnexpectedServiceDiscoveryEntry(t *testing.T) {
	root := writePreflightRoot(t, preflightRootOptions{})
	rewritePreflightNoebsConfig(t, root, "config.docker.yaml", func(noebs map[string]interface{}) {
		getMap(noebs, "service_discovery")["monolith"] = "http://monolith:8080"
	})

	err := validateDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if !errors.Is(err, errUnexpectedServiceDiscoveryEntry) {
		t.Fatalf("validateDeploymentRootWithDecrypt() error = %v, want %v", err, errUnexpectedServiceDiscoveryEntry)
	}
}

func TestValidateKubernetesDeploymentRootRejectsMissingGRPCServiceDiscoveryEntry(t *testing.T) {
	root := writeRenderedKubernetesPreflightRoot(t)
	rewritePreflightNoebsConfig(t, root, "config.yaml", func(noebs map[string]interface{}) {
		delete(getMap(noebs, "grpc_service_discovery"), string(serviceRoleWalletLedger))
	})

	err := validateKubernetesDeploymentRootWithDecrypt(root, readPlainPreflightSecret)
	if !errors.Is(err, errMissingServiceDiscoveryEntry) {
		t.Fatalf("validateKubernetesDeploymentRootWithDecrypt() error = %v, want %v", err, errMissingServiceDiscoveryEntry)
	}
}

func exactServiceDiscoveryConfig() ebs_fields.NoebsConfig {
	return ebs_fields.NoebsConfig{
		ServiceDiscovery: map[string]string{
			string(serviceRoleIdentityAuth):   "http://identity-auth:8080",
			string(serviceRoleCardVault):      "http://card-vault:8080",
			string(serviceRoleEBSAdapter):     "http://ebs-adapter:8080",
			string(serviceRolePSPWebhook):     "http://psp-webhook:8080",
			string(serviceRoleAdminReporting): "http://admin-reporting:8080",
			string(serviceRoleNotification):   "http://notification-chat:8080",
			string(serviceRoleWalletAPI):      "http://wallet-api:8080",
		},
		GRPCServiceDiscovery: map[string]string{
			string(serviceRoleWalletLedger): "wallet-ledger:9090",
		},
	}
}

func rewritePreflightNoebsConfig(t *testing.T, root, name string, mutate func(map[string]interface{})) {
	t.Helper()
	path := filepath.Join(root, name)
	config, err := readYAMLMapFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	noebs := getMap(config, "noebs")
	if noebs == nil {
		t.Fatalf("%s missing noebs config", path)
	}
	mutate(noebs)
	payload, err := yaml.Marshal(config)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	writePreflightFile(t, root, name, string(payload))
}
