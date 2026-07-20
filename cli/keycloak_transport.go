package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

var errInvalidKeycloakTransport = errors.New("invalid Keycloak transport configuration")

func keycloakClientTLSConfig(caPEM string) (*tls.Config, error) {
	block, rest := pem.Decode([]byte(strings.TrimSpace(caPEM)))
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("%w: CA certificate", errInvalidKeycloakTransport)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !certificate.IsCA {
		return nil, fmt.Errorf("%w: CA certificate", errInvalidKeycloakTransport)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    roots,
	}, nil
}

func readKeycloakClientTLSConfig(path string) (*tls.Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("%w: CA path", errInvalidKeycloakTransport)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Keycloak transport CA: %w", err)
	}
	return keycloakClientTLSConfig(string(payload))
}

func requireHTTPSKeycloakEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%w: HTTPS endpoint", errInvalidKeycloakTransport)
	}
	return nil
}
