package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/workloadauth"
)

func TestSessionValidatorsDoNotSendUnsignedRequests(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	cfg := ebs_fields.NoebsConfig{
		ServiceDiscovery: map[string]string{string(serviceRoleIdentityAuth): server.URL},
	}
	if _, err := newIdentitySessionValidator(cfg, nil); !errors.Is(err, workloadauth.ErrMissingSigner) {
		t.Fatalf("identity constructor error = %v", err)
	}
	if _, err := newChatSessionValidator(cfg, nil); !errors.Is(err, workloadauth.ErrMissingSigner) {
		t.Fatalf("chat constructor error = %v", err)
	}

	identity := &identitySessionValidator{endpoint: server.URL, client: server.Client()}
	err := identity.ValidateSession(context.Background(), "tenant_1", 42, 1)
	if !errors.Is(err, gateway.ErrSessionValidation) || !errors.Is(err, workloadauth.ErrMissingSigner) {
		t.Fatalf("identity validation error = %v", err)
	}
	chat := &chatSessionValidator{endpoint: server.URL, client: server.Client()}
	err = chat.ValidateSession(context.Background(), chatGatewayIdentity{
		UserIdentity: gateway.UserIdentity{
			TenantID:     "tenant_1",
			UserID:       42,
			SessionEpoch: 1,
		},
		Token: "session-token",
	})
	if !errors.Is(err, gateway.ErrSessionValidation) || !errors.Is(err, workloadauth.ErrMissingSigner) {
		t.Fatalf("chat validation error = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("unsigned upstream hits = %d", hits.Load())
	}
}
