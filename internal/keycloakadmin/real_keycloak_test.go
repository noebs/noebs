package keycloakadmin

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base32"
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

	"github.com/coreos/go-oidc/v3/oidc"
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
		if !t.Run("FreshMicrosoftCompatibleOTPEnrollment", func(t *testing.T) {
			assertRealFreshOTPEnrollment(t, baseURL, transport, steady, state)
		}) {
			return
		}
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

func assertRealFreshOTPEnrollment(t *testing.T, baseURL string, transport http.RoundTripper, reconciler *Reconciler, state DesiredState) {
	t.Helper()
	ctx := context.Background()
	session, err := reconciler.session(ctx)
	if err != nil {
		t.Fatal("fresh enrollment: admin session failed")
	}
	base := realmPath(state.Realm.Name)
	lookupPath := base + "/users?exact=true&username=" + url.QueryEscape("wallet-authorizer@example.invalid")
	var users []userRepresentation
	if _, err := session.get(ctx, lookupPath, &users); err != nil || len(users) != 0 {
		t.Fatal("fresh enrollment requires an unused isolated mock Google identity")
	}
	// The mock has one fixed identity. Delete only the new test user afterward
	// so the existing SHA-256 credential fixture can run independently.
	userPath := ""
	t.Cleanup(func() {
		if userPath != "" {
			if err := session.delete(context.Background(), userPath, nil); err != nil {
				t.Error("fresh enrollment: isolated test-user cleanup failed")
			}
		}
	})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("fresh enrollment: cookie jar initialization failed")
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport, Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	authorization, err := url.Parse(realWalletAuthorizationURL(t, baseURL,
		"noebs-real-keycloak-fresh-enrollment-pkce-verifier-0000000001", "fresh-enrollment-state", "fresh-enrollment-nonce"))
	if err != nil {
		t.Fatal("fresh enrollment: authorization URL failed")
	}
	query := authorization.Query()
	query.Set("client_id", "noebs-mobile")
	query.Set("redirect_uri", "https://api.noebs.sd/mobile/oauth/callback")
	query.Set("scope", "openid organization:*")
	query.Set("kc_idp_hint", "google")
	query.Del("max_age")
	query.Del("login_hint")
	query.Set("ui_locales", "en")
	authorization.RawQuery = query.Encode()
	response, err := client.Get(authorization.String())
	if err != nil {
		t.Fatal("fresh enrollment: authorization request failed")
	}
	pageURL, body := followRealEnrollment(t, client, response, authorization, true)
	if _, err := session.get(ctx, lookupPath, &users); err != nil || len(users) != 1 {
		t.Fatal("fresh enrollment: first broker login did not create exactly one isolated user")
	}
	userPath = base + "/users/" + url.PathEscape(users[0].ID)
	type credential struct {
		Type string `json:"type"`
		Data string `json:"credentialData"`
	}
	var credentials []credential
	if _, err := session.get(ctx, userPath+"/credentials", &credentials); err != nil || len(credentials) != 0 {
		t.Fatal("fresh enrollment: expected no stored credentials before setup")
	}
	if !strings.Contains(string(body), `id="kc-totp-secret-qr-code"`) || !strings.Contains(string(body), "Microsoft Authenticator") {
		t.Fatal("fresh enrollment: QR setup did not advertise Microsoft Authenticator")
	}
	manualLink := realOTPSetupMatch(t, body, `<a href="([^"]+)" id="mode-manual"`, "manual setup link")
	manualURL, err := pageURL.Parse(manualLink)
	if err != nil || manualURL.Scheme != authorization.Scheme || manualURL.Host != authorization.Host {
		t.Fatal("fresh enrollment: manual setup link is outside the isolated issuer")
	}
	response, err = client.Get(manualURL.String())
	if err != nil {
		t.Fatal("fresh enrollment: manual setup request failed")
	}
	pageURL, body = followRealEnrollment(t, client, response, authorization, true)
	for id, expected := range map[string]string{"algorithm": "SHA1", "digits": "6", "period": "30"} {
		value := realOTPSetupMatch(t, body, `<li id="kc-totp-`+id+`">[^<]*:\s*([^<]+)</li>`, "manual policy "+id)
		if strings.TrimSpace(value) != expected {
			t.Fatalf("fresh enrollment: unexpected %s policy", id)
		}
	}
	encoded := realOTPSetupMatch(t, body, `<span id="kc-totp-secret-key">([^<]+)</span>`, "manual setup key")
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.Join(strings.Fields(encoded), ""))
	if err != nil || len(secret) == 0 {
		t.Fatal("fresh enrollment: invalid manual setup key encoding")
	}
	hidden := realOTPSetupMatch(t, body, `<input[^>]*name="totpSecret"[^>]*value="([^"]+)"`, "hidden setup key")
	if string(secret) != hidden {
		t.Fatal("fresh enrollment: displayed and submitted setup keys differ")
	}
	action := realOTPSetupMatch(t, body, `<form action="([^"]+)"[^>]*id="kc-totp-settings-form"`, "setup form action")
	actionURL, err := pageURL.Parse(action)
	if err != nil || actionURL.Scheme != authorization.Scheme || actionURL.Host != authorization.Host {
		t.Fatal("fresh enrollment: setup action is outside the isolated issuer")
	}
	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(time.Now().Unix()/30))
	mac := hmac.New(sha1.New, secret) // Microsoft Authenticator's TOTP profile.
	_, _ = mac.Write(counter)
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	code := fmt.Sprintf("%06d", (binary.BigEndian.Uint32(digest[offset:offset+4])&0x7fffffff)%1_000_000)
	response, err = client.PostForm(actionURL.String(), url.Values{
		"totp": {code}, "totpSecret": {hidden}, "mode": {"manual"}, "userLabel": {"synthetic-microsoft-compatible"},
	})
	if err != nil {
		t.Fatal("fresh enrollment: setup submission failed")
	}
	callback, _ := followRealEnrollment(t, client, response, authorization, false)
	if callback.Query().Get("state") != "fresh-enrollment-state" || callback.Query().Get("code") == "" || callback.Query().Get("error") != "" {
		t.Fatal("fresh enrollment: authorization did not complete successfully")
	}
	credentials = nil
	if _, err := session.get(ctx, userPath+"/credentials", &credentials); err != nil || len(credentials) != 1 || credentials[0].Type != "otp" {
		t.Fatal("fresh enrollment: expected exactly one stored OTP credential")
	}
	var metadata struct {
		Algorithm string `json:"algorithm"`
		SubType   string `json:"subType"`
		Digits    int    `json:"digits"`
		Period    int    `json:"period"`
	}
	if err := json.Unmarshal([]byte(credentials[0].Data), &metadata); err != nil ||
		metadata.Algorithm != "HmacSHA1" || metadata.SubType != "totp" || metadata.Digits != 6 || metadata.Period != 30 {
		t.Fatal("fresh enrollment: stored OTP metadata differs from the Microsoft-compatible profile")
	}
}

func realOTPSetupMatch(t *testing.T, body []byte, pattern, field string) string {
	t.Helper()
	matches := regexp.MustCompile(pattern).FindAllSubmatch(body, -1)
	if len(matches) != 1 || len(matches[0]) != 2 {
		t.Fatalf("fresh enrollment: missing or duplicate %s", field)
	}
	return html.UnescapeString(string(matches[0][1]))
}

func followRealEnrollment(t *testing.T, client *http.Client, response *http.Response, issuer *url.URL, wantSetup bool) (*url.URL, []byte) {
	t.Helper()
	for range 16 {
		body := readRealResponse(t, response)
		assertNoPasswordBody(t, body)
		if response.StatusCode == http.StatusOK && wantSetup && strings.Contains(string(body), `id="kc-totp-settings-form"`) {
			return response.Request.URL, body
		}
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			t.Fatalf("fresh enrollment: unexpected HTTP status %d", response.StatusCode)
		}
		next, err := response.Request.URL.Parse(response.Header.Get("Location"))
		if err != nil || next.Scheme != "https" || next.User != nil {
			t.Fatal("fresh enrollment: invalid redirect")
		}
		if next.Host == "api.noebs.sd" && next.Path == "/mobile/oauth/callback" {
			if wantSetup {
				t.Fatal("fresh enrollment: authentication completed without required OTP setup")
			}
			return next, nil // Never fetch a public callback or log its query.
		}
		if next.Host != issuer.Host && next.Host != "accounts.google.com" {
			t.Fatal("fresh enrollment: redirect is outside the isolated issuer and Google mock")
		}
		response, err = client.Get(next.String())
		if err != nil {
			t.Fatal("fresh enrollment: redirect request failed")
		}
	}
	t.Fatal("fresh enrollment: redirect limit exceeded")
	return nil, nil
}

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
		// Retain a credential enrolled under the previous SHA-256 policy. Its
		// successful step-up proves an enrollment-policy change preserves it.
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
	firstStarted := time.Now().Truncate(time.Second)
	authorization := realWalletAuthorizationURL(t, baseURL, verifier, "wallet-step-up-1", "wallet-nonce-1")
	action, sawGoogle, sawPostBroker := reachRealOTP(t, client, authorization)
	if !sawGoogle || !sawPostBroker {
		t.Fatalf("first wallet authorization: Google=%v post-broker=%v", sawGoogle, sawPostBroker)
	}
	firstCodeTime := time.Now()
	callback := submitRealOTP(t, client, action, realTOTP(realTestOTPSecret, firstCodeTime))
	if callback.Host != "api.noebs.sd" || callback.Path != "/wallet/authorizations/oauth/callback" ||
		callback.Query().Get("state") != "wallet-step-up-1" || callback.Query().Get("code") == "" || callback.Query().Get("error") != "" {
		t.Fatal("first wallet authorization returned an invalid callback")
	}
	firstAuthTime := exchangeRealAuthorizationCode(t, client, baseURL, callback.Query().Get("code"), verifier, clientSecret, "wallet-nonce-1")
	if firstAuthTime.Before(firstStarted) {
		t.Fatal("first wallet authorization reused an older authentication time")
	}

	// Retain the browser session but enter a new current OTP. Code reuse stays
	// disabled; waiting for its next period takes at most 30 seconds.
	nextPeriod := time.Unix((firstCodeTime.Unix()/30+1)*30, 0)
	if delay := time.Until(nextPeriod); delay > 0 {
		time.Sleep(delay)
	}
	secondVerifier := "noebs-real-keycloak-wallet-authorizer-pkce-verifier-0000000002"
	secondStarted := time.Now().Truncate(time.Second)
	second := realWalletAuthorizationURL(t, baseURL, secondVerifier, "wallet-step-up-2", "wallet-nonce-2")
	action, sawGoogle, sawPostBroker = reachRealOTP(t, client, second)
	// The production max_age=0 request requires full reauthentication. In
	// Keycloak 26.7 CookieAuthenticator resets LoA when OIDCLoginProtocol finds
	// auth_time expired, so Google LoA1 and post-broker OTP must run again.
	if !sawGoogle || !sawPostBroker {
		t.Fatalf("second wallet authorization skipped required reauthentication: Google=%v post-broker=%v", sawGoogle, sawPostBroker)
	}
	callback = submitRealOTP(t, client, action, realTOTP(realTestOTPSecret, time.Now()))
	if callback.Host != "api.noebs.sd" || callback.Path != "/wallet/authorizations/oauth/callback" ||
		callback.Query().Get("state") != "wallet-step-up-2" || callback.Query().Get("code") == "" || callback.Query().Get("error") != "" {
		t.Fatal("second wallet authorization returned an invalid callback")
	}
	secondAuthTime := exchangeRealAuthorizationCode(t, client, baseURL, callback.Query().Get("code"), secondVerifier, clientSecret, "wallet-nonce-2")
	if !secondAuthTime.After(firstAuthTime) || secondAuthTime.Before(secondStarted) {
		t.Fatal("second wallet authorization retained the previous authentication time instead of the fresh OTP time")
	}
	t.Log("legacy SHA256 credential completed two fresh LoA2 authorizations with advancing authentication times and required broker reauthentication")
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
	// Match the production transaction-authorizer request. OIDC requires
	// auth_time in the ID token when max_age is supplied.
	query.Set("max_age", "0")
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
			t.Fatal("wallet step-up request failed")
		}
		body := readRealResponse(t, response)
		assertNoPasswordBody(t, body)
		if response.StatusCode == http.StatusOK {
			match := otpFormActionPattern.FindSubmatch(body)
			if len(match) != 2 {
				t.Fatal("wallet step-up returned a non-OTP page")
			}
			return html.UnescapeString(string(match[1])), sawGoogle, sawPostBroker
		}
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			t.Fatalf("wallet step-up status = %d", response.StatusCode)
		}
		next, err := response.Request.URL.Parse(response.Header.Get("Location"))
		if err != nil {
			t.Fatal("wallet step-up returned an invalid redirect")
		}
		if next.Hostname() == "api.noebs.sd" {
			t.Fatal("wallet authorization completed without OTP")
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
		t.Fatal("wallet OTP submission request failed")
	}
	for range 12 {
		body := readRealResponse(t, response)
		assertNoPasswordBody(t, body)
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			t.Fatalf("OTP submission status = %d", response.StatusCode)
		}
		next, err := response.Request.URL.Parse(response.Header.Get("Location"))
		if err != nil {
			t.Fatal("wallet OTP submission returned an invalid redirect")
		}
		if next.Hostname() == "api.noebs.sd" {
			return next
		}
		response, err = client.Get(next.String())
		if err != nil {
			t.Fatal("wallet OTP completion request failed")
		}
	}
	t.Fatal("OTP submission exceeded redirect limit")
	return nil
}

func exchangeRealAuthorizationCode(t *testing.T, client *http.Client, baseURL, code, verifier, clientSecret, expectedNonce string) time.Time {
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
		t.Fatal("wallet authorization code exchange request failed")
	}
	body := readRealResponse(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authorization code exchange status = %d", response.StatusCode)
	}
	var tokens struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokens); err != nil {
		t.Fatal("authorization code exchange returned malformed token JSON")
	}
	ctx := oidc.ClientContext(context.Background(), client)
	issuer := baseURL + "/realms/noebs"
	idTokens := oidc.NewVerifier(issuer, oidc.NewRemoteKeySet(ctx, issuer+"/protocol/openid-connect/certs"), &oidc.Config{
		ClientID: walletAuthorizerClientID, SupportedSigningAlgs: []string{"RS256"},
	})
	verified, err := idTokens.Verify(ctx, tokens.IDToken)
	if err != nil || verified.Subject == "" || verified.Nonce != expectedNonce || len(verified.Audience) != 1 || verified.Audience[0] != walletAuthorizerClientID {
		t.Fatal("wallet ID token failed issuer, signature, audience, subject or nonce validation")
	}
	if verified.AccessTokenHash != "" {
		if err := verified.VerifyAccessToken(tokens.AccessToken); err != nil {
			t.Fatal("wallet ID token access-token hash differs from the issued access token")
		}
	}
	var claims struct {
		ACR                string `json:"acr"`
		AuthorizedParty    string `json:"azp"`
		AuthenticationTime int64  `json:"auth_time"`
		IssuedAt           int64  `json:"iat"`
	}
	if err := verified.Claims(&claims); err != nil {
		t.Fatal("wallet ID token claims are malformed")
	}
	if claims.ACR != googleTOTPACR || claims.AuthorizedParty != walletAuthorizerClientID {
		t.Fatal("wallet ID token lacks the required authorizer and LoA2 binding")
	}
	authenticationTime := time.Unix(claims.AuthenticationTime, 0)
	issuedAt := time.Unix(claims.IssuedAt, 0)
	now := time.Now()
	if claims.AuthenticationTime <= 0 || claims.IssuedAt <= 0 || authenticationTime.After(issuedAt) ||
		authenticationTime.After(now.Add(5*time.Second)) || now.Sub(authenticationTime) > 30*time.Second {
		t.Fatal("wallet ID token lacks a valid fresh authentication time")
	}
	parts := strings.Split(tokens.AccessToken, ".")
	if len(parts) != 3 {
		t.Fatal("wallet authorization returned an invalid access token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	var accessClaims map[string]json.RawMessage
	if err != nil || json.Unmarshal(payload, &accessClaims) != nil {
		t.Fatal("wallet authorization returned malformed access-token claims")
	}
	if _, exists := accessClaims["auth_time"]; exists {
		t.Fatal("payment authentication time unexpectedly expanded to access-token claims")
	}
	return authenticationTime
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
	if len(mappers) != 1 || !mapperMatches(mappers[0], authenticationTimeMapper()) {
		t.Fatal("wallet authorizer does not have exactly the managed ID-token authentication-time mapper")
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
	realm.OTPPolicyAlgorithm = "HmacSHA256"
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
