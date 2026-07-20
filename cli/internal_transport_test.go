package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
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
		"temporal_postgres_certificate": true,
		"temporal_postgres_private_key": true,
		"temporal_certificate":          true,
		"temporal_private_key":          true,
		"edge_certificate":              true,
		"edge_private_key":              true,
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
	for identity, config := range prepared.platform {
		if identity == edgeTransportIdentity {
			if err := validateClientCertificateIdentity(config.Certificate, edgeTransportIdentity); err != nil {
				t.Fatalf("validate exact edge client leaf: %v", err)
			}
			continue
		}
		if err := validateServerCertificateIdentity(
			config.Certificate,
			identity,
			identity+".noebs.svc",
			identity+".noebs.svc.cluster.local",
		); err != nil {
			t.Fatalf("validate exact %s server leaf: %v", identity, err)
		}
	}
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

func TestEdgeIdentityIsTheOnlyExternalGatewayPeer(t *testing.T) {
	now := time.Now().UTC()
	prepared, err := prepareInternalTransportRelease(newTestInternalTransportInputs(t, now), rand.Reader, now)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := prepared.services[serviceRoleAPIGateway].ServerTLSConfig(
		string(serviceRoleAPIGateway),
		internalTransportServerPeers(serviceRoleAPIGateway)...,
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

	edgeTLS, err := prepared.platform[edgeTransportIdentity].ClientTLSConfig(edgeTransportIdentity)
	if err != nil {
		t.Fatal(err)
	}
	edgeTLS.ServerName = "api-gateway.noebs.svc.cluster.local"
	edgeClient := &http.Client{Transport: &http.Transport{TLSClientConfig: edgeTLS}}
	response, err := edgeClient.Get(server.URL)
	if err != nil {
		t.Fatalf("edge mTLS request failed: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("edge mTLS response = %s, want 204", response.Status)
	}

	otherTLS, err := prepared.services[serviceRoleIdentityAuth].ClientTLSConfig(string(serviceRoleIdentityAuth))
	if err != nil {
		t.Fatal(err)
	}
	otherTLS.ServerName = "api-gateway.noebs.svc.cluster.local"
	other := &http.Client{Transport: &http.Transport{TLSClientConfig: otherTLS}}
	if response, err := other.Get(server.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("api-gateway accepted identity-auth as an edge peer")
	}

	tls12 := edgeTLS.Clone()
	tls12.MaxVersion = tls.VersionTLS12
	tls12.MinVersion = tls.VersionTLS12
	legacy := &http.Client{Transport: &http.Transport{TLSClientConfig: tls12}}
	if response, err := legacy.Get(server.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("api-gateway accepted TLS 1.2")
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

func TestHTTPRuntimeHasNoPlaintextTransportMode(t *testing.T) {
	now := time.Now().UTC()
	prepared, err := prepareInternalTransportRelease(newTestInternalTransportInputs(t, now), rand.Reader, now)
	if err != nil {
		t.Fatal(err)
	}
	discovery := make(map[string]string, len(expectedHTTPServiceDiscoveryKeys()))
	for _, service := range expectedHTTPServiceDiscoveryKeys() {
		discovery[service] = "http://" + service + ":8080"
	}
	cfg := ebs_fields.NoebsConfig{ServiceDiscovery: discovery}
	if err := validateInternalTransportRuntimeConfig(serviceRoleIdentityAuth, cfg); err == nil || !strings.Contains(err.Error(), "internal_transport is required") {
		t.Fatalf("missing transport error = %v", err)
	}
	cfg.InternalTransport = prepared.services[serviceRoleIdentityAuth]
	if err := validateInternalTransportRuntimeConfig(serviceRoleIdentityAuth, cfg); err == nil || !strings.Contains(err.Error(), "requires HTTPS") {
		t.Fatalf("plaintext discovery error = %v", err)
	}
	for service := range discovery {
		discovery[service] = "https://" + service + ":8080"
	}
	if err := validateInternalTransportRuntimeConfig(serviceRoleIdentityAuth, cfg); err != nil {
		t.Fatalf("exact TLS config rejected: %v", err)
	}
}

func TestAPIGatewayTransportAllowsOnlyEdgeAndSelf(t *testing.T) {
	peers := internalTransportServerPeers(serviceRoleAPIGateway)
	if !slices.Equal(peers, []string{edgeTransportIdentity, string(serviceRoleAPIGateway)}) {
		t.Fatalf("api-gateway peers = %v", peers)
	}
}

func TestWorkloadSigningPrivateKeysAreIsolatedPerCaller(t *testing.T) {
	prepared, err := prepareWorkloadAuthRelease(kubernetesReleaseWorkloadAuthInputs{
		Database: kubernetesReleaseWorkloadDatabaseInput{
			MigratePassword: testCanonicalReleaseSecret(1),
			RuntimePassword: testCanonicalReleaseSecret(2),
			CleanupPassword: testCanonicalReleaseSecret(3),
		},
	}, rand.Reader)
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
