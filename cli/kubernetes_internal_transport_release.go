package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/adonese/noebs/internal/transportauth"
)

type kubernetesReleaseInternalTransportInputs struct {
	CACertificate string `yaml:"ca_certificate"`
	CAPrivateKey  string `yaml:"ca_private_key"`
}

type preparedInternalTransportRelease struct {
	caCertificate string
	services      map[serviceRole]transportauth.Config
	platform      map[string]transportauth.Config
}

func prepareInternalTransportRelease(inputs kubernetesReleaseInternalTransportInputs, random io.Reader, now time.Time) (preparedInternalTransportRelease, error) {
	if random == nil || now.IsZero() {
		return preparedInternalTransportRelease{}, errors.New("internal transport generation inputs are required")
	}
	ca, caKey, caPEM, err := prepareInternalTransportCA(inputs, random, now.UTC())
	if err != nil {
		return preparedInternalTransportRelease{}, err
	}
	prepared := preparedInternalTransportRelease{
		caCertificate: caPEM,
		services:      map[serviceRole]transportauth.Config{},
		platform:      map[string]transportauth.Config{},
	}
	for _, role := range internalTransportServiceRoles() {
		certificate, privateKey, err := generateInternalTransportIdentity(role, ca, caKey, random, now.UTC())
		if err != nil {
			return preparedInternalTransportRelease{}, err
		}
		config := transportauth.Config{
			CACertificate: caPEM,
			Certificate:   certificate,
			PrivateKey:    privateKey,
		}
		if _, err := config.ClientTLSConfig(string(role)); err != nil {
			return preparedInternalTransportRelease{}, fmt.Errorf("validate internal transport identity %s: %w", role, err)
		}
		prepared.services[role] = config
	}
	for _, identity := range []string{"postgres"} {
		certificate, privateKey, err := generateInternalTransportIdentity(serviceRole(identity), ca, caKey, random, now.UTC())
		if err != nil {
			return preparedInternalTransportRelease{}, err
		}
		config := transportauth.Config{CACertificate: caPEM, Certificate: certificate, PrivateKey: privateKey}
		if err := config.ValidateIdentity(identity); err != nil {
			return preparedInternalTransportRelease{}, fmt.Errorf("validate internal transport identity %s: %w", identity, err)
		}
		prepared.platform[identity] = config
	}
	return prepared, nil
}

func internalTransportServiceRoles() []serviceRole {
	return []serviceRole{
		serviceRoleAPIGateway,
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
		serviceRoleWalletAPI,
		serviceRoleWalletLedger,
	}
}

func prepareInternalTransportCA(inputs kubernetesReleaseInternalTransportInputs, random io.Reader, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, string, error) {
	certificatePEM := strings.TrimSpace(inputs.CACertificate)
	privateKeyPEM := strings.TrimSpace(inputs.CAPrivateKey)
	if (certificatePEM == "") != (privateKeyPEM == "") {
		return nil, nil, "", errors.New("internal transport requires both ca_certificate and ca_private_key")
	}
	if certificatePEM != "" {
		return parseInternalTransportCA(certificatePEM, privateKeyPEM)
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		return nil, nil, "", fmt.Errorf("generate internal transport CA key: %w", err)
	}
	serial, err := randomCertificateSerial(random)
	if err != nil {
		return nil, nil, "", err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Noebs internal transport CA"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(2, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(random, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, "", fmt.Errorf("create internal transport CA: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, "", fmt.Errorf("parse generated internal transport CA: %w", err)
	}
	return certificate, privateKey, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
}

func parseInternalTransportCA(certificatePEM, privateKeyPEM string) (*x509.Certificate, *ecdsa.PrivateKey, string, error) {
	certificateBlock, certificateRest := pem.Decode([]byte(certificatePEM))
	if certificateBlock == nil || certificateBlock.Type != "CERTIFICATE" || len(strings.TrimSpace(string(certificateRest))) != 0 {
		return nil, nil, "", errors.New("invalid internal transport CA certificate")
	}
	certificate, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil || !certificate.IsCA {
		return nil, nil, "", errors.New("invalid internal transport CA certificate")
	}
	keyBlock, keyRest := pem.Decode([]byte(privateKeyPEM))
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" || len(strings.TrimSpace(string(keyRest))) != 0 {
		return nil, nil, "", errors.New("invalid internal transport CA private key")
	}
	privateKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	publicKey, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	if err != nil || !ok || privateKey.Curve != elliptic.P256() || privateKey.PublicKey.X.Cmp(publicKey.X) != 0 || privateKey.PublicKey.Y.Cmp(publicKey.Y) != 0 {
		return nil, nil, "", errors.New("invalid internal transport CA private key")
	}
	return certificate, privateKey, string(pem.EncodeToMemory(certificateBlock)), nil
}

func generateInternalTransportIdentity(role serviceRole, ca *x509.Certificate, caKey *ecdsa.PrivateKey, random io.Reader, now time.Time) (string, string, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		return "", "", fmt.Errorf("generate internal transport key for %s: %w", role, err)
	}
	serial, err := randomCertificateSerial(random)
	if err != nil {
		return "", "", err
	}
	identity := string(role)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: identity},
		DNSNames: []string{
			identity,
			identity + ".noebs.svc",
			identity + ".noebs.svc.cluster.local",
		},
		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(random, template, ca, &privateKey.PublicKey, caKey)
	if err != nil {
		return "", "", fmt.Errorf("create internal transport certificate for %s: %w", role, err)
	}
	privateKeyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal internal transport key for %s: %w", role, err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKeyDER})), nil
}

func randomCertificateSerial(random io.Reader) (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(random, limit)
	if err != nil {
		return nil, fmt.Errorf("generate internal transport certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		return nil, errors.New("generated zero internal transport certificate serial")
	}
	return serial, nil
}

func (r preparedInternalTransportRelease) configForRole(role serviceRole) map[string]interface{} {
	config, ok := r.services[role]
	if !ok {
		return nil
	}
	return map[string]interface{}{
		"ca_certificate": config.CACertificate,
		"certificate":    config.Certificate,
		"private_key":    config.PrivateKey,
	}
}

func (r preparedInternalTransportRelease) platformConfig(identity string) (transportauth.Config, bool) {
	config, ok := r.platform[strings.TrimSpace(identity)]
	return config, ok
}

func (r preparedInternalTransportRelease) platformSecret() map[string]interface{} {
	postgres := r.platform["postgres"]
	return map[string]interface{}{
		"ca_certificate":       r.caCertificate,
		"postgres_certificate": postgres.Certificate,
		"postgres_private_key": postgres.PrivateKey,
	}
}
