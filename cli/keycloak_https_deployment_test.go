package main

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestKeycloakApplicationTransportIsHTTPSOnly(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "deploy", "kubernetes", "base", "keycloak.conf.example"),
		filepath.Join("..", "deploy", "docker", "keycloak", "keycloak.conf.example"),
	} {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		config := string(payload)
		for _, required := range []string{
			"http-enabled=false\n",
			"https-port=8443\n",
			"https-certificate-file=/opt/keycloak/conf/tls.crt\n",
			"https-certificate-key-file=/opt/keycloak/conf/tls.key\n",
			"https-protocols=TLSv1.3\n",
			"http-management-scheme=http\n",
			"http-management-port=9000\n",
		} {
			if !strings.Contains(config, required) {
				t.Fatalf("%s missing %q", path, required)
			}
		}
		for _, forbidden := range []string{"http-enabled=true", "http-port=8080", "TLSv1.2"} {
			if strings.Contains(config, forbidden) {
				t.Fatalf("%s contains transport fallback %q", path, forbidden)
			}
		}
	}
}

func TestGeneratedKeycloakCertificateHasExactServiceDNSNames(t *testing.T) {
	now := time.Now().UTC()
	prepared, err := prepareInternalTransportRelease(newTestInternalTransportInputs(t, now), rand.Reader, now)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(prepared.platform["keycloak"].Certificate))
	if block == nil {
		t.Fatal("generated Keycloak certificate is not PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"keycloak", "keycloak.noebs.svc", "keycloak.noebs.svc.cluster.local"}
	if !slices.Equal(certificate.DNSNames, want) {
		t.Fatalf("Keycloak DNS SANs = %v, want %v", certificate.DNSNames, want)
	}
	if slices.Contains(certificate.DNSNames, "keycloak-postgres") {
		t.Fatal("Keycloak server certificate contains the database identity")
	}
}
