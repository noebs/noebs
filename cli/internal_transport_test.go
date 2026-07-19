package main

import (
	"bytes"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/transportauth"
	"gopkg.in/yaml.v3"
)

func TestInternalTransportSigningCAKeyIsInputOnly(t *testing.T) {
	now := time.Now().UTC()
	_, caKey, caCertificate, err := prepareInternalTransportCA(kubernetesReleaseInternalTransportInputs{}, rand.Reader, now)
	if err != nil {
		t.Fatal(err)
	}
	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPrivateKey := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyDER})

	prepared, err := prepareInternalTransportRelease(kubernetesReleaseInternalTransportInputs{
		CACertificate: caCertificate,
		CAPrivateKey:  string(caPrivateKey),
	}, rand.Reader, now)
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
		"ca_certificate":       true,
		"postgres_certificate": true,
		"postgres_private_key": true,
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

func TestGeneratedInternalTransportPerformsMutualTLS(t *testing.T) {
	prepared, err := prepareInternalTransportRelease(kubernetesReleaseInternalTransportInputs{}, rand.Reader, time.Now().UTC())
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
	response.Body.Close()
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
		response.Body.Close()
		t.Fatal("wallet-ledger accepted a CA-issued certificate for identity-auth")
	}

	unauthenticatedTLS := clientTLS.Clone()
	unauthenticatedTLS.Certificates = nil
	unauthenticated := &http.Client{Transport: &http.Transport{TLSClientConfig: unauthenticatedTLS}}
	if response, err := unauthenticated.Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("server accepted a client without a workload certificate")
	}
}

func TestWalletLedgerTransportAllowsOnlyWalletAPI(t *testing.T) {
	peers := internalTransportServerPeers(serviceRoleWalletLedger)
	if len(peers) != 1 || peers[0] != string(serviceRoleWalletAPI) {
		t.Fatalf("wallet-ledger peers = %v, want [wallet-api]", peers)
	}
}

func TestInternalTransportServerRequiresPeerAllowlist(t *testing.T) {
	prepared, err := prepareInternalTransportRelease(kubernetesReleaseInternalTransportInputs{}, rand.Reader, time.Now().UTC())
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
