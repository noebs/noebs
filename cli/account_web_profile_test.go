package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
)

func TestAccountWebProfileUsesSignedConfiguredPrincipal(t *testing.T) {
	fixture := newAccountWebFixture(t)
	principal, err := tenantauth.Authorize(fixture.session.claims, "tenant-a", tenantauth.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	verifier := newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleAPIGateway))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/consumer/auth/profile" {
			t.Errorf("incorrect profile endpoint %s %s", r.Method, r.URL.Path)
		}
		if _, err := verifier.Verify(r, body); err != nil {
			t.Errorf("unsigned profile request: %v", err)
		}
		for name, expected := range map[string]string{workloadauth.HeaderTenantID: "tenant-a", workloadauth.HeaderSubject: "customer", workloadauth.HeaderAuthorizedParty: accountWebClientID, workloadauth.HeaderRoles: "user", workloadauth.HeaderSourceIP: "192.0.2.1"} {
			if r.Header.Get(name) != expected {
				t.Errorf("incorrect principal %s", name)
			}
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get(workloadauth.HeaderUserID) != "" {
			t.Error("credentials or invented user ID forwarded")
		}
		var payload map[string]string
		if json.Unmarshal(body, &payload) != nil || len(payload) != 1 || payload["fullname"] != "Confirmed Name" {
			t.Errorf("incorrect profile input %s", body)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	client := &accountWebProfileClient{&identityProfileProjectionResolver{endpoint: server.URL, client: server.Client(), signers: newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleIdentityAuth))}}
	if err := client.Create(context.Background(), principal, "Confirmed Name", "request", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
}

func TestAccountWebRuntimeRejectsMissingOrAmbiguousTenantAndCallbacks(t *testing.T) {
	cfg := cliTestConfig(serviceRoleAPIGateway, unavailableCLIDatabaseURL)
	cfg.WebTenantID = "tenant-a"
	cfg.WebClientSecret = "test-web-secret"
	cfg.WebRedirectURL = "https://app.example/account/oauth/callback"
	cfg.WebPostLogoutURL = "https://app.example/account/oauth/logout/callback"
	if err := validateAccountWebRuntimeConfig(cfg); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"tenant", "noncanonical tenant", "reserved tenant", "client secret", "redirect", "logout origin"} {
		t.Run(field, func(t *testing.T) {
			bad := cfg
			switch field {
			case "tenant":
				bad.WebTenantID = ""
			case "noncanonical tenant":
				bad.WebTenantID = "Tenant-A"
			case "reserved tenant":
				bad.WebTenantID = "default"
			case "client secret":
				bad.WebClientSecret = ""
			case "redirect":
				bad.WebRedirectURL = "https://app.example/mobile/oauth/callback"
			case "logout origin":
				bad.WebPostLogoutURL = "https://evil.example/account/oauth/logout/callback"
			}
			if err := validateAccountWebRuntimeConfig(bad); err == nil {
				t.Fatal("invalid web runtime accepted")
			}
		})
	}
}
