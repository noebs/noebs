package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/transportauth"
	"gopkg.in/yaml.v3"
)

func TestInternalTransportSigningCAKeyIsInputOnly(t *testing.T) {
	now := time.Now().UTC()
	inputs := newTestInternalTransportInputs(t, now)
	caPrivateKey := []byte(inputs.CAPrivateKey)

	prepared, err := prepareInternalTransportRelease(inputs, rand.Reader, now)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := yaml.Marshal(prepared.platformSecret())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, caPrivateKey) || bytes.Contains(payload, []byte("ca_private_key")) {
		t.Fatalf("rendered platform credentials contain the signing CA private key")
	}

	var values map[string]interface{}
	if err := yaml.Unmarshal(payload, &values); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"ca_certificate":                true,
		"postgres_certificate":          true,
		"postgres_private_key":          true,
		"keycloak_postgres_certificate": true,
		"keycloak_postgres_private_key": true,
		"keycloak_certificate":          true,
		"keycloak_private_key":          true,
	}
	if len(values) != len(want) {
		t.Fatalf("platform credential keys = %v, want %v", values, want)
	}
	for key := range values {
		if !want[key] {
			t.Fatalf("unexpected platform credential key %q", key)
		}
	}
}

func TestGeneratedInternalTransportHasDistinctPlatformIdentities(t *testing.T) {
	now := time.Now().UTC()
	prepared, err := prepareInternalTransportRelease(newTestInternalTransportInputs(t, now), rand.Reader, now)
	if err != nil {
		t.Fatal(err)
	}
	postgres := prepared.platform["postgres"]
	keycloakPostgres := prepared.platform["keycloak-postgres"]
	keycloak := prepared.platform["keycloak"]
	if err := postgres.ValidateIdentity("postgres"); err != nil {
		t.Fatalf("validate Postgres identity: %v", err)
	}
	if err := keycloakPostgres.ValidateIdentity("keycloak-postgres"); err != nil {
		t.Fatalf("validate Keycloak Postgres identity: %v", err)
	}
	if err := keycloak.ValidateIdentity("keycloak"); err != nil {
		t.Fatalf("validate Keycloak identity: %v", err)
	}
	if postgres.Certificate == keycloakPostgres.Certificate || postgres.PrivateKey == keycloakPostgres.PrivateKey ||
		keycloak.Certificate == keycloakPostgres.Certificate || keycloak.PrivateKey == keycloakPostgres.PrivateKey {
		t.Fatal("Postgres platform identities share certificate material")
	}
	if err := postgres.ValidateIdentity("keycloak-postgres"); err == nil {
		t.Fatal("Noebs Postgres certificate is valid for Keycloak Postgres")
	}
}

func TestGeneratedInternalTransportPerformsMutualTLS(t *testing.T) {
	now := time.Now().UTC()
	prepared, err := prepareInternalTransportRelease(newTestInternalTransportInputs(t, now), rand.Reader, now)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := prepared.services[serviceRoleWalletLedger].ServerTLSConfig(
		string(serviceRoleWalletLedger),
		internalTransportServerPeers(serviceRoleWalletLedger)...,
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()

	clientTLS, err := prepared.services[serviceRoleWalletAPI].ClientTLSConfig(string(serviceRoleWalletAPI))
	if err != nil {
		t.Fatal(err)
	}
	clientTLS.ServerName = string(serviceRoleWalletLedger)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("mTLS request failed: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close mTLS response body: %v", err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("mTLS response = %s, want 204", response.Status)
	}

	otherServiceTLS, err := prepared.services[serviceRoleIdentityAuth].ClientTLSConfig(string(serviceRoleIdentityAuth))
	if err != nil {
		t.Fatal(err)
	}
	otherServiceTLS.ServerName = string(serviceRoleWalletLedger)
	otherService := &http.Client{Transport: &http.Transport{TLSClientConfig: otherServiceTLS}}
	if response, err := otherService.Get(server.URL); err == nil {
		if err := response.Body.Close(); err != nil {
			t.Fatalf("close unexpected mTLS response body: %v", err)
		}
		t.Fatal("wallet-ledger accepted a CA-issued certificate for identity-auth")
	}

	unauthenticatedTLS := clientTLS.Clone()
	unauthenticatedTLS.Certificates = nil
	unauthenticated := &http.Client{Transport: &http.Transport{TLSClientConfig: unauthenticatedTLS}}
	if response, err := unauthenticated.Get(server.URL); err == nil {
		if err := response.Body.Close(); err != nil {
			t.Fatalf("close unexpected unauthenticated response body: %v", err)
		}
		t.Fatal("server accepted a client without a workload certificate")
	}
}

func newTestInternalTransportInputs(t *testing.T, now time.Time) kubernetesReleaseInternalTransportInputs {
	t.Helper()
	inputs, err := generateTestInternalTransportInputs(now)
	if err != nil {
		t.Fatalf("generate test transport CA: %v", err)
	}
	return inputs
}

func generateTestInternalTransportInputs(now time.Time) (kubernetesReleaseInternalTransportInputs, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return kubernetesReleaseInternalTransportInputs{}, err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Noebs test transport CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return kubernetesReleaseInternalTransportInputs{}, err
	}
	privateKeyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return kubernetesReleaseInternalTransportInputs{}, err
	}
	return kubernetesReleaseInternalTransportInputs{
		CACertificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})),
		CAPrivateKey:  string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKeyDER})),
	}, nil
}

func TestWalletLedgerTransportAllowsOnlyWalletAPI(t *testing.T) {
	peers := internalTransportServerPeers(serviceRoleWalletLedger)
	if len(peers) != 1 || peers[0] != string(serviceRoleWalletAPI) {
		t.Fatalf("wallet-ledger peers = %v, want [wallet-api]", peers)
	}
}

func TestInternalTransportServerRequiresPeerAllowlist(t *testing.T) {
	now := time.Now().UTC()
	prepared, err := prepareInternalTransportRelease(newTestInternalTransportInputs(t, now), rand.Reader, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepared.services[serviceRoleWalletLedger].ServerTLSConfig(string(serviceRoleWalletLedger))
	if !errors.Is(err, transportauth.ErrInvalidConfiguration) {
		t.Fatalf("empty server peer allowlist error = %v, want ErrInvalidConfiguration", err)
	}
}

func TestWorkloadSigningPrivateKeysAreIsolatedPerCaller(t *testing.T) {
	prepared, err := prepareWorkloadAuthRelease(kubernetesReleaseWorkloadAuthInputs{}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range workloadAuthCallerRoles {
		payload, err := yaml.Marshal(prepared.configForRole(role))
		if err != nil {
			t.Fatal(err)
		}
		for otherRole, caller := range prepared.callers {
			contains := bytes.Contains(payload, []byte(caller.privateKey))
			if otherRole == role && !contains {
				t.Fatalf("%s config is missing its signing private key", role)
			}
			if otherRole != role && contains {
				t.Fatalf("%s config contains %s signing private key", role, otherRole)
			}
		}
	}

	for _, role := range []serviceRole{
		serviceRoleEBSAdapterEvents,
		serviceRoleAdminReportingProjector,
		serviceRoleWalletWorker,
		serviceRoleIdentityAuthMigrate,
		serviceRoleWorkloadAuthMigrate,
		serviceRoleWorkloadAuthCleanup,
	} {
		payload, err := yaml.Marshal(prepared.configForRole(role))
		if err != nil {
			t.Fatal(err)
		}
		for callerRole, caller := range prepared.callers {
			if bytes.Contains(payload, []byte(caller.privateKey)) {
				t.Fatalf("%s config contains %s signing private key", role, callerRole)
			}
		}
	}
}
