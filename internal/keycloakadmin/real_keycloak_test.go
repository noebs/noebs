package keycloakadmin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type recordingTransport struct {
	base      http.RoundTripper
	mutations *[]string
}

func (t recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPut || request.Method == http.MethodDelete || request.Method == http.MethodPost {
		*t.mutations = append(*t.mutations, request.Method+" "+request.URL.Path)
	}
	return t.base.RoundTrip(request)
}

func TestRealKeycloak26_7Reconcile(t *testing.T) {
	baseURL := os.Getenv("NOEBS_TEST_KEYCLOAK_URL")
	if baseURL == "" {
		t.Skip("NOEBS_TEST_KEYCLOAK_URL is not set")
	}
	secret := os.Getenv("NOEBS_TEST_KEYCLOAK_BOOTSTRAP_SECRET")
	caPath := os.Getenv("NOEBS_TEST_KEYCLOAK_CA")
	if secret == "" || caPath == "" {
		t.Fatal("NOEBS_TEST_KEYCLOAK_BOOTSTRAP_SECRET and NOEBS_TEST_KEYCLOAK_CA are required")
	}
	ca, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("test Keycloak CA is not PEM")
	}
	mutations := []string{}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    roots,
	}}
	httpClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: recordingTransport{base: transport, mutations: &mutations},
	}
	config := validTestConfig(baseURL)
	config.ClientSecret = secret
	reconciler, err := New(config, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	first, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("first real Reconcile() error = %v", err)
	}
	t.Logf("first real reconcile: created=%d updated=%d deleted=%d", first.Created, first.Updated, first.Deleted)
	mutations = mutations[:0]
	steadyConfig := config
	steadyConfig.AdminRealm = state.Realm.Name
	steadyConfig.ClientID = state.ReconcilerClient.ClientID
	steadyConfig.ClientSecret = steadyConfig.ClientCredentials[state.ReconcilerClient.Credential].ClientSecret
	steady, err := New(steadyConfig, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	second, err := steady.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("second real Reconcile() error = %v", err)
	}
	if second.Changed() {
		t.Logf("second real reconcile mutations: %v", mutations)
		t.Fatalf("second real Reconcile() result = %#v", second)
	}
	assertRealGoogleOnlyBrowser(t, baseURL, transport)
	injectRealAuthenticationDrift(t, steady, state)
	repaired, err := steady.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("real hostile-drift Reconcile() error = %v", err)
	}
	if !repaired.Changed() {
		t.Fatal("real hostile authentication drift was not reported")
	}
	t.Logf("real hostile-drift repair: created=%d updated=%d deleted=%d", repaired.Created, repaired.Updated, repaired.Deleted)
	final, err := steady.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("final real Reconcile() error = %v", err)
	}
	if final.Changed() {
		t.Fatalf("final real Reconcile() result = %#v", final)
	}
	assertRealGoogleOnlyBrowser(t, baseURL, transport)
}

func assertRealGoogleOnlyBrowser(t *testing.T, baseURL string, transport http.RoundTripper) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	authorization, err := url.Parse(baseURL + "/realms/noebs/protocol/openid-connect/auth")
	if err != nil {
		t.Fatal(err)
	}
	query := authorization.Query()
	query.Set("client_id", "noebs-mobile")
	query.Set("redirect_uri", "https://api.noebs.sd/mobile/oauth/callback")
	query.Set("response_type", "code")
	query.Set("scope", "openid organization:*")
	query.Set("state", "real-keycloak-state")
	query.Set("nonce", "real-keycloak-nonce")
	query.Set("code_challenge", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	query.Set("code_challenge_method", "S256")
	authorization.RawQuery = query.Encode()

	response, err := client.Get(authorization.String())
	if err != nil {
		t.Fatal(err)
	}
	assertNoPasswordForm(t, response)
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("authorization status = %d", response.StatusCode)
	}
	broker, err := authorization.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if broker.Path != "/realms/noebs/broker/google/login" || broker.Host != authorization.Host {
		t.Fatalf("authorization redirect = %s", broker.Redacted())
	}
	response, err = client.Get(broker.String())
	if err != nil {
		t.Fatal(err)
	}
	assertNoPasswordForm(t, response)
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("Google broker status = %d", response.StatusCode)
	}
	provider, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if provider.Scheme != "https" || provider.Hostname() != "accounts.google.com" {
		t.Fatalf("Google provider redirect host = %s", provider.Redacted())
	}
}

func assertNoPasswordForm(t *testing.T, response *http.Response) {
	t.Helper()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(body))
	for _, marker := range []string{`name="username"`, `name='username'`, `name="password"`, `name='password'`} {
		if strings.Contains(lower, marker) {
			t.Fatalf("Keycloak response contains local credential field %s", marker)
		}
	}
}

func injectRealAuthenticationDrift(t *testing.T, reconciler *Reconciler, state DesiredState) {
	t.Helper()
	ctx := context.Background()
	session, err := reconciler.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := realmPath(state.Realm.Name)
	authenticationBase := base + "/authentication"
	rogue := authenticationFlowRepresentation{
		Alias: "hostile-password-flow", Description: "hostile", ProviderID: "basic-flow", TopLevel: true, BuiltIn: false,
	}
	if err := session.post(ctx, authenticationBase+"/flows", rogue); err != nil {
		t.Fatal(err)
	}
	password := managedAuthenticationExecution{ProviderID: "auth-username-password-form", Requirement: "REQUIRED", Priority: 10}
	if err := createAuthenticationExecution(ctx, session, authenticationBase, rogue.Alias, password); err != nil {
		t.Fatal(err)
	}

	var realm realmRepresentation
	if _, err := session.get(ctx, base, &realm); err != nil {
		t.Fatal(err)
	}
	realm.BrowserFlow = rogue.Alias
	realm.FirstBrokerLoginFlow = "first broker login"
	realm.SSLRequired = "external"
	realm.OTPPolicyAlgorithm = "HmacSHA1"
	realm.OTPPolicyCodeReusable = true
	realm.MaxSecondaryAuthFailures = 0
	if err := session.put(ctx, base, realm); err != nil {
		t.Fatal(err)
	}

	password.Priority = 30
	password.Requirement = "ALTERNATIVE"
	if err := createAuthenticationExecution(ctx, session, authenticationBase, state.Authentication.BrowserFlow, password); err != nil {
		t.Fatal(err)
	}
	setRealExecutionRequirement(t, session, authenticationBase, state.Authentication.BrowserFlow, password)
	executions, err := listDirectAuthenticationExecutions(ctx, session, authenticationBase, state.Authentication.BrowserFlow)
	if err != nil {
		t.Fatal(err)
	}
	for _, execution := range executions {
		if execution.ProviderID != "identity-provider-redirector" {
			continue
		}
		var config authenticatorConfigRepresentation
		if _, err := session.get(ctx, authenticationBase+"/config/"+url.PathEscape(execution.AuthenticationConfig), &config); err != nil {
			t.Fatal(err)
		}
		config.Alias = "hostile-redirect"
		config.Config = map[string]string{"defaultProvider": "hostile"}
		if err := session.put(ctx, authenticationBase+"/config/"+url.PathEscape(config.ID), config); err != nil {
			t.Fatal(err)
		}
	}
	otpExecutions, err := listDirectAuthenticationExecutions(ctx, session, authenticationBase, state.Authentication.PostBrokerLoginFlow)
	if err != nil {
		t.Fatal(err)
	}
	otpExecutions[0].Requirement = "DISABLED"
	if err := session.put(ctx, authenticationBase+"/flows/"+url.PathEscape(state.Authentication.PostBrokerLoginFlow)+"/executions", otpExecutions[0]); err != nil {
		t.Fatal(err)
	}

	var configureOTP requiredActionProviderRepresentation
	if _, err := session.get(ctx, authenticationBase+"/required-actions/"+configureTOTPProvider, &configureOTP); err != nil {
		t.Fatal(err)
	}
	configureOTP.Enabled = false
	configureOTP.DefaultAction = false
	configureOTP.Priority = 999
	configureOTP.Config = map[string]string{"hostile": "true"}
	if err := session.put(ctx, authenticationBase+"/required-actions/"+configureTOTPProvider, configureOTP); err != nil {
		t.Fatal(err)
	}
	var updatePassword requiredActionProviderRepresentation
	if _, err := session.get(ctx, authenticationBase+"/required-actions/UPDATE_PASSWORD", &updatePassword); err != nil {
		t.Fatal(err)
	}
	updatePassword.Enabled = true
	updatePassword.DefaultAction = true
	if err := session.put(ctx, authenticationBase+"/required-actions/UPDATE_PASSWORD", updatePassword); err != nil {
		t.Fatal(err)
	}

	var google identityProviderRepresentation
	providerPath := base + "/identity-provider/instances/google"
	if _, err := session.get(ctx, providerPath, &google); err != nil {
		t.Fatal(err)
	}
	credential := reconciler.config.IdentityProviders["google"]
	google.Config["clientSecret"] = credential.ClientSecret
	google.FirstBrokerLoginFlowAlias = "first broker login"
	google.PostBrokerLoginFlowAlias = ""
	if err := session.put(ctx, providerPath, google); err != nil {
		t.Fatal(err)
	}

	adminCLI, found, err := findClient(ctx, session, base, "admin-cli")
	if err != nil || !found {
		t.Fatalf("read admin-cli: found=%v err=%v", found, err)
	}
	adminCLI.Enabled = true
	adminCLI.StandardFlowEnabled = true
	adminCLI.DirectAccessGrantsEnabled = true
	adminCLI.FullScopeAllowed = true
	if err := session.put(ctx, base+"/clients/"+url.PathEscape(adminCLI.ID), adminCLI); err != nil {
		t.Fatal(err)
	}
}

func setRealExecutionRequirement(t *testing.T, session *adminSession, base, flowAlias string, desired managedAuthenticationExecution) {
	t.Helper()
	executions, err := listDirectAuthenticationExecutions(context.Background(), session, base, flowAlias)
	if err != nil {
		t.Fatal(err)
	}
	for _, execution := range executions {
		if execution.ProviderID != desired.ProviderID {
			continue
		}
		execution.Requirement = desired.Requirement
		execution.Priority = desired.Priority
		if err := session.put(context.Background(), base+"/flows/"+url.PathEscape(flowAlias)+"/executions", execution); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("execution %s is missing from %s", desired.ProviderID, flowAlias)
}
