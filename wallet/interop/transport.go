package interop

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

var ErrTransportConfig = errors.New("invalid interop transport configuration")

// TransportConfig describes the external SDK and the private callback listener.
// Construct it with NewTransportConfig; there are no implicit local endpoints.
type TransportConfig struct {
	sdkOutboundURL       string
	sdkInboundURL        string
	backendListenAddress string
	backendAllowedPeers  []netip.Addr
	valid                bool
}

func NewTransportConfig(outboundURL, inboundURL, listenAddress string, allowedPeers []string) (TransportConfig, error) {
	for _, endpoint := range []struct{ name, value string }{{"interop_sdk_outbound_url", outboundURL}, {"interop_sdk_inbound_url", inboundURL}} {
		name, value := endpoint.name, endpoint.value
		parsed, err := url.Parse(value)
		if err != nil || value == "" || strings.TrimSpace(value) != value ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" ||
			strings.HasSuffix(parsed.Host, ":") {
			return TransportConfig{}, fmt.Errorf("%w: %s must be an explicit HTTP(S) origin without credentials, path, query, or fragment", ErrTransportConfig, name)
		}
		if port := parsed.Port(); port != "" && !validPort(port) {
			return TransportConfig{}, fmt.Errorf("%w: %s has an invalid port", ErrTransportConfig, name)
		}
	}
	host, port, err := net.SplitHostPort(listenAddress)
	address, addressErr := netip.ParseAddr(host)
	if err != nil || addressErr != nil || address.String() != host || !validPort(port) ||
		(!address.IsUnspecified() && !privatePeer(address)) {
		return TransportConfig{}, fmt.Errorf("%w: interop_backend_listen_address requires a private or unspecified literal IP and port", ErrTransportConfig)
	}
	if len(allowedPeers) == 0 {
		return TransportConfig{}, fmt.Errorf("%w: interop_backend_allowed_peers requires explicit private peer IPs", ErrTransportConfig)
	}
	peers := make([]netip.Addr, 0, len(allowedPeers))
	seen := make(map[netip.Addr]bool, len(allowedPeers))
	for _, value := range allowedPeers {
		peer, err := netip.ParseAddr(value)
		if err != nil || peer.String() != value || !privatePeer(peer) || seen[peer] {
			return TransportConfig{}, fmt.Errorf("%w: interop_backend_allowed_peers requires unique canonical private IPs", ErrTransportConfig)
		}
		seen[peer] = true
		peers = append(peers, peer)
	}
	return TransportConfig{sdkOutboundURL: outboundURL, sdkInboundURL: inboundURL, backendListenAddress: listenAddress, backendAllowedPeers: peers, valid: true}, nil
}

func validPort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && port > 0 && port <= 65535 && strconv.Itoa(port) == value
}

func privatePeer(address netip.Addr) bool {
	return address.Zone() == "" && !address.Is4In6() &&
		(address.IsLoopback() || address.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(address))
}

func (c TransportConfig) acceptsPeer(remoteAddress string) bool {
	peer, err := netip.ParseAddrPort(remoteAddress)
	if err != nil || !c.valid {
		return false
	}
	for _, allowed := range c.backendAllowedPeers {
		if peer.Addr().Unmap() == allowed {
			return true
		}
	}
	return false
}
