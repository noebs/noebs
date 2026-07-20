package keycloakadmin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
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
	googleAddress := os.Getenv("NOEBS_TEST_GOOGLE_ADDRESS")
	if googleAddress != "" {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err == nil && (host == "accounts.google.com" || host == "oauth2.googleapis.com" || host == "www.googleapis.com" || host == "openidconnect.googleapis.com") {
				address = googleAddress
			}
			return dialer.DialContext(ctx, network, address)
		}
	}
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
	assertRealKeycloakAuthority(t, steady, state)
	if googleAddress != "" {
		seedRealGoogleUser(t, steady, state)
		assertRealWalletStepUp(t, baseURL, transport, steady.config.ClientCredentials[walletAuthorizerClientID].ClientSecret)
	}
	assertRealGoogleBrokerRedirect(t, baseURL, transport, "noebs-mobile", "https://api.noebs.sd/mobile/oauth/callback")
	assertRealGoogleBrokerRedirect(t, baseURL, transport, walletAuthorizerClientID, walletAuthorizationCallbackURI)
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
	assertRealKeycloakAuthority(t, steady, state)
	assertRealGoogleBrokerRedirect(t, baseURL, transport, "noebs-mobile", "https://api.noebs.sd/mobile/oauth/callback")
	assertRealGoogleBrokerRedirect(t, baseURL, transport, walletAuthorizerClientID, walletAuthorizationCallbackURI)
}

func assertRealGoogleBrokerRedirect(t *testing.T, baseURL string, transport http.RoundTripper, clientID, redirectURI string) {
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
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", "openid")
	query.Set("state", "real-keycloak-state")
	query.Set("nonce", "real-keycloak-nonce")
	query.Set("code_challenge", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	query.Set("code_challenge_method", "S256")
	if clientID == walletAuthorizerClientID {
		query.Set("acr_values", googleACR)
		query.Set("login_hint", "wallet-authorizer@example.invalid")
	} else {
		query.Set("scope", "openid organization:*")
	}
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
	expectedBrokerPath := strings.TrimSuffix(authorization.Path, "/protocol/openid-connect/auth") + "/broker/google/login"
	if broker.Path != expectedBrokerPath || broker.Host != authorization.Host {
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
	if provider.Query().Has("acr_values") || provider.Query().Has("max_age") {
		t.Fatalf("Google provider redirect leaked local authentication parameters: %s", provider.Redacted())
	}
	if clientID == walletAuthorizerClientID && provider.Query().Get("login_hint") != "wallet-authorizer@example.invalid" {
		t.Fatalf("Google provider login_hint = %q", provider.Query().Get("login_hint"))
	}
}

const realTestOTPSecret = "noebs-real-keycloak-test-otp-secret"

var otpFormActionPattern = regexp.MustCompile(`<form id="kc-otp-login-form"[^>]+action="([^"]+)"`)

func seedRealGoogleUser(t *testing.T, reconciler *Reconciler, state DesiredState) {
	t.Helper()
	ctx := context.Background()
	session, err := reconciler.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := realmPath(state.Realm.Name)
	user := map[string]any{
		"username":        "wallet-authorizer@example.invalid",
		"email":           "wallet-authorizer@example.invalid",
		"emailVerified":   true,
		"enabled":         true,
		"requiredActions": []string{},
		"credentials": []map[string]any{{
			"type":           "otp",
			"secretData":     `{"value":"` + realTestOTPSecret + `"}`,
			"credentialData": `{"subType":"totp","digits":6,"counter":0,"period":30,"algorithm":"HmacSHA256"}`,
		}},
	}
	if err := session.post(ctx, base+"/users", user); err != nil {
		t.Fatal(err)
	}
	var users []userRepresentation
	if _, err := session.get(ctx, base+"/users?exact=true&username="+url.QueryEscape("wallet-authorizer@example.invalid"), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("seeded Google users = %#v", users)
	}
	userPath := base + "/users/" + url.PathEscape(users[0].ID)
	var created map[string]any
	if _, err := session.get(ctx, userPath, &created); err != nil {
		t.Fatal(err)
	}
	created["requiredActions"] = []string{}
	if err := session.put(ctx, userPath, created); err != nil {
		t.Fatal(err)
	}
	link := map[string]string{
		"identityProvider": "google",
		"userId":           "noebs-google-test-subject",
		"userName":         "wallet-authorizer@example.invalid",
	}
	if err := session.post(ctx, userPath+"/federated-identity/google", link); err != nil {
		t.Fatal(err)
	}
}

func assertRealWalletStepUp(t *testing.T, baseURL string, transport http.RoundTripper, clientSecret string) {
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
	verifier := "noebs-real-keycloak-wallet-authorizer-pkce-verifier-0000000001"
	authorization := realWalletAuthorizationURL(t, baseURL, verifier, "wallet-step-up-1", "wallet-nonce-1")
	action, sawGoogle, sawPostBroker := reachRealOTP(t, client, authorization)
	if !sawGoogle || !sawPostBroker {
		t.Fatalf("first wallet authorization: Google=%v post-broker=%v", sawGoogle, sawPostBroker)
	}
	callback := submitRealOTP(t, client, action, realTOTP(realTestOTPSecret, time.Now()))
	if callback.Hostname() != "api.noebs.sd" || callback.Path != "/wallet/authorizations/oauth/callback" || callback.Query().Get("state") != "wallet-step-up-1" {
		t.Fatalf("wallet authorization callback = %s", callback.Redacted())
	}
	exchangeRealAuthorizationCode(t, client, baseURL, callback.Query().Get("code"), verifier, clientSecret)

	secondVerifier := "noebs-real-keycloak-wallet-authorizer-pkce-verifier-0000000002"
	second := realWalletAuthorizationURL(t, baseURL, secondVerifier, "wallet-step-up-2", "wallet-nonce-2")
	_, sawGoogle, sawPostBroker = reachRealOTP(t, client, second)
	if sawGoogle || sawPostBroker {
		t.Fatalf("second wallet authorization unexpectedly brokered: Google=%v post-broker=%v", sawGoogle, sawPostBroker)
	}
}

func realWalletAuthorizationURL(t *testing.T, baseURL, verifier, state, nonce string) string {
	t.Helper()
	authorization, err := url.Parse(baseURL + "/realms/noebs/protocol/openid-connect/auth")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(verifier))
	query := authorization.Query()
	query.Set("client_id", walletAuthorizerClientID)
	query.Set("redirect_uri", walletAuthorizationCallbackURI)
	query.Set("response_type", "code")
	query.Set("scope", "openid")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(digest[:]))
	query.Set("code_challenge_method", "S256")
	query.Set("acr_values", googleACR)
	query.Set("login_hint", "wallet-authorizer@example.invalid")
	authorization.RawQuery = query.Encode()
	return authorization.String()
}

func reachRealOTP(t *testing.T, client *http.Client, start string) (string, bool, bool) {
	t.Helper()
	current := start
	sawGoogle := false
	sawPostBroker := false
	for range 16 {
		response, err := client.Get(current)
		if err != nil {
			t.Fatal(err)
		}
		body := readRealResponse(t, response)
		assertNoPasswordBody(t, body)
		if response.StatusCode == http.StatusOK {
			match := otpFormActionPattern.FindSubmatch(body)
			if len(match) != 2 {
				t.Fatalf("wallet step-up returned a non-OTP page: %s", response.Request.URL.Redacted())
			}
			return html.UnescapeString(string(match[1])), sawGoogle, sawPostBroker
		}
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			t.Fatalf("wallet step-up status = %d at %s", response.StatusCode, response.Request.URL.Redacted())
		}
		next, err := response.Request.URL.Parse(response.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if next.Hostname() == "api.noebs.sd" {
			t.Fatalf("wallet authorization completed without OTP: %s", next.Redacted())
		}
		if next.Hostname() == "accounts.google.com" {
			sawGoogle = true
		}
		if strings.HasSuffix(next.Path, "/login-actions/post-broker-login") {
			sawPostBroker = true
		}
		current = next.String()
	}
	t.Fatal("wallet step-up exceeded redirect limit")
	return "", false, false
}

func submitRealOTP(t *testing.T, client *http.Client, action, otp string) *url.URL {
	t.Helper()
	response, err := client.PostForm(action, url.Values{"otp": {otp}, "selectedCredentialId": {""}})
	if err != nil {
		t.Fatal(err)
	}
	for range 12 {
		body := readRealResponse(t, response)
		assertNoPasswordBody(t, body)
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			t.Fatalf("OTP submission status = %d at %s", response.StatusCode, response.Request.URL.Redacted())
		}
		next, err := response.Request.URL.Parse(response.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if next.Hostname() == "api.noebs.sd" {
			return next
		}
		response, err = client.Get(next.String())
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("OTP submission exceeded redirect limit")
	return nil
}

func exchangeRealAuthorizationCode(t *testing.T, client *http.Client, baseURL, code, verifier, clientSecret string) {
	t.Helper()
	response, err := client.PostForm(baseURL+"/realms/noebs/protocol/openid-connect/token", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {walletAuthorizerClientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {walletAuthorizationCallbackURI},
		"code_verifier": {verifier},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := readRealResponse(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authorization code exchange status = %d: %s", response.StatusCode, body)
	}
	var tokens struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tokens); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tokens.IDToken, ".")
	if len(parts) != 3 {
		t.Fatal("authorization code exchange returned an invalid ID token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		ACR                string `json:"acr"`
		AuthenticationTime int64  `json:"auth_time"`
		IssuedAt           int64  `json:"iat"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.ACR != googleTOTPACR {
		t.Fatalf("wallet ID token acr = %q, want %q", claims.ACR, googleTOTPACR)
	}
	authenticationTime := time.Unix(claims.AuthenticationTime, 0)
	issuedAt := time.Unix(claims.IssuedAt, 0)
	now := time.Now()
	if claims.AuthenticationTime <= 0 || claims.IssuedAt <= 0 || authenticationTime.After(issuedAt) ||
		authenticationTime.After(now.Add(5*time.Second)) || now.Sub(authenticationTime) > 30*time.Second {
		t.Fatalf("wallet ID token auth_time = %d and iat = %d, want a fresh step-up authentication", claims.AuthenticationTime, claims.IssuedAt)
	}
}

func realTOTP(secret string, now time.Time) string {
	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(now.Unix()/30))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(counter)
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

func readRealResponse(t *testing.T, response *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return body
}

func assertRealKeycloakAuthority(t *testing.T, reconciler *Reconciler, state DesiredState) {
	t.Helper()
	ctx := context.Background()
	session, err := reconciler.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := realmPath(state.Realm.Name)
	authenticationBase := base + "/authentication"
	for _, flow := range desiredAuthenticationFlows(state) {
		assertRealAuthenticationFlowExact(t, session, authenticationBase, flow)
	}

	authorizer, found, err := findClient(ctx, session, base, walletAuthorizerClientID)
	if err != nil || !found {
		t.Fatalf("read wallet authorizer: found=%v err=%v", found, err)
	}
	attributes := managedClientAttributes()
	attributes["access.token.signed.response.alg"] = "RS256"
	attributes["id.token.signed.response.alg"] = "RS256"
	attributes["pkce.code.challenge.method"] = "S256"
	attributes["default.acr.values"] = googleTOTPACR
	attributes["minimum.acr.value"] = googleTOTPACR
	attributes[managedClientSecretHash] = secretHash(reconciler.config.ClientCredentials[walletAuthorizerClientID].ClientSecret)
	wanted := clientRepresentation{
		ClientID:                           walletAuthorizerClientID,
		Name:                               "Noebs Wallet Authorizer",
		Enabled:                            true,
		Protocol:                           "openid-connect",
		ClientAuthenticatorType:            "client-secret",
		StandardFlowEnabled:                true,
		RedirectURIs:                       []string{walletAuthorizationCallbackURI},
		WebOrigins:                         []string{},
		Attributes:                         attributes,
		NodeReRegistrationTimeout:          -1,
		AuthenticationFlowBindingOverrides: map[string]string{},
	}
	if !clientMatches(authorizer, wanted) {
		t.Fatalf("wallet authorizer = %#v", authorizer)
	}
	secretMatches, err := clientSecretMatches(ctx, session, base, authorizer, reconciler.config.ClientCredentials[walletAuthorizerClientID].ClientSecret)
	if err != nil || !secretMatches {
		t.Fatalf("wallet authorizer secret: matches=%v err=%v", secretMatches, err)
	}
	var mappers []protocolMapperRepresentation
	if _, err := session.get(ctx, base+"/clients/"+url.PathEscape(authorizer.ID)+"/protocol-mappers/models", &mappers); err != nil {
		t.Fatal(err)
	}
	if len(mappers) != 0 {
		t.Fatalf("wallet authorizer protocol mappers = %#v", mappers)
	}
	assertRealClientScopes(t, session, base, authorizer, "default", []string{"acr"})
	assertRealClientScopes(t, session, base, authorizer, "optional", nil)

	var google identityProviderRepresentation
	if _, err := session.get(ctx, base+"/identity-provider/instances/google", &google); err != nil {
		t.Fatal(err)
	}
	if google.FirstBrokerLoginFlowAlias != state.Authentication.FirstBrokerLoginFlow || google.PostBrokerLoginFlowAlias != state.Authentication.PostBrokerLoginFlow ||
		google.Config["forwardParameters"] != "login_hint" {
		t.Fatalf("Google identity provider authority = %#v", google)
	}
}

func assertRealAuthenticationFlowExact(t *testing.T, session *adminSession, base string, desired managedAuthenticationFlow) {
	t.Helper()
	executions, err := listDirectAuthenticationExecutions(context.Background(), session, base, desired.Alias)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != len(desired.Executions) {
		t.Fatalf("authentication flow %s executions = %#v", desired.Alias, executions)
	}
	for index, wanted := range desired.Executions {
		execution := executions[index]
		if execution.Requirement != wanted.Requirement || execution.Priority != wanted.Priority || !authenticationExecutionIdentityMatches(execution, wanted) {
			t.Fatalf("authentication flow %s execution %d = %#v", desired.Alias, index, execution)
		}
		if wanted.Config != nil {
			var config authenticatorConfigRepresentation
			if _, err := session.get(context.Background(), base+"/config/"+url.PathEscape(execution.AuthenticationConfig), &config); err != nil {
				t.Fatal(err)
			}
			if config.Alias != wanted.ConfigAlias || !equalStringMap(config.Config, wanted.Config) {
				t.Fatalf("authentication flow %s config = %#v", desired.Alias, config)
			}
		} else if execution.AuthenticationConfig != "" {
			t.Fatalf("authentication flow %s has unexpected config %s", desired.Alias, execution.AuthenticationConfig)
		}
		if wanted.Flow != nil {
			assertRealAuthenticationFlowExact(t, session, base, *wanted.Flow)
		}
	}
}

func assertRealClientScopes(t *testing.T, session *adminSession, base string, client clientRepresentation, assignment string, wanted []string) {
	t.Helper()
	var scopes []clientScopeRepresentation
	if _, err := session.get(context.Background(), base+"/clients/"+url.PathEscape(client.ID)+"/"+assignment+"-client-scopes", &scopes); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		names = append(names, scope.Name)
	}
	if !equalStrings(names, wanted) {
		t.Fatalf("client %s %s scopes = %v, want %v", client.ClientID, assignment, names, wanted)
	}
}

func assertNoPasswordForm(t *testing.T, response *http.Response) {
	t.Helper()
	assertNoPasswordBody(t, readRealResponse(t, response))
}

func assertNoPasswordBody(t *testing.T, body []byte) {
	t.Helper()
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
	hostileACRMap := `{"urn:noebs:acr:google":1,"urn:noebs:acr:google-totp":1}`
	realm.Attributes["acr.loa.map"] = &hostileACRMap
	if err := session.put(ctx, base, realm); err != nil {
		t.Fatal(err)
	}

	password.Priority = 30
	password.Requirement = "ALTERNATIVE"
	if err := createAuthenticationExecution(ctx, session, authenticationBase, state.Authentication.BrowserFlow, password); err != nil {
		t.Fatal(err)
	}
	setRealExecutionRequirement(t, session, authenticationBase, state.Authentication.BrowserFlow, password)
	executions, err := listDirectAuthenticationExecutions(ctx, session, authenticationBase, googleLoA1FlowAlias)
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
	for _, alias := range []string{googleTOTPLoA2FlowAlias, googlePostBrokerLoA2FlowAlias} {
		loa2Executions, err := listDirectAuthenticationExecutions(ctx, session, authenticationBase, alias)
		if err != nil {
			t.Fatal(err)
		}
		for _, execution := range loa2Executions {
			if execution.ProviderID != "conditional-level-of-authentication" {
				continue
			}
			execution.Requirement = "DISABLED"
			if err := session.put(ctx, authenticationBase+"/flows/"+url.PathEscape(alias)+"/executions", execution); err != nil {
				t.Fatal(err)
			}
			var config authenticatorConfigRepresentation
			if _, err := session.get(ctx, authenticationBase+"/config/"+url.PathEscape(execution.AuthenticationConfig), &config); err != nil {
				t.Fatal(err)
			}
			config.Alias = "hostile-loa2"
			config.Config = map[string]string{"loa-condition-level": "1", "loa-max-age": "300"}
			if err := session.put(ctx, authenticationBase+"/config/"+url.PathEscape(config.ID), config); err != nil {
				t.Fatal(err)
			}
		}
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
	google.Config["forwardParameters"] = "acr_values"
	google.FirstBrokerLoginFlowAlias = "first broker login"
	google.PostBrokerLoginFlowAlias = rogue.Alias
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

	authorizer, found, err := findClient(ctx, session, base, walletAuthorizerClientID)
	if err != nil || !found {
		t.Fatalf("read wallet authorizer: found=%v err=%v", found, err)
	}
	authorizer.StandardFlowEnabled = false
	authorizer.ImplicitFlowEnabled = true
	authorizer.DirectAccessGrantsEnabled = true
	authorizer.ServiceAccountsEnabled = true
	authorizer.AuthorizationServicesEnabled = true
	authorizer.FullScopeAllowed = true
	authorizer.RedirectURIs = []string{"https://hostile.invalid/callback"}
	authorizer.WebOrigins = []string{"https://hostile.invalid"}
	authorizer.Attributes["pkce.code.challenge.method"] = "plain"
	authorizer.Attributes["default.acr.values"] = googleACR
	authorizer.Attributes["minimum.acr.value"] = googleACR
	authorizer.Attributes["hostile.attribute"] = "true"
	if err := session.put(ctx, base+"/clients/"+url.PathEscape(authorizer.ID), authorizer); err != nil {
		t.Fatal(err)
	}
	hostileMapper := protocolMapperRepresentation{
		Name: "hostile-wallet-claim", Protocol: "openid-connect", ProtocolMapper: "oidc-hardcoded-claim-mapper", Config: map[string]string{},
	}
	if err := session.post(ctx, base+"/clients/"+url.PathEscape(authorizer.ID)+"/protocol-mappers/models", hostileMapper); err != nil {
		t.Fatal(err)
	}
	var scopes []clientScopeRepresentation
	if _, err := session.get(ctx, base+"/client-scopes", &scopes); err != nil {
		t.Fatal(err)
	}
	for _, scope := range scopes {
		switch scope.Name {
		case "email":
			if err := session.put(ctx, base+"/clients/"+url.PathEscape(authorizer.ID)+"/default-client-scopes/"+url.PathEscape(scope.ID), nil); err != nil {
				t.Fatal(err)
			}
		case "organization":
			if err := session.put(ctx, base+"/clients/"+url.PathEscape(authorizer.ID)+"/optional-client-scopes/"+url.PathEscape(scope.ID), nil); err != nil {
				t.Fatal(err)
			}
		}
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
