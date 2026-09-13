package keycloakadmin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"testing"
)

func TestLocalAccountPolicyRequiresExplicitPasswordMinimum(t *testing.T) {
	for _, minimum := range []int{0, 1, 11, 129} {
		state := repositoryDesiredState(t)
		state.Authentication.LocalAccounts.MinimumPasswordLength = minimum
		if err := state.Validate(); !errors.Is(err, ErrInvalidDesiredState) {
			t.Fatalf("password minimum %d: got %v", minimum, err)
		}
		if state.Authentication.LocalAccounts.MinimumPasswordLength != minimum {
			t.Fatal("validation silently replaced the password policy")
		}
	}
	state := repositoryDesiredState(t)
	state.Authentication.LocalAccounts.VerifyEmail = false
	if err := state.Validate(); !errors.Is(err, ErrInvalidDesiredState) {
		t.Fatalf("email recovery without verification: got %v", err)
	}
}

func TestReconcileMissingAccountDependenciesFailsBeforeNetwork(t *testing.T) {
	for _, missing := range []string{"smtp", "provider", "extra provider"} {
		t.Run(missing, func(t *testing.T) {
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
			defer server.Close()
			config := validTestConfig(server.URL)
			switch missing {
			case "smtp":
				config.SMTP = nil
			case "provider":
				config.IdentityProviders = nil
			case "extra provider":
				config.IdentityProviders["unexpected"] = IdentityProviderCredential{ClientID: "id", ClientSecret: "secret"}
			}
			reconciler, err := New(config, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = reconciler.Reconcile(context.Background(), repositoryDesiredState(t))
			if !errors.Is(err, ErrInvalidConfig) || requests != 0 {
				t.Fatalf("missing dependency: err=%v requests=%d", err, requests)
			}
		})
	}
}

func TestLocalOnlyReconciliationNeedsNoSocialCredentials(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	config := validTestConfig(server.URL)
	config.IdentityProviders = nil
	state := repositoryDesiredState(t)
	state.IdentityProviders = nil
	reconciler, err := New(config, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if len(fake.identityProviders) != 0 || !fake.realm.RegistrationAllowed || !fake.realm.VerifyEmail || !fake.realm.ResetPasswordAllowed {
		t.Fatal("local-only configuration did not enable native accounts")
	}
	writes := fake.writeCount()
	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil || result.Changed() || fake.writeCount() != writes {
		t.Fatalf("local-only state did not converge: result=%v err=%v", result, err)
	}
}

func TestAccountProfileAndMailDriftAreReconciled(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	config := validTestConfig(server.URL)
	reconciler, err := New(config, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	fake.userProfile["unmanagedAttributePolicy"] = "ENABLED"
	fake.smtpServer["host"] = "wrong.example"
	fake.realm.PasswordPolicy = "length(1)"
	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil || !result.Changed() {
		t.Fatalf("drift repair failed: result=%v err=%v", result, err)
	}
	if fake.userProfile["unmanagedAttributePolicy"] != nil || !reflect.DeepEqual(fake.smtpServer, smtpRepresentation(config.SMTP)) || fake.realm.PasswordPolicy != desiredRealmRepresentation(state).PasswordPolicy {
		t.Fatal("profile, password or SMTP drift survived reconciliation")
	}
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil || result.Changed() {
		t.Fatalf("repaired state did not converge: result=%v err=%v", result, err)
	}
}

func TestPhoneProfileDoesNotRequireOrInventEmail(t *testing.T) {
	attributes := desiredLocalAccountProfile()["attributes"].([]map[string]any)
	for _, attribute := range attributes {
		if attribute["name"] == "email" && attribute["required"] != nil {
			t.Fatal("phone registration requires a mailbox")
		}
		if attribute["name"] != "username" {
			continue
		}
		validation := attribute["validations"].(map[string]any)["pattern"].(map[string]any)
		pattern := regexp.MustCompile(validation["pattern"].(string))
		for _, valid := range []string{"+249912345678", "+971500001234", "email-user", "broker-user"} {
			if !pattern.MatchString(valid) {
				t.Fatalf("profile rejected %q", valid)
			}
		}
		for _, invalid := range []string{"user@example.com", "user@", "@example.com", "", "+", "+012345", "+249 123456", "+1234567890123456"} {
			if pattern.MatchString(invalid) {
				t.Fatalf("profile accepted malformed phone %q", invalid)
			}
		}
	}
}

func TestSMTPConfigRejectsMissingAndInsecureValues(t *testing.T) {
	valid := SMTPConfig{Host: "smtp.example.com", Port: 465, From: "accounts@example.com", TLS: true}
	for _, mutate := range []func(*SMTPConfig){
		func(s *SMTPConfig) { s.Host = "" },
		func(s *SMTPConfig) { s.Host = "smtp.example.com:587" },
		func(s *SMTPConfig) { s.Host = "bad..host" },
		func(s *SMTPConfig) { s.Port = 0 },
		func(s *SMTPConfig) { s.From = "" },
		func(s *SMTPConfig) { s.FromDisplayName = "Header\r\nInjection" },
		func(s *SMTPConfig) { s.Username = "user" },
		func(s *SMTPConfig) { s.TLS = false },
	} {
		config := valid
		mutate(&config)
		before := config
		if err := config.Validate(); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("invalid SMTP config accepted: %v", err)
		}
		if config != before {
			t.Fatal("SMTP validation mutated configuration")
		}
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSMTPReassertsMaskedSecretAndRemovesDisabledConfiguration(t *testing.T) {
	config := SMTPConfig{Host: "smtp.example.com", Port: 465, From: "accounts@example.com", Username: "accounts", Password: "correct-secret", TLS: true}
	stored := smtpRepresentation(&config)
	stored["password"] = "drifted-secret"
	writes := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			masked := cloneStringMap(stored)
			if masked["password"] != "" {
				masked["password"] = "**********"
			}
			writeJSON(w, http.StatusOK, map[string]any{"smtpServer": masked})
		case http.MethodPut:
			var payload struct {
				SMTPServer map[string]string `json:"smtpServer"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			stored = payload.SMTPServer
			writes++
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	session := &adminSession{baseURL: server.URL, token: "test-token", http: server.Client()}
	var result Result
	if err := reconcileSMTP(context.Background(), session, "noebs", &config, &result); err != nil {
		t.Fatal(err)
	}
	if stored["password"] != config.Password || writes != 1 {
		t.Fatal("masked SMTP password drift was not overwritten")
	}
	if err := reconcileSMTP(context.Background(), session, "noebs", nil, &result); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 || !result.Changed() {
		t.Fatal("disabled SMTP configuration retained a sender or credential")
	}
}

func testOIDCProvider() IdentityProvider {
	return IdentityProvider{Alias: "enterprise", DisplayName: "Enterprise", ProviderID: "oidc", Credential: "enterprise", Config: map[string]string{
		"defaultScope": "openid profile email", "forwardParameters": "login_hint", "syncMode": "IMPORT",
		"issuer": "https://id.example.com", "authorizationUrl": "https://id.example.com/authorize",
		"tokenUrl": "https://id.example.com/token", "jwksUrl": "https://id.example.com/jwks",
		"userInfoUrl": "https://id.example.com/userinfo", "validateSignature": "true", "useJwksUrl": "true",
		"pkceEnabled": "true", "pkceMethod": "S256",
	}}
}

func TestAdditionalOIDCProviderSharesAuthorityAndConverges(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	state := repositoryDesiredState(t)
	state.IdentityProviders = append(state.IdentityProviders, testOIDCProvider())
	config := validTestConfig(server.URL)
	config.IdentityProviders["enterprise"] = IdentityProviderCredential{ClientID: "enterprise-id", ClientSecret: "enterprise-secret"}
	reconciler, err := New(config, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	provider := fake.identityProviders["enterprise"]
	if provider.PostBrokerLoginFlowAlias != state.Authentication.PostBrokerLoginFlow || provider.FirstBrokerLoginFlowAlias != state.Authentication.FirstBrokerLoginFlow || provider.TrustEmail || provider.StoreToken {
		t.Fatal("OIDC provider did not share the credential and assurance boundaries")
	}
	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil || result.Changed() {
		t.Fatalf("OIDC provider did not converge: result=%v err=%v", result, err)
	}
}

func TestOIDCProviderRejectsUnsafeConfiguration(t *testing.T) {
	for _, key := range []string{"issuer", "authorizationUrl", "tokenUrl", "jwksUrl", "userInfoUrl", "validateSignature", "useJwksUrl", "pkceEnabled", "pkceMethod", "forwardParameters"} {
		state := repositoryDesiredState(t)
		provider := testOIDCProvider()
		delete(provider.Config, key)
		state.IdentityProviders = []IdentityProvider{provider}
		if err := state.Validate(); !errors.Is(err, ErrInvalidDesiredState) {
			t.Fatalf("missing %s: got %v", key, err)
		}
	}
	for _, endpoint := range []string{"http://id.example.com/token", "https://user:pass@id.example.com/token", "https://id.example.com/token?code=unexpected"} {
		state := repositoryDesiredState(t)
		provider := testOIDCProvider()
		provider.Config["tokenUrl"] = endpoint
		state.IdentityProviders = []IdentityProvider{provider}
		if err := state.Validate(); !errors.Is(err, ErrInvalidDesiredState) {
			t.Fatalf("unsafe endpoint accepted: %v", err)
		}
	}
}
