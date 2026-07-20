package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/httpclient"
)

var (
	internalTransportClientTLS *tls.Config
	internalTransportServerTLS *tls.Config
)

func validateInternalTransportRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	secureDiscovery := false
	plainDiscovery := false
	for _, target := range expectedHTTPServiceDiscoveryKeys() {
		endpoint, err := url.Parse(strings.TrimSpace(cfg.ServiceDiscovery[target]))
		if err != nil {
			return fmt.Errorf("noebs.service_discovery.%s: %w", target, err)
		}
		switch endpoint.Scheme {
		case "https":
			secureDiscovery = true
		case "http":
			plainDiscovery = true
		}
	}
	if secureDiscovery && plainDiscovery {
		return errors.New("Noebs internal service discovery must not mix HTTP and HTTPS")
	}
	present := cfg.InternalTransport.Present()
	if !roleUsesInternalTransportIdentity(role) {
		if present {
			return errors.New("noebs.internal_transport is not allowed for this role")
		}
		return nil
	}
	if !present {
		return errors.New("noebs.internal_transport is required")
	}
	if !secureDiscovery || plainDiscovery {
		return errors.New("noebs.internal_transport requires HTTPS service discovery")
	}
	if _, err := cfg.InternalTransport.ClientTLSConfig(string(role)); err != nil {
		return fmt.Errorf("noebs.internal_transport client: %w", err)
	}
	if role.startsHTTP() || role == serviceRoleWalletLedger {
		if _, err := cfg.InternalTransport.ServerTLSConfig(string(role), internalTransportServerPeers(role)...); err != nil {
			return fmt.Errorf("noebs.internal_transport server: %w", err)
		}
	}
	return nil
}

func initInternalTransport(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	internalTransportClientTLS = nil
	internalTransportServerTLS = nil
	if !cfg.InternalTransport.Present() {
		return nil
	}
	clientTLS, err := cfg.InternalTransport.ClientTLSConfig(string(role))
	if err != nil {
		return err
	}
	internalTransportClientTLS = clientTLS
	if role.startsHTTP() || role == serviceRoleWalletLedger {
		serverTLS, err := cfg.InternalTransport.ServerTLSConfig(string(role), internalTransportServerPeers(role)...)
		if err != nil {
			return err
		}
		internalTransportServerTLS = serverTLS
	}
	return nil
}

func internalTransportServerPeers(role serviceRole) []string {
	if role == serviceRoleWalletLedger {
		return []string{string(serviceRoleWalletAPI)}
	}
	if !role.startsHTTP() {
		return nil
	}
	if role == serviceRoleAPIGateway {
		return []string{"edge", string(serviceRoleAPIGateway)}
	}
	callers := expectedWorkloadCallers(role)
	peers := make([]string, 0, len(callers)+1)
	for caller := range callers {
		peers = append(peers, caller)
	}
	// HTTP readiness probes connect over loopback with the receiver's own
	// workload identity.
	peers = append(peers, string(role))
	sort.Strings(peers)
	return peers
}

func roleUsesInternalTransportIdentity(role serviceRole) bool {
	return role.startsHTTP() || role == serviceRoleWalletLedger
}

func newInternalHTTPClient(options ...httpclient.Option) *http.Client {
	if internalTransportClientTLS != nil {
		options = append(options, httpclient.WithTLSConfig(internalTransportClientTLS))
	}
	return httpclient.New(options...)
}
