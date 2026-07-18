package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
)

var (
	errMissingServiceDiscoveryEntry    = errors.New("missing service discovery entry")
	errUnexpectedServiceDiscoveryEntry = errors.New("unexpected service discovery entry")
	errInvalidServiceDiscoveryEntry    = errors.New("invalid service discovery entry")
)

func validateServiceDiscoveryCatalog(label string, cfg ebs_fields.NoebsConfig) error {
	if err := validateExactHTTPServiceDiscoveryCatalog(cfg.ServiceDiscovery); err != nil {
		return fmt.Errorf("%s service discovery: %w", label, err)
	}
	if err := validateExactGRPCServiceDiscoveryCatalog(cfg.GRPCServiceDiscovery); err != nil {
		return fmt.Errorf("%s grpc service discovery: %w", label, err)
	}
	return nil
}

func validateExactHTTPServiceDiscoveryCatalog(discovery map[string]string) error {
	expected := expectedHTTPServiceDiscoveryKeys()
	for _, key := range expected {
		endpoint := strings.TrimSpace(discovery[key])
		if endpoint == "" {
			return fmt.Errorf("%w: noebs.service_discovery.%s", errMissingServiceDiscoveryEntry, key)
		}
		if err := validateHTTPServiceDiscoveryEndpoint(key, endpoint); err != nil {
			return err
		}
	}
	return rejectUnexpectedServiceDiscoveryKeys("noebs.service_discovery", discovery, expected)
}

func validateExactGRPCServiceDiscoveryCatalog(discovery map[string]string) error {
	expected := expectedGRPCServiceDiscoveryKeys()
	for _, key := range expected {
		endpoint := strings.TrimSpace(discovery[key])
		if endpoint == "" {
			return fmt.Errorf("%w: noebs.grpc_service_discovery.%s", errMissingServiceDiscoveryEntry, key)
		}
		if err := validateHostPortServiceDiscoveryEndpoint("noebs.grpc_service_discovery."+key, endpoint); err != nil {
			return err
		}
	}
	return rejectUnexpectedServiceDiscoveryKeys("noebs.grpc_service_discovery", discovery, expected)
}

func expectedHTTPServiceDiscoveryKeys() []string {
	return []string{
		string(serviceRoleIdentityAuth),
		string(serviceRoleCardVault),
		string(serviceRoleEBSAdapter),
		string(serviceRolePSPWebhook),
		string(serviceRoleAdminReporting),
		string(serviceRoleNotification),
		string(serviceRoleBeneficiary),
		string(serviceRoleWalletAPI),
	}
}

func expectedGRPCServiceDiscoveryKeys() []string {
	return []string{
		string(serviceRoleWalletLedger),
	}
}

func rejectUnexpectedServiceDiscoveryKeys(label string, discovery map[string]string, expected []string) error {
	expectedKeys := make(map[string]bool, len(expected))
	for _, key := range expected {
		expectedKeys[key] = true
	}
	for key := range discovery {
		if !expectedKeys[key] {
			return fmt.Errorf("%w: %s.%s", errUnexpectedServiceDiscoveryEntry, label, key)
		}
	}
	return nil
}

func validateHTTPServiceDiscoveryEndpoint(key, endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%w: parse noebs.service_discovery.%s: %w", errInvalidServiceDiscoveryEntry, key, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: noebs.service_discovery.%s must use http or https", errInvalidServiceDiscoveryEntry, key)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: noebs.service_discovery.%s missing host", errInvalidServiceDiscoveryEntry, key)
	}
	if parsed.Port() == "" {
		return fmt.Errorf("%w: noebs.service_discovery.%s missing port", errInvalidServiceDiscoveryEntry, key)
	}
	if _, err := strconv.Atoi(parsed.Port()); err != nil {
		return fmt.Errorf("%w: noebs.service_discovery.%s port: %w", errInvalidServiceDiscoveryEntry, key, err)
	}
	return nil
}

func validateHostPortServiceDiscoveryEndpoint(label, endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", errInvalidServiceDiscoveryEntry, label, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%w: %s missing host", errInvalidServiceDiscoveryEntry, label)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("%w: %s port: %w", errInvalidServiceDiscoveryEntry, label, err)
	}
	return nil
}
