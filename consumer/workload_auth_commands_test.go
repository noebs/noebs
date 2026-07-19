package consumer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/workloadauth"
)

func TestInternalServiceCommandsCarryVerifiedWorkloadIdentity(t *testing.T) {
	ebsPublic, ebsPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identityPublic, identityPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	registry := workloadauth.Registry{
		"ebs-adapter-test":   {Caller: "ebs-adapter", PublicKey: ebsPublic},
		"identity-auth-test": {Caller: "identity-auth", PublicKey: identityPublic},
	}
	verifiers := make(map[string]*workloadauth.Verifier)
	for _, audience := range []string{cardVaultServiceDiscoveryKey, notificationServiceDiscoveryKey} {
		verifier, err := workloadauth.NewVerifier(audience, registry, workloadauth.SystemClock{}, commandNonceStore{})
		if err != nil {
			t.Fatal(err)
		}
		verifiers[audience] = verifier
	}

	type expectedCommand struct {
		audience string
		caller   string
	}
	expected := map[string]expectedCommand{
		"/internal/card-vault/funded-operations/claim": {audience: cardVaultServiceDiscoveryKey, caller: "ebs-adapter"},
		"/internal/notification-chat/biller-hook":      {audience: notificationServiceDiscoveryKey, caller: "ebs-adapter"},
		"/internal/card-vault/cards/masked":            {audience: cardVaultServiceDiscoveryKey, caller: "identity-auth"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		command, ok := expected[r.URL.Path]
		if !ok {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		principal, err := verifiers[command.audience].Verify(r, body)
		if err != nil || principal.Caller != command.caller {
			t.Errorf("verify %s: principal=%+v error=%v", r.URL.Path, principal, err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get(gateway.GatewayTenantIDHeader) != "tenant_1" ||
			r.Header.Get(gateway.GatewayAdminIdentityHeader) != "" ||
			r.Header.Get(gateway.GatewayAdminRoleHeader) != "" ||
			r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected command identity headers: %v", r.Header)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	discovery := map[string]string{
		cardVaultServiceDiscoveryKey:    server.URL,
		notificationServiceDiscoveryKey: server.URL,
	}
	ebs := &Service{
		HTTPClient:  server.Client(),
		NoebsConfig: ebs_fields.NoebsConfig{ServiceDiscovery: discovery},
		WorkloadSigners: commandSignerSet(t, "ebs-adapter-test", ebsPrivate,
			cardVaultServiceDiscoveryKey, notificationServiceDiscoveryKey),
	}
	identity := &Service{
		HTTPClient:  server.Client(),
		NoebsConfig: ebs_fields.NoebsConfig{ServiceDiscovery: discovery},
		WorkloadSigners: commandSignerSet(t, "identity-auth-test", identityPrivate,
			cardVaultServiceDiscoveryKey),
	}

	commands := []func() error{
		func() error {
			return ebs.doAdminServiceCommand(context.Background(), "tenant_1", cardVaultCommandTarget, "/internal/card-vault/funded-operations/claim", map[string]string{"operation_id": "operation-1"}, nil)
		},
		func() error {
			return ebs.doAdminServiceCommand(context.Background(), "tenant_1", notificationCommandTarget, "/internal/notification-chat/biller-hook", map[string]string{"event": "paid"}, nil)
		},
		func() error {
			return identity.doCardVaultCommand(context.Background(), "tenant_1", 42, "/internal/card-vault/cards/masked", struct{}{}, nil)
		},
	}
	for index, command := range commands {
		if err := command(); err != nil {
			t.Fatalf("command %d: %v", index, err)
		}
	}
}

func commandSignerSet(t *testing.T, keyID string, privateKey ed25519.PrivateKey, audiences ...string) *workloadauth.SignerSet {
	t.Helper()
	signers, err := workloadauth.NewSignerSet(workloadauth.Config{
		SigningKeyID:      keyID,
		SigningPrivateKey: base64.StdEncoding.EncodeToString(privateKey),
	}, audiences)
	if err != nil {
		t.Fatal(err)
	}
	return signers
}

func testEBSWorkloadSigners(t *testing.T) *workloadauth.SignerSet {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return commandSignerSet(t, "ebs-adapter-test", privateKey,
		cardVaultServiceDiscoveryKey, notificationServiceDiscoveryKey)
}

type commandNonceStore struct{}

func (commandNonceStore) Use(context.Context, string, string, string, time.Time) (bool, error) {
	return true, nil
}

func TestInternalServiceCommandDoesNotSendWithoutSigner(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	service := &Service{
		HTTPClient: server.Client(),
		NoebsConfig: ebs_fields.NoebsConfig{ServiceDiscovery: map[string]string{
			notificationServiceDiscoveryKey: server.URL,
		}},
	}
	err := service.doAdminServiceCommand(context.Background(), "tenant_1", notificationCommandTarget, "/internal/notification-chat/biller-hook", struct{}{}, nil)
	if !errors.Is(err, workloadauth.ErrMissingSigner) {
		t.Fatalf("error = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("unsigned upstream hits = %d", hits.Load())
	}
}
