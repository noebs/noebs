package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
)

func TestIdentityProfileProjectionResolverSignsTheVerifiedPrincipal(t *testing.T) {
	verifier := newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleAPIGateway))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/internal/identity-auth/principals/resolve" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := verifier.Verify(request, nil); err != nil {
			t.Errorf("verify signed projection request: %v", err)
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		wantHeaders := map[string]string{
			workloadauth.HeaderTenantID:        "tenant-a",
			workloadauth.HeaderIssuer:          "https://api.noebs.sd/auth/realms/noebs",
			workloadauth.HeaderSubject:         "subject-1",
			workloadauth.HeaderOrganizationID:  "organization-1",
			workloadauth.HeaderAuthorizedParty: "noebs-mobile",
			workloadauth.HeaderRoles:           "user",
			workloadauth.HeaderSourceIP:        "203.0.113.9",
		}
		for name, want := range wantHeaders {
			if got := request.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		if got := request.Header.Get(workloadauth.HeaderUserID); got != "" {
			t.Errorf("bootstrap user id = %q, want empty", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"user_id":42}`))
	}))
	defer server.Close()

	resolver := &identityProfileProjectionResolver{
		endpoint: server.URL,
		client:   server.Client(),
		signers:  newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleIdentityAuth)),
	}
	userID, err := resolver.Resolve(context.Background(), gatewayPrincipalForTest(t), "request-1", "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	if userID != 42 {
		t.Fatalf("user id = %d, want 42", userID)
	}
}

func TestIdentityProfileProjectionResolverDistinguishesMissingProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	resolver := &identityProfileProjectionResolver{
		endpoint: server.URL,
		client:   server.Client(),
		signers:  newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleIdentityAuth)),
	}
	_, err := resolver.Resolve(context.Background(), gatewayPrincipalForTest(t), "request-1", "203.0.113.9")
	if !errors.Is(err, errProfileProjectionNotFound) {
		t.Fatalf("resolve error = %v, want profile not found", err)
	}
}

func gatewayPrincipalForTest(t *testing.T) tenantauth.Principal {
	t.Helper()
	organization, err := tenantauth.NewOrganization("organization-1", []tenantauth.Role{tenantauth.RoleUser}, nil)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := tenantauth.NewClaims(tenantauth.Identity{
		Issuer:          "https://api.noebs.sd/auth/realms/noebs",
		Subject:         "subject-1",
		AuthorizedParty: "noebs-mobile",
		IssuedAt:        time.Now().Add(-time.Minute),
		ExpiresAt:       time.Now().Add(5 * time.Minute).Truncate(time.Second),
	}, map[string]tenantauth.Organization{"tenant-a": organization})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := tenantauth.SelectTenant(claims, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	return principal
}
