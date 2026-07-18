// Package transportauth builds the mutually authenticated TLS boundary used
// by Noebs HTTP services inside the cluster.
package transportauth

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidConfiguration = errors.New("invalid internal transport configuration")

type Config struct {
	CACertificate string `json:"ca_certificate"`
	Certificate   string `json:"certificate"`
	PrivateKey    string `json:"private_key"`
}

func (c Config) Present() bool {
	return strings.TrimSpace(c.CACertificate) != "" || strings.TrimSpace(c.Certificate) != "" || strings.TrimSpace(c.PrivateKey) != ""
}

func (c Config) ClientTLSConfig(identity string) (*tls.Config, error) {
	certificate, roots, _, err := c.material(identity)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
	}, nil
}

func (c Config) ServerTLSConfig(identity string) (*tls.Config, error) {
	certificate, roots, _, err := c.material(identity)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    roots,
	}, nil
}

func (c Config) material(identity string) (tls.Certificate, *x509.CertPool, *x509.Certificate, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" || !c.Present() || strings.TrimSpace(c.CACertificate) == "" || strings.TrimSpace(c.Certificate) == "" || strings.TrimSpace(c.PrivateKey) == "" {
		return tls.Certificate{}, nil, nil, ErrInvalidConfiguration
	}
	certificate, err := tls.X509KeyPair([]byte(c.Certificate), []byte(c.PrivateKey))
	if err != nil || len(certificate.Certificate) == 0 {
		return tls.Certificate{}, nil, nil, fmt.Errorf("%w: service certificate", ErrInvalidConfiguration)
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("%w: service certificate", ErrInvalidConfiguration)
	}
	if err := leaf.VerifyHostname(identity); err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("%w: certificate identity %q", ErrInvalidConfiguration, identity)
	}
	caBlock, rest := pem.Decode([]byte(c.CACertificate))
	if caBlock == nil || caBlock.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return tls.Certificate{}, nil, nil, fmt.Errorf("%w: CA certificate", ErrInvalidConfiguration)
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil || !ca.IsCA {
		return tls.Certificate{}, nil, nil, fmt.Errorf("%w: CA certificate", ErrInvalidConfiguration)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     roots,
		DNSName:   identity,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("%w: certificate chain", ErrInvalidConfiguration)
	}
	certificate.Leaf = leaf
	return certificate, roots, leaf, nil
}
