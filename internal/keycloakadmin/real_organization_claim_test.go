package keycloakadmin

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/oidcauth"
	"github.com/adonese/noebs/internal/tenantauth"
)

const (
	realGoogleUserEmail = "wallet-authorizer@example.invalid"
	realMobileClientID  = "noebs-mobile"
	realMobileRedirect  = "https://api.noebs.sd/mobile/oauth/callback"
)

func TestRealKeycloak26_7OrganizationClaim(t *testing.T) {
	baseURL := os.Getenv("NOEBS_TEST_KEYCLOAK_URL")
	if baseURL == "" {
		t.Skip("NOEBS_TEST_KEYCLOAK_URL is not set")
	}
	secret := os.Getenv("NOEBS_TEST_KEYCLOAK_BOOTSTRAP_SECRET")
	caPath := os.Getenv("NOEBS_TEST_KEYCLOAK_CA")
	googleAddress := os.Getenv("NOEBS_TEST_GOOGLE_ADDRESS")
	if secret == "" || caPath == "" || googleAddress == "" {
		t.Fatal("NOEBS_TEST_KEYCLOAK_BOOTSTRAP_SECRET, NOEBS_TEST_KEYCLOAK_CA, and NOEBS_TEST_GOOGLE_ADDRESS are required")
	}
	ca, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("test Keycloak CA is not PEM")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    roots,
	}}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err == nil && (host == "accounts.google.com" || host == "oauth2.googleapis.com" || host == "www.googleapis.com" || host == "openidconnect.googleapis.com") {
			address = googleAddress
		}
		return dialer.DialContext(ctx, network, address)
	}
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	config := validTestConfig(baseURL)
	config.ClientSecret = secret
	bootstrap, err := New(config, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := bootstrap.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("real organization authority reconcile: %v", err)
	}
	config.AdminRealm = state.Realm.Name
	config.ClientID = state.ReconcilerClient.ClientID
	config.ClientSecret = config.ClientCredentials[state.ReconcilerClient.Credential].ClientSecret
	steady, err := New(config, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	result, err := steady.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("real organization authority steady reconcile: %v", err)
	}
	if result.Changed() {
		t.Fatalf("real organization authority steady reconcile = %#v", result)
	}
	seedRealGoogleUser(t, steady, state)
	assertRealOrganizationAccessToken(t, baseURL, transport, steady, state)
}

func assertRealOrganizationAccessToken(
	t *testing.T,
	baseURL string,
	transport http.RoundTripper,
	reconciler *Reconciler,
	state DesiredState,
) {
	t.Helper()
	ctx := context.Background()
	subject, err := reconciler.LookupSubjectByEmail(ctx, realGoogleUserEmail)
	if err != nil {
		t.Fatal(err)
	}
	desired := Memberships{
		APIVersion: MembershipsAPIVersion,
		Subject:    subject,
		Memberships: []TenantMembership{
			{Tenant: "tenant-cutover", Class: MembershipClassTenantAdmin},
			{Tenant: "tenant-sandbox", Class: MembershipClassUser},
		},
	}
	actions, err := reconciler.AssignMemberships(ctx, state, desired, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 || actions[0].Tenant != "tenant-cutover" || actions[0].Action != MembershipActionAdd ||
		actions[1].Tenant != "tenant-sandbox" || actions[1].Action != MembershipActionAdd {
		t.Fatalf("real membership actions = %#v", actions)
	}

	verifier := "noebs-real-keycloak-organization-pkce-verifier-0000000001"
	authorization := realOrganizationAuthorizationURL(t, baseURL, verifier)
	callback, sawGoogle, sawPostBroker := reachRealOrganizationCallback(t, transport, authorization)
	if !sawGoogle || !sawPostBroker {
		t.Fatalf("organization authorization: Google=%v post-broker=%v", sawGoogle, sawPostBroker)
	}
	accessToken := exchangeRealMobileAuthorizationCode(t, transport, baseURL, callback.Query().Get("code"), verifier)

	topology, err := readMembershipTopology(ctx, mustRealAdminSession(t, reconciler), state)
	if err != nil {
		t.Fatal(err)
	}
	wantAccess := map[string][]string{
		"tenant-cutover": desiredOrganizationGroupRoles(t, state, "tenant-cutover", MembershipClassTenantAdmin),
		"tenant-sandbox": desiredOrganizationGroupRoles(t, state, "tenant-sandbox", MembershipClassUser),
	}
	assertRealOrganizationWireClaim(t, accessToken, topology, wantAccess)

	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	verified, err := oidcauth.NewRemoteVerifier(oidcauth.RuntimeConfig{
		Issuer:                           baseURL + "/realms/" + state.Realm.Name,
		JWKSURL:                          baseURL + "/realms/" + state.Realm.Name + "/protocol/openid-connect/certs",
		Audience:                         state.ResourceClient.ClientID,
		AllowedClients:                   []string{realMobileClientID},
		AccessTokenType:                  "Bearer",
		MaxFutureIssuedAtSeconds:         5,
		JWKSRefreshSeconds:               300,
		UnknownKeyRefreshIntervalSeconds: 30,
	}, client, oidcauth.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verified.VerifyAccessToken(ctx, accessToken)
	if err != nil {
		t.Fatalf("verify real organization access token: %v", err)
	}
	identity := claims.Identity()
	if identity.Subject != subject || identity.AuthorizedParty != realMobileClientID {
		t.Fatalf("verified real identity = %+v", identity)
	}

	cutover, err := tenantauth.Authorize(claims, "tenant-cutover", tenantauth.RoleTenantAdmin)
	if err != nil {
		t.Fatalf("authorize tenant-cutover administrator: %v", err)
	}
	if cutover.OrganizationID() != topology["tenant-cutover"].representation.ID ||
		!slices.Equal(cutover.Roles(), []tenantauth.Role{tenantauth.RoleTenantAdmin}) ||
		cutover.HasRole(tenantauth.RoleUser) {
		t.Fatalf("tenant-cutover principal: organization=%q roles=%v", cutover.OrganizationID(), cutover.Roles())
	}
	approve, err := tenantauth.NewPermissionPolicy(tenantauth.PermissionWalletWorkflowApprove, tenantauth.RoleTenantAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := approve.Authorize(claims, "tenant-cutover"); err != nil {
		t.Fatalf("authorize tenant-cutover approval: %v", err)
	}

	sandbox, err := tenantauth.Authorize(claims, "tenant-sandbox", tenantauth.RoleUser)
	if err != nil {
		t.Fatalf("authorize tenant-sandbox user: %v", err)
	}
	if sandbox.OrganizationID() != topology["tenant-sandbox"].representation.ID ||
		!slices.Equal(sandbox.Roles(), []tenantauth.Role{tenantauth.RoleUser}) || len(sandbox.Permissions()) != 0 {
		t.Fatalf("tenant-sandbox principal: organization=%q roles=%v permissions=%v", sandbox.OrganizationID(), sandbox.Roles(), sandbox.Permissions())
	}
	if _, err := tenantauth.Authorize(claims, "tenant-sandbox", tenantauth.RoleTenantAdmin); !errors.Is(err, tenantauth.ErrForbidden) {
		t.Fatalf("tenant-sandbox administrator error = %v, want forbidden", err)
	}
	if _, err := approve.Authorize(claims, "tenant-sandbox"); !errors.Is(err, tenantauth.ErrForbidden) {
		t.Fatalf("tenant-sandbox approval error = %v, want forbidden", err)
	}
}

func realOrganizationAuthorizationURL(t *testing.T, baseURL, verifier string) string {
	t.Helper()
	authorization, err := url.Parse(baseURL + "/realms/noebs/protocol/openid-connect/auth")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(verifier))
	query := authorization.Query()
	query.Set("client_id", realMobileClientID)
	query.Set("redirect_uri", realMobileRedirect)
	query.Set("response_type", "code")
	query.Set("scope", "openid organization:*")
	query.Set("state", "real-organization-state")
	query.Set("nonce", "real-organization-nonce")
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(digest[:]))
	query.Set("code_challenge_method", "S256")
	query.Set("login_hint", realGoogleUserEmail)
	authorization.RawQuery = query.Encode()
	return authorization.String()
}

func reachRealOrganizationCallback(t *testing.T, transport http.RoundTripper, start string) (*url.URL, bool, bool) {
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
	current := start
	sawGoogle := false
	sawPostBroker := false
	for range 20 {
		response, err := client.Get(current)
		if err != nil {
			t.Fatal(err)
		}
		body := readRealResponse(t, response)
		assertNoPasswordBody(t, body)
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			t.Fatalf("organization authorization status = %d at %s", response.StatusCode, response.Request.URL.Redacted())
		}
		next, err := response.Request.URL.Parse(response.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if response.Request.URL.Hostname() == "accounts.google.com" || next.Hostname() == "accounts.google.com" {
			sawGoogle = true
		}
		if strings.HasSuffix(response.Request.URL.Path, "/login-actions/post-broker-login") ||
			strings.HasSuffix(next.Path, "/login-actions/post-broker-login") {
			sawPostBroker = true
		}
		if next.Hostname() == "api.noebs.sd" {
			if next.Path != "/mobile/oauth/callback" || next.Query().Get("state") != "real-organization-state" || next.Query().Get("code") == "" {
				t.Fatalf("organization callback = %s", next.Redacted())
			}
			return next, sawGoogle, sawPostBroker
		}
		current = next.String()
	}
	t.Fatal("organization authorization exceeded redirect limit")
	return nil, false, false
}

func exchangeRealMobileAuthorizationCode(t *testing.T, transport http.RoundTripper, baseURL, code, verifier string) string {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	response, err := client.PostForm(baseURL+"/realms/noebs/protocol/openid-connect/token", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {realMobileClientID},
		"code":          {code},
		"redirect_uri":  {realMobileRedirect},
		"code_verifier": {verifier},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := readRealResponse(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mobile authorization code exchange status = %d: %s", response.StatusCode, body)
	}
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokens); err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken == "" {
		t.Fatal("mobile authorization code exchange returned no access token")
	}
	return tokens.AccessToken
}

type realWireOrganization struct {
	ID             string `json:"id"`
	ResourceAccess map[string]struct {
		Roles []string `json:"roles"`
	} `json:"resource_access"`
}

func assertRealOrganizationWireClaim(
	t *testing.T,
	accessToken string,
	topology membershipTopology,
	wantAccess map[string][]string,
) {
	t.Helper()
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		t.Fatal("real access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Organization map[string]realWireOrganization `json:"organization"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if len(claims.Organization) != len(wantAccess) {
		t.Fatalf("real organization claim = %#v", claims.Organization)
	}
	for tenant, wantRoles := range wantAccess {
		organization, exists := claims.Organization[tenant]
		if !exists || organization.ID != topology[tenant].representation.ID || len(organization.ResourceAccess) != 1 {
			t.Fatalf("real organization claim for %s = %#v", tenant, organization)
		}
		access, exists := organization.ResourceAccess["noebs-api"]
		if !exists {
			t.Fatalf("real organization claim for %s has resource access %#v", tenant, organization.ResourceAccess)
		}
		gotRoles := slices.Clone(access.Roles)
		slices.Sort(gotRoles)
		if !slices.Equal(gotRoles, wantRoles) {
			t.Fatalf("real organization roles for %s = %v, want %v", tenant, gotRoles, wantRoles)
		}
	}
}

func desiredOrganizationGroupRoles(t *testing.T, state DesiredState, tenant string, class MembershipClass) []string {
	t.Helper()
	for _, organization := range state.Organizations {
		if organization.Alias != tenant {
			continue
		}
		for _, group := range organization.Groups {
			if group.Name == string(class) {
				roles := slices.Clone(group.ClientRoles)
				slices.Sort(roles)
				return roles
			}
		}
	}
	t.Fatalf("desired organization group %s/%s does not exist", tenant, class)
	return nil
}

func mustRealAdminSession(t *testing.T, reconciler *Reconciler) *adminSession {
	t.Helper()
	session, err := reconciler.session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return session
}
