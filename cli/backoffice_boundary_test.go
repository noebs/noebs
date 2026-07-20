package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofiber/fiber/v2"
)

const (
	backofficeBoundaryHost    = "app.example"
	backofficeBoundaryIssuer  = "https://identity.example/realms/noebs"
	backofficeBoundarySubject = "operator-1"
)

func TestBackofficeLifecycleUsesExactCanonicalEndpoints(t *testing.T) {
	claims := backofficeBoundaryClaims(t, []backofficeBoundaryMembership{
		{
			tenant:       "tenant-a",
			organization: "org-a",
			roles:        []tenantauth.Role{tenantauth.RoleBackoffice},
			permissions: []tenantauth.Permission{
				tenantauth.PermissionReportingRead,
				tenantauth.PermissionWalletRead,
			},
		},
		{
			tenant:       "tenant-b",
			organization: "org-b",
			roles:        []tenantauth.Role{tenantauth.RoleUser},
			permissions:  []tenantauth.Permission{tenantauth.PermissionWalletRead},
		},
		{
			tenant:       "tenant-c",
			organization: "org-c",
			roles:        []tenantauth.Role{tenantauth.RoleBackoffice},
			permissions:  []tenantauth.Permission{tenantauth.PermissionWalletRead},
		},
	})
	fixture := newBackofficeBoundaryFixture(t, claims)
	app := fiber.New()
	if err := registerBackofficeLifecycleRoutes(app, fixture.handler); err != nil {
		t.Fatal(err)
	}

	wrongHost := backofficeBoundaryRequest(t, http.MethodGet, backofficeLoginPath, nil)
	wrongHost.Host = "other.example"
	response := backofficeBoundaryDo(t, app, wrongHost)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong-host login status = %d", response.StatusCode)
	}
	assertBackofficeNoStore(t, response)

	login := backofficeBoundaryRequest(t, http.MethodGet, backofficeLoginPath+"?return_to=%2Fbackoffice%2Ft%2Ftenant-a%2Fwallet", nil)
	response = backofficeBoundaryDo(t, app, login)
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d", response.StatusCode)
	}
	assertBackofficeNoStore(t, response)
	authorization, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Scheme != "https" || authorization.Host != "identity.example" ||
		authorization.Path != "/realms/noebs/protocol/openid-connect/auth" ||
		authorization.Query().Get("redirect_uri") != "https://"+backofficeBoundaryHost+backofficeCallbackPath ||
		authorization.Query().Get("scope") != "openid organization:*" {
		t.Fatalf("authorization redirect = %q", response.Header.Get("Location"))
	}
	state := authorization.Query().Get("state")
	fixture.idTokens.setNonce(authorization.Query().Get("nonce"))
	flowCookie := backofficeBoundaryCookie(t, response, backofficeFlowCookie)

	oldCallback := backofficeBoundaryRequest(t, http.MethodGet, "/backoffice/callback?state="+url.QueryEscape(state)+"&code=code", nil)
	oldCallback.AddCookie(flowCookie)
	response = backofficeBoundaryDo(t, app, oldCallback)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("old callback status = %d", response.StatusCode)
	}

	callbackQuery := url.Values{
		"state":         {state},
		"code":          {"code"},
		"iss":           {backofficeBoundaryIssuer},
		"session_state": {"session-1"},
	}.Encode()
	callback := backofficeBoundaryRequest(t, http.MethodGet, backofficeCallbackPath+"?"+callbackQuery, nil)
	callback.AddCookie(flowCookie)
	response = backofficeBoundaryDo(t, app, callback)
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/backoffice/t/tenant-a/wallet" {
		t.Fatalf("callback status/location = %d %q", response.StatusCode, response.Header.Get("Location"))
	}
	assertBackofficeNoStore(t, response)
	sessionCookie := backofficeBoundaryCookie(t, response, backofficeSessionCookie)
	if !sessionCookie.Secure || !sessionCookie.HttpOnly || sessionCookie.Path != "/" || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v", sessionCookie)
	}

	home := backofficeBoundaryRequest(t, http.MethodGet, backofficeHomePath, nil)
	home.AddCookie(sessionCookie)
	response = backofficeBoundaryDo(t, app, home)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("home status = %d", response.StatusCode)
	}
	homeBody := backofficeBoundaryBody(t, response)
	for _, expected := range []string{
		`href="/backoffice/t/tenant-a/reporting"`,
		`href="/backoffice/t/tenant-a/wallet"`,
		`action="/backoffice/logout"`,
	} {
		if !strings.Contains(homeBody, expected) {
			t.Errorf("home missing %q: %s", expected, homeBody)
		}
	}
	for _, hiddenTenant := range []string{"tenant-b", "tenant-c"} {
		if strings.Contains(homeBody, "/backoffice/t/"+hiddenTenant+"/") {
			t.Fatalf("home exposed unauthorized tenant %s: %s", hiddenTenant, homeBody)
		}
	}
	authContext, cancel := context.WithTimeout(context.Background(), backofficeRequestTTL)
	authenticated, err := fixture.handler.service.Authenticate(authContext, sessionCookie.Value)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(homeBody, `value="`+authenticated.CSRFToken+`"`) {
		t.Fatalf("home missing session CSRF token")
	}

	logoutBody := url.Values{"_csrf": {authenticated.CSRFToken}}.Encode()
	logout := backofficeBoundaryRequest(t, http.MethodPost, backofficeLogoutPath, strings.NewReader(logoutBody))
	logout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logout.Header.Set("Origin", "https://"+backofficeBoundaryHost)
	logout.Header.Set("Sec-Fetch-Site", "same-origin")
	logout.AddCookie(sessionCookie)
	response = backofficeBoundaryDo(t, app, logout)
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status = %d body=%s", response.StatusCode, backofficeBoundaryBody(t, response))
	}
	logoutLocation, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if logoutLocation.Path != "/realms/noebs/protocol/openid-connect/logout" ||
		logoutLocation.Query().Get("client_id") != backofficeClientID ||
		logoutLocation.Query().Get("id_token_hint") != "id-token" ||
		logoutLocation.Query().Get("post_logout_redirect_uri") != "https://"+backofficeBoundaryHost+backofficeLoggedOutPath {
		t.Fatalf("logout redirect = %q", response.Header.Get("Location"))
	}
	cleared := backofficeBoundaryCookie(t, response, backofficeSessionCookie)
	if cleared.MaxAge != -1 || !cleared.Secure || !cleared.HttpOnly {
		t.Fatalf("cleared session cookie = %#v", cleared)
	}

	oldLogoutCallback := backofficeBoundaryRequest(t, http.MethodGet, "/backoffice/logged-out", nil)
	response = backofficeBoundaryDo(t, app, oldLogoutCallback)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("old logout callback status = %d", response.StatusCode)
	}
	loggedOut := backofficeBoundaryRequest(t, http.MethodGet, backofficeLoggedOutPath, nil)
	response = backofficeBoundaryDo(t, app, loggedOut)
	if response.StatusCode != http.StatusOK || !strings.Contains(backofficeBoundaryBody(t, response), "You are signed out") {
		t.Fatalf("logout callback status = %d", response.StatusCode)
	}

	if !fixture.repository.allContextsBounded(backofficeRequestTTL) {
		t.Fatalf("a lifecycle repository operation escaped the %s request deadline", backofficeRequestTTL)
	}
}

func TestBackofficeCallbackPreservesUnrelatedLoginFlow(t *testing.T) {
	claims := backofficeBoundaryClaims(t, []backofficeBoundaryMembership{{
		tenant:       "tenant-a",
		organization: "org-a",
		roles:        []tenantauth.Role{tenantauth.RoleBackoffice},
		permissions:  []tenantauth.Permission{tenantauth.PermissionWalletRead},
	}})
	fixture := newBackofficeBoundaryFixture(t, claims)
	app := fiber.New()
	if err := registerBackofficeLifecycleRoutes(app, fixture.handler); err != nil {
		t.Fatal(err)
	}

	login := backofficeBoundaryRequest(t, http.MethodGet, backofficeLoginPath, nil)
	response := backofficeBoundaryDo(t, app, login)
	authorization, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := authorization.Query().Get("state")
	fixture.idTokens.setNonce(authorization.Query().Get("nonce"))
	flowCookie := backofficeBoundaryCookie(t, response, backofficeFlowCookie)

	unrelated, err := fixture.handler.service.BeginLogin(context.Background(), backofficeHomePath)
	if err != nil {
		t.Fatal(err)
	}
	unrelatedAuthorization, err := url.Parse(unrelated.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}

	invalidCallbacks := []string{
		backofficeCallbackPath + "?state=" + url.QueryEscape(state) + "&code=code",
		backofficeCallbackPath + "?state=" + url.QueryEscape(state) + "&code=code&code=duplicate&iss=" + url.QueryEscape(backofficeBoundaryIssuer),
		backofficeCallbackPath + "?state=" + url.QueryEscape(unrelatedAuthorization.Query().Get("state")) + "&code=code&iss=" + url.QueryEscape(backofficeBoundaryIssuer),
		backofficeCallbackPath + "?state=" + url.QueryEscape(state) + "&code=code&iss=https%3A%2F%2Fevil.example",
		backofficeCallbackPath + "?state=" + url.QueryEscape(state) + "&code=code&iss=" + url.QueryEscape(backofficeBoundaryIssuer) + "&session_state=",
	}
	for _, target := range invalidCallbacks {
		request := backofficeBoundaryRequest(t, http.MethodGet, target, nil)
		request.AddCookie(flowCookie)
		response = backofficeBoundaryDo(t, app, request)
		if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("invalid callback %q status = %d", target, response.StatusCode)
		}
		assertBackofficeSecurityHeaders(t, response)
		assertBackofficeCookieAbsent(t, response, backofficeFlowCookie)
	}

	callback := backofficeBoundaryRequest(
		t,
		http.MethodGet,
		backofficeCallbackPath+"?state="+url.QueryEscape(state)+"&code=code&iss="+url.QueryEscape(backofficeBoundaryIssuer)+"&session_state=session-2",
		nil,
	)
	callback.AddCookie(flowCookie)
	response = backofficeBoundaryDo(t, app, callback)
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != backofficeHomePath {
		t.Fatalf("valid callback status/location = %d %q", response.StatusCode, response.Header.Get("Location"))
	}
	cleared := backofficeBoundaryCookie(t, response, backofficeFlowCookie)
	if cleared.MaxAge != -1 {
		t.Fatalf("terminal callback flow cookie = %#v", cleared)
	}
	_ = backofficeBoundaryCookie(t, response, backofficeSessionCookie)
}

func TestBackofficeLogoutPreservesSessionUntilRequestIsValidated(t *testing.T) {
	claims := backofficeBoundaryClaims(t, []backofficeBoundaryMembership{{
		tenant:       "tenant-a",
		organization: "org-a",
		roles:        []tenantauth.Role{tenantauth.RoleBackoffice},
		permissions:  []tenantauth.Permission{tenantauth.PermissionWalletRead},
	}})
	fixture := newBackofficeBoundaryFixture(t, claims)
	sessionCookie, csrfToken := fixture.completeSession(t, claims, backofficeHomePath)
	app := fiber.New()
	if err := registerBackofficeLifecycleRoutes(app, fixture.handler); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		target     string
		csrf       string
		origin     string
		wantStatus int
	}{
		{name: "unexpected query", target: backofficeLogoutPath + "?next=/", csrf: csrfToken, origin: "https://" + backofficeBoundaryHost, wantStatus: http.StatusBadRequest},
		{name: "invalid csrf", target: backofficeLogoutPath, csrf: "invalid", origin: "https://" + backofficeBoundaryHost, wantStatus: http.StatusForbidden},
		{name: "cross origin", target: backofficeLogoutPath, csrf: csrfToken, origin: "https://evil.example", wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := url.Values{"_csrf": {test.csrf}}.Encode()
			request := backofficeBoundaryRequest(t, http.MethodPost, test.target, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			request.AddCookie(sessionCookie)
			response := backofficeBoundaryDo(t, app, request)
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}
			assertBackofficeSecurityHeaders(t, response)
			assertBackofficeCookieAbsent(t, response, backofficeSessionCookie)

			ctx, cancel := context.WithTimeout(context.Background(), backofficeRequestTTL)
			_, err := fixture.handler.service.Authenticate(ctx, sessionCookie.Value)
			cancel()
			if err != nil {
				t.Fatalf("invalid logout destroyed the session: %v", err)
			}
		})
	}

	body := url.Values{"_csrf": {csrfToken}}.Encode()
	logout := backofficeBoundaryRequest(t, http.MethodPost, backofficeLogoutPath, strings.NewReader(body))
	logout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logout.Header.Set("Origin", "https://"+backofficeBoundaryHost)
	logout.Header.Set("Sec-Fetch-Site", "same-origin")
	logout.AddCookie(sessionCookie)
	response := backofficeBoundaryDo(t, app, logout)
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("valid logout status = %d", response.StatusCode)
	}
	cleared := backofficeBoundaryCookie(t, response, backofficeSessionCookie)
	if cleared.MaxAge != -1 {
		t.Fatalf("terminal logout session cookie = %#v", cleared)
	}
}

func TestBackofficeProxyAuthenticatesScopesRewritesAndSigns(t *testing.T) {
	identityClaims := backofficeBoundaryClaims(t, []backofficeBoundaryMembership{{
		tenant:       "tenant-a",
		organization: "org-a",
		roles:        []tenantauth.Role{tenantauth.RoleBackoffice},
		permissions:  []tenantauth.Permission{tenantauth.PermissionWalletRead},
	}})
	fixture := newBackofficeBoundaryFixture(t, identityClaims)
	sessionCookie, csrfToken := fixture.completeSession(t, identityClaims, backofficeHomePath)

	captures := make(chan backofficeUpstreamCapture, 32)
	walletVerifier := newTestWorkloadVerifier(t, string(serviceRoleWalletAPI), string(serviceRoleAPIGateway))
	reportingVerifier := newTestWorkloadVerifier(t, string(serviceRoleAdminReporting), string(serviceRoleAPIGateway))
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		verifier := walletVerifier
		if strings.HasPrefix(request.URL.Path, "/dashboard") {
			verifier = reportingVerifier
		}
		principal, err := verifier.Verify(request, body)
		if err != nil {
			t.Errorf("verify rewritten upstream request %s %s: %v", request.Method, request.URL.String(), err)
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		captures <- backofficeUpstreamCapture{
			method:     request.Method,
			requestURI: request.URL.RequestURI(),
			header:     request.Header.Clone(),
			body:       string(body),
			caller:     principal.Caller,
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	previousSigners := workloadSigners
	previousTLS := internalTransportClientTLS
	workloadSigners = newTestWorkloadSigners(t, string(serviceRoleAPIGateway),
		string(serviceRoleWalletAPI), string(serviceRoleAdminReporting))
	internalTransportClientTLS = nil
	t.Cleanup(func() {
		workloadSigners = previousSigners
		internalTransportClientTLS = previousTLS
	})
	cfg := ebs_fields.NoebsConfig{ServiceDiscovery: map[string]string{
		string(serviceRoleWalletAPI):      upstream.URL,
		string(serviceRoleAdminReporting): upstream.URL,
	}}
	app := fiber.New()
	if err := registerBackofficeLifecycleRoutes(app, fixture.handler); err != nil {
		t.Fatal(err)
	}
	if err := registerBackofficeProxyRoutes(app, cfg, fixture.handler); err != nil {
		t.Fatal(err)
	}
	wrongHost := backofficeBoundaryRequest(t, http.MethodGet, "/backoffice/t/tenant-a/wallet/wallets", nil)
	wrongHost.Host = "other.example"
	response := backofficeBoundaryDo(t, app, wrongHost)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong-host proxy status = %d", response.StatusCode)
	}
	assertBackofficeFiberHeaders(t, response)
	missingSession := backofficeBoundaryRequest(t, http.MethodGet, "/backoffice/t/tenant-a/wallet/wallets", nil)
	response = backofficeBoundaryDo(t, app, missingSession)
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/backoffice/login?return_to=%2Fbackoffice%2Ft%2Ftenant-a%2Fwallet%2Fwallets" {
		t.Fatalf("missing-session redirect = %d %q", response.StatusCode, response.Header.Get("Location"))
	}
	assertBackofficeFiberHeaders(t, response)

	request := backofficeBoundaryRequest(t, http.MethodGet, "/backoffice/t/tenant-a/wallet/wallets?limit=25", nil)
	request.AddCookie(sessionCookie)
	request.Header.Set(workloadauth.HeaderRequestID, "req-backoffice-boundary-read")
	request.Header.Set("Authorization", "Bearer attacker")
	request.Header.Set("X-Active-Tenant", "tenant-b")
	request.Header.Set("X-Tenant-ID", "tenant-b")
	request.Header.Set("X-Admin-Key", "attacker")
	request.Header.Set("X-Admin-Role", "platform-admin")
	request.Header.Set(workloadauth.HeaderTenantID, "tenant-b")
	request.Header.Set(workloadauth.HeaderSignature, "attacker")
	request.Header.Set(backofficeauth.HeaderCSRFToken, "attacker")
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	response = backofficeBoundaryDo(t, app, request)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("authorized read status = %d body=%s", response.StatusCode, backofficeBoundaryBody(t, response))
	}
	assertBackofficeFiberHeaders(t, response)
	capture := backofficeBoundaryCapture(t, captures)
	if capture.method != http.MethodGet || capture.requestURI != "/admin/wallet/wallets?limit=25" || capture.caller != string(serviceRoleAPIGateway) {
		t.Fatalf("upstream request = %+v", capture)
	}
	for _, name := range []string{
		"Authorization", "Cookie", "X-Active-Tenant", "X-Tenant-ID", "X-Admin-Key", "X-Admin-Role", "X-CSRF-Token",
	} {
		if value := capture.header.Get(name); value != "" {
			t.Errorf("upstream retained %s=%q", name, value)
		}
	}
	if capture.header.Get(backofficeauth.HeaderCSRFToken) != csrfToken {
		t.Fatalf("upstream internal CSRF token was not session-derived")
	}
	principal, err := gateway.ParseInternalPrincipalIdentity(gateway.PrincipalHeaderValues{
		TenantID:        capture.header.Get(gateway.GatewayTenantIDHeader),
		Issuer:          capture.header.Get(gateway.GatewayIssuerHeader),
		Subject:         capture.header.Get(gateway.GatewaySubjectHeader),
		OrganizationID:  capture.header.Get(gateway.GatewayOrganizationIDHeader),
		AuthorizedParty: capture.header.Get(gateway.GatewayAuthorizedPartyHeader),
		Roles:           capture.header.Get(gateway.GatewayRolesHeader),
		Permission:      capture.header.Get(gateway.GatewayPermissionHeader),
		UserID:          capture.header.Get(gateway.GatewayUserIDHeader),
		SourceIP:        capture.header.Get(gateway.GatewaySourceIPHeader),
		TokenExpiresAt:  capture.header.Get(gateway.GatewayTokenExpiresAtHeader),
	}, fixture.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if principal.TenantID != "tenant-a" || principal.OrganizationID != "org-a" ||
		principal.Issuer != backofficeBoundaryIssuer || principal.Subject != backofficeBoundarySubject ||
		principal.AuthorizedParty != backofficeClientID || principal.UserID != 0 ||
		principal.SourceIP != "203.0.113.7" || !principal.HasRole(tenantauth.RoleBackoffice) ||
		principal.Permission() != tenantauth.PermissionWalletRead {
		t.Fatalf("signed upstream principal = %+v roles=%v permission=%q", principal, principal.Roles(), principal.Permission())
	}

	fixture.accessTokens.setClaims(backofficeBoundaryClaims(t, []backofficeBoundaryMembership{{
		tenant:       "tenant-a",
		organization: "org-a",
		roles:        []tenantauth.Role{tenantauth.RoleTenantAdmin},
		permissions:  []tenantauth.Permission{tenantauth.PermissionWalletManualCreate},
	}}))
	mutationBody := url.Values{"_csrf": {csrfToken}, "amount": {"100"}}.Encode()
	mutation := backofficeBoundaryRequest(t, http.MethodPost, "/backoffice/t/tenant-a/wallet/manual", strings.NewReader(mutationBody))
	mutation.AddCookie(sessionCookie)
	mutation.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mutation.Header.Set("Origin", "https://"+backofficeBoundaryHost)
	mutation.Header.Set("Sec-Fetch-Site", "same-origin")
	mutation.Header.Set(workloadauth.HeaderRequestID, "req-backoffice-boundary-write")
	mutation.Header.Set("Authorization", "Bearer attacker")
	response = backofficeBoundaryDo(t, app, mutation)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("authorized mutation status = %d body=%s", response.StatusCode, backofficeBoundaryBody(t, response))
	}
	capture = backofficeBoundaryCapture(t, captures)
	if capture.requestURI != "/admin/wallet/manual" || capture.body != mutationBody ||
		capture.header.Get("Authorization") != "" || capture.header.Get("Cookie") != "" ||
		capture.header.Get("X-CSRF-Token") != "" ||
		capture.header.Get(gateway.GatewayPermissionHeader) != string(tenantauth.PermissionWalletManualCreate) {
		t.Fatalf("mutation upstream request = %+v", capture)
	}

	for _, retired := range []string{"/dashboard", "/admin/wallet", "/admin/wallet/wallets"} {
		retiredRequest := backofficeBoundaryRequest(t, http.MethodGet, retired, nil)
		retiredRequest.AddCookie(sessionCookie)
		response = backofficeBoundaryDo(t, app, retiredRequest)
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("retired route %s status = %d", retired, response.StatusCode)
		}
	}
}

func TestBackofficeProxyEnforcesTenantRolePermissionAndCSRF(t *testing.T) {
	baseClaims := backofficeBoundaryClaims(t, []backofficeBoundaryMembership{{
		tenant:       "tenant-a",
		organization: "org-a",
		roles:        []tenantauth.Role{tenantauth.RoleBackoffice},
		permissions:  []tenantauth.Permission{tenantauth.PermissionWalletRead},
	}})
	fixture := newBackofficeBoundaryFixture(t, baseClaims)
	sessionCookie, csrfToken := fixture.completeSession(t, baseClaims, backofficeHomePath)

	upstreamCalls := make(chan string, 32)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamCalls <- request.URL.Path
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	previousSigners := workloadSigners
	workloadSigners = newTestWorkloadSigners(t, string(serviceRoleAPIGateway),
		string(serviceRoleWalletAPI), string(serviceRoleAdminReporting))
	t.Cleanup(func() { workloadSigners = previousSigners })
	app := fiber.New()
	if err := registerBackofficeProxyRoutes(app, ebs_fields.NoebsConfig{ServiceDiscovery: map[string]string{
		string(serviceRoleWalletAPI):      upstream.URL,
		string(serviceRoleAdminReporting): upstream.URL,
	}}, fixture.handler); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		membership backofficeBoundaryMembership
		csrf       bool
		wantStatus int
	}{
		{
			name: "backoffice wallet read", method: http.MethodGet, path: "/backoffice/t/tenant-a/wallet/wallets",
			membership: backofficeBoundaryMembership{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleBackoffice}, permissions: []tenantauth.Permission{tenantauth.PermissionWalletRead}},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "tenant admin wallet read", method: http.MethodGet, path: "/backoffice/t/tenant-a/wallet/wallets",
			membership: backofficeBoundaryMembership{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, permissions: []tenantauth.Permission{tenantauth.PermissionWalletRead}},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "permission does not imply operator role", method: http.MethodGet, path: "/backoffice/t/tenant-a/wallet/wallets",
			membership: backofficeBoundaryMembership{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleUser}, permissions: []tenantauth.Permission{tenantauth.PermissionWalletRead}},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "operator role does not imply permission", method: http.MethodGet, path: "/backoffice/t/tenant-a/wallet/audit",
			membership: backofficeBoundaryMembership{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, permissions: []tenantauth.Permission{tenantauth.PermissionWalletRead}},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "backoffice cannot mutate", method: http.MethodPost, path: "/backoffice/t/tenant-a/wallet/manual",
			membership: backofficeBoundaryMembership{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleBackoffice}, permissions: []tenantauth.Permission{tenantauth.PermissionWalletManualCreate}},
			csrf:       true, wantStatus: http.StatusForbidden,
		},
		{
			name: "write permission is exact", method: http.MethodPost, path: "/backoffice/t/tenant-a/wallet/fees",
			membership: backofficeBoundaryMembership{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, permissions: []tenantauth.Permission{tenantauth.PermissionWalletManualCreate}},
			csrf:       true, wantStatus: http.StatusForbidden,
		},
		{
			name: "tenant admin exact mutation", method: http.MethodPost, path: "/backoffice/t/tenant-a/wallet/fees",
			membership: backofficeBoundaryMembership{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, permissions: []tenantauth.Permission{tenantauth.PermissionWalletFeesWrite}},
			csrf:       true, wantStatus: http.StatusNoContent,
		},
		{
			name: "other tenant membership is isolated", method: http.MethodGet, path: "/backoffice/t/tenant-b/wallet/wallets",
			membership: backofficeBoundaryMembership{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, permissions: []tenantauth.Permission{tenantauth.PermissionWalletRead}},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "token tenant must exist in catalog", method: http.MethodGet, path: "/backoffice/t/tenant-c/wallet/wallets",
			membership: backofficeBoundaryMembership{tenant: "tenant-c", organization: "org-c", roles: []tenantauth.Role{tenantauth.RoleBackoffice}, permissions: []tenantauth.Permission{tenantauth.PermissionWalletRead}},
			wantStatus: http.StatusNotFound,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture.accessTokens.setClaims(backofficeBoundaryClaims(t, []backofficeBoundaryMembership{test.membership}))
			var body io.Reader
			if test.method == http.MethodPost {
				body = strings.NewReader(url.Values{"value": {"1"}}.Encode())
			}
			request := backofficeBoundaryRequest(t, test.method, test.path, body)
			request.AddCookie(sessionCookie)
			request.Header.Set(workloadauth.HeaderRequestID, fmt.Sprintf("req-backoffice-matrix-%02d", index))
			if test.method == http.MethodPost {
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if test.csrf {
				request.Header.Set("X-CSRF-Token", csrfToken)
				request.Header.Set("Origin", "https://"+backofficeBoundaryHost)
				request.Header.Set("Sec-Fetch-Site", "same-origin")
			}
			response := backofficeBoundaryDo(t, app, request)
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", response.StatusCode, test.wantStatus, backofficeBoundaryBody(t, response))
			}
			assertBackofficeFiberHeaders(t, response)
			if test.wantStatus == http.StatusNoContent {
				backofficeBoundaryCall(t, upstreamCalls)
			} else {
				assertNoBackofficeBoundaryCall(t, upstreamCalls)
			}
		})
	}

	fixture.accessTokens.setClaims(backofficeBoundaryClaims(t, []backofficeBoundaryMembership{{
		tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleTenantAdmin},
		permissions: []tenantauth.Permission{tenantauth.PermissionWalletManualCreate},
	}}))
	csrfCases := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{name: "missing", mutate: func(*http.Request) {}, want: http.StatusForbidden},
		{name: "wrong origin", mutate: func(request *http.Request) {
			request.Header.Set("X-CSRF-Token", csrfToken)
			request.Header.Set("Origin", "https://evil.example")
		}, want: http.StatusForbidden},
		{name: "exact header", mutate: func(request *http.Request) {
			request.Header.Set("X-CSRF-Token", csrfToken)
			request.Header.Set("Origin", "https://"+backofficeBoundaryHost)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
		}, want: http.StatusNoContent},
	}
	for index, test := range csrfCases {
		t.Run("csrf "+test.name, func(t *testing.T) {
			request := backofficeBoundaryRequest(t, http.MethodPost, "/backoffice/t/tenant-a/wallet/manual", strings.NewReader("value=1"))
			request.AddCookie(sessionCookie)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set(workloadauth.HeaderRequestID, fmt.Sprintf("req-backoffice-csrf-%02d", index))
			test.mutate(request)
			response := backofficeBoundaryDo(t, app, request)
			if response.StatusCode != test.want {
				t.Fatalf("status = %d, want %d body=%s", response.StatusCode, test.want, backofficeBoundaryBody(t, response))
			}
			assertBackofficeFiberHeaders(t, response)
			if test.want == http.StatusNoContent {
				backofficeBoundaryCall(t, upstreamCalls)
			} else {
				assertNoBackofficeBoundaryCall(t, upstreamCalls)
			}
		})
	}

	for _, test := range []struct {
		target string
		status int
	}{
		{target: "/backoffice/t/tenant-a/wallet/wallets?tenant_id=tenant-b", status: http.StatusBadRequest},
		{target: "/backoffice/t/TENANT-A/wallet/wallets", status: http.StatusNotFound},
	} {
		request := backofficeBoundaryRequest(t, http.MethodGet, test.target, nil)
		request.AddCookie(sessionCookie)
		response := backofficeBoundaryDo(t, app, request)
		if response.StatusCode != test.status {
			t.Errorf("non-canonical tenant selection %q status = %d, want %d", test.target, response.StatusCode, test.status)
		}
		assertBackofficeFiberHeaders(t, response)
		assertNoBackofficeBoundaryCall(t, upstreamCalls)
	}
}

func TestBackofficeRoutePermissionMatrixIsExact(t *testing.T) {
	type expectedRoute struct {
		method     string
		path       string
		upstream   string
		role       serviceRole
		permission tenantauth.Permission
		write      bool
	}
	expected := []expectedRoute{
		{http.MethodGet, "/backoffice/t/:tenant/reporting", "/dashboard", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/reporting/", "/dashboard/", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/reporting/get_tid", "/dashboard/get_tid", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/reporting/get", "/dashboard/get", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/reporting/all", "/dashboard/all", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/reporting/all/:id", "/dashboard/all/:id", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/reporting/count", "/dashboard/count", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/reporting/merchant", "/dashboard/merchant", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/reporting/status", "/dashboard/status", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/reporting/test_browser", "/dashboard/test_browser", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/reporting/stream", "/dashboard/stream", serviceRoleAdminReporting, tenantauth.PermissionReportingRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet", "/admin/wallet", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/", "/admin/wallet/", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/wallets", "/admin/wallet/wallets", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/wallets/:id", "/admin/wallet/wallets/:id", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/transactions", "/admin/wallet/transactions", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/transactions/:client_reference", "/admin/wallet/transactions/:client_reference", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/pending", "/admin/wallet/pending", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/manual", "/admin/wallet/manual", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/manual/:workflow_id", "/admin/wallet/manual/:workflow_id", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/fees", "/admin/wallet/fees", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/rates", "/admin/wallet/rates", serviceRoleWalletAPI, tenantauth.PermissionWalletRead, false},
		{http.MethodGet, "/backoffice/t/:tenant/wallet/audit", "/admin/wallet/audit", serviceRoleWalletAPI, tenantauth.PermissionWalletAuditRead, false},
		{http.MethodPost, "/backoffice/t/:tenant/wallet/manual", "/admin/wallet/manual", serviceRoleWalletAPI, tenantauth.PermissionWalletManualCreate, true},
		{http.MethodPost, "/backoffice/t/:tenant/wallet/fees", "/admin/wallet/fees", serviceRoleWalletAPI, tenantauth.PermissionWalletFeesWrite, true},
		{http.MethodPost, "/backoffice/t/:tenant/wallet/rates", "/admin/wallet/rates", serviceRoleWalletAPI, tenantauth.PermissionWalletRatesWrite, true},
		{http.MethodPost, "/backoffice/t/:tenant/wallet/approve/:workflow_id", "/admin/wallet/approve/:workflow_id", serviceRoleWalletAPI, tenantauth.PermissionWalletWorkflowApprove, true},
		{http.MethodPost, "/backoffice/t/:tenant/wallet/reject/:workflow_id", "/admin/wallet/reject/:workflow_id", serviceRoleWalletAPI, tenantauth.PermissionWalletWorkflowReject, true},
	}
	actual := backofficeRouteSpecs()
	if len(actual) != len(expected) {
		t.Fatalf("route count = %d, want %d", len(actual), len(expected))
	}
	for index, want := range expected {
		got := actual[index]
		if got.method != want.method || got.path != want.path || got.upstreamPath != want.upstream ||
			got.role != want.role || got.permission != want.permission {
			t.Errorf("route[%d] = %+v, want %+v", index, got, want)
		}
		wantRoles := backofficeReadRoles
		if want.write {
			wantRoles = backofficeWriteRoles
		}
		if !slices.Equal(got.roles, wantRoles) {
			t.Errorf("route[%d] roles = %v, want %v", index, got.roles, wantRoles)
		}
	}
}

func TestBackofficeRuntimeRequiresExactCallbackPaths(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		path string
		ok   bool
	}{
		{"oauth callback", "https://app.example" + backofficeCallbackPath, backofficeCallbackPath, true},
		{"logout callback", "https://app.example" + backofficeLoggedOutPath, backofficeLoggedOutPath, true},
		{"old oauth callback", "https://app.example/backoffice/callback", backofficeCallbackPath, false},
		{"old logout callback", "https://app.example/backoffice/logged-out", backofficeLoggedOutPath, false},
		{"callback query", "https://app.example" + backofficeCallbackPath + "?next=/", backofficeCallbackPath, false},
		{"callback fragment", "https://app.example" + backofficeCallbackPath + "#next", backofficeCallbackPath, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := requireBackofficeCallbackPath(test.raw, test.path)
			if (err == nil) != test.ok {
				t.Fatalf("requireBackofficeCallbackPath(%q, %q) error = %v", test.raw, test.path, err)
			}
		})
	}
}

type backofficeBoundaryFixture struct {
	clock        backofficeBoundaryClock
	repository   *backofficeBoundaryRepository
	accessTokens *backofficeBoundaryAccessVerifier
	idTokens     *backofficeBoundaryIDVerifier
	handler      *backofficeHTTP
}

func newBackofficeBoundaryFixture(t *testing.T, claims tenantauth.Claims) *backofficeBoundaryFixture {
	t.Helper()
	clock := backofficeBoundaryClock{now: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)}
	repository := &backofficeBoundaryRepository{
		flows:    make(map[backofficeauth.Digest]backofficeauth.FlowRecord),
		sessions: make(map[backofficeauth.Digest]backofficeauth.SessionRecord),
	}
	accessTokens := &backofficeBoundaryAccessVerifier{claims: claims}
	idTokens := &backofficeBoundaryIDVerifier{clock: clock}
	tokenServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Form.Get("grant_type") != "authorization_code" || request.Form.Get("code") != "code" {
			t.Errorf("token exchange form = %v", request.Form)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"access_token":"access-token","refresh_token":"refresh-token","refresh_expires_in":28800,"id_token":"id-token","token_type":"Bearer"}`)
	}))
	t.Cleanup(tokenServer.Close)
	oauthClient, err := backofficeauth.NewOAuthClient(backofficeauth.OAuthClientConfig{
		Issuer:            backofficeBoundaryIssuer,
		ClientID:          backofficeClientID,
		ClientSecret:      "test-client-secret",
		AuthorizationURL:  backofficeBoundaryIssuer + "/protocol/openid-connect/auth",
		TokenURL:          tokenServer.URL,
		RedirectURL:       "https://" + backofficeBoundaryHost + backofficeCallbackPath,
		EndSessionURL:     backofficeBoundaryIssuer + "/protocol/openid-connect/logout",
		PostLogoutURL:     "https://" + backofficeBoundaryHost + backofficeLoggedOutPath,
		Scopes:            []string{oidc.ScopeOpenID, "organization:*"},
		HTTPClient:        &http.Client{Timeout: 2 * time.Second},
		IDTokens:          idTokens,
		AccessTokens:      accessTokens,
		Clock:             clock,
		MaxFutureIssuedAt: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := backofficeauth.NewKeyring(backofficeauth.KeyringConfig{
		ActiveKeyID: "test-key",
		Keys:        map[string][]byte{"test-key": make([]byte, 32)},
		Entropy:     rand.Reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	cookies, err := backofficeauth.NewCookiePolicy(backofficeauth.CookiePolicyConfig{
		FlowName: backofficeFlowCookie, SessionName: backofficeSessionCookie,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := backofficeauth.NewService(backofficeauth.ServiceConfig{
		Flows: repository, Sessions: repository, OAuth: oauthClient, Keys: keyring, Cookies: cookies,
		Clock: clock, Entropy: rand.Reader, FlowTTL: backofficeFlowTTL, IdleTTL: backofficeIdleTTL,
		AbsoluteTTL: backofficeAbsoluteTTL, RefreshSkew: backofficeRefreshSkew,
		TouchInterval: backofficeTouchInterval, ReturnPathPrefix: backofficeReturnPrefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := backofficeauth.NewCSRFProtector("https://" + backofficeBoundaryHost)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{
		{ID: "tenant-a", Name: "Tenant A"},
		{ID: "tenant-b", Name: "Tenant B"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &backofficeBoundaryFixture{
		clock: clock, repository: repository, accessTokens: accessTokens, idTokens: idTokens,
		handler: &backofficeHTTP{service: service, cookies: cookies, csrf: csrf, catalog: catalog, host: backofficeBoundaryHost, issuer: backofficeBoundaryIssuer},
	}
}

func (f *backofficeBoundaryFixture) completeSession(
	t *testing.T,
	claims tenantauth.Claims,
	returnPath string,
) (*http.Cookie, string) {
	t.Helper()
	f.accessTokens.setClaims(claims)
	started, err := f.handler.service.BeginLogin(context.Background(), returnPath)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	f.idTokens.setNonce(authorization.Query().Get("nonce"))
	completed, err := f.handler.service.CompleteLogin(
		context.Background(), authorization.Query().Get("state"), started.FlowCookie.Value, "code",
	)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := f.handler.service.Authenticate(context.Background(), completed.SessionCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	return completed.SessionCookie, authenticated.CSRFToken
}

type backofficeBoundaryClock struct{ now time.Time }

func (c backofficeBoundaryClock) Now() time.Time { return c.now }

type backofficeBoundaryIDVerifier struct {
	mu    sync.Mutex
	clock backofficeBoundaryClock
	nonce string
}

func (v *backofficeBoundaryIDVerifier) setNonce(nonce string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.nonce = nonce
}

func (v *backofficeBoundaryIDVerifier) Verify(context.Context, string) (*oidc.IDToken, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return &oidc.IDToken{
		Issuer: backofficeBoundaryIssuer, Audience: []string{backofficeClientID}, Subject: backofficeBoundarySubject,
		Expiry: v.clock.Now().Add(2 * time.Hour), IssuedAt: v.clock.Now().Add(-time.Minute), Nonce: v.nonce,
	}, nil
}

type backofficeBoundaryAccessVerifier struct {
	mu     sync.RWMutex
	claims tenantauth.Claims
}

func (v *backofficeBoundaryAccessVerifier) setClaims(claims tenantauth.Claims) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.claims = claims
}

func (v *backofficeBoundaryAccessVerifier) VerifyAccessToken(context.Context, string) (tenantauth.Claims, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.claims, nil
}

type backofficeBoundaryRepository struct {
	mu        sync.Mutex
	flows     map[backofficeauth.Digest]backofficeauth.FlowRecord
	sessions  map[backofficeauth.Digest]backofficeauth.SessionRecord
	deadlines []time.Duration
}

func (r *backofficeBoundaryRepository) observe(ctx context.Context) {
	deadline, ok := ctx.Deadline()
	if !ok {
		r.deadlines = append(r.deadlines, 0)
		return
	}
	r.deadlines = append(r.deadlines, time.Until(deadline))
}

func (r *backofficeBoundaryRepository) CreateFlow(ctx context.Context, flow backofficeauth.FlowRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observe(ctx)
	if _, exists := r.flows[flow.StateHash]; exists {
		return backofficeauth.ErrFlowConflict
	}
	r.flows[flow.StateHash] = flow
	return nil
}

func (r *backofficeBoundaryRepository) ConsumeFlow(
	ctx context.Context,
	stateHash, browserHash backofficeauth.Digest,
	now time.Time,
) (backofficeauth.FlowRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observe(ctx)
	flow, exists := r.flows[stateHash]
	if !exists || flow.BrowserHash != browserHash || !flow.ExpiresAt.After(now) {
		return backofficeauth.FlowRecord{}, backofficeauth.ErrInvalidFlow
	}
	delete(r.flows, stateHash)
	return flow, nil
}

func (r *backofficeBoundaryRepository) CreateSession(ctx context.Context, session backofficeauth.SessionRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observe(ctx)
	if _, exists := r.sessions[session.SessionHash]; exists {
		return backofficeauth.ErrSessionConflict
	}
	r.sessions[session.SessionHash] = session
	return nil
}

func (r *backofficeBoundaryRepository) LoadSession(ctx context.Context, hash backofficeauth.Digest) (backofficeauth.SessionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observe(ctx)
	session, exists := r.sessions[hash]
	if !exists {
		return backofficeauth.SessionRecord{}, backofficeauth.ErrSessionNotFound
	}
	return session, nil
}

func (r *backofficeBoundaryRepository) RefreshSession(
	ctx context.Context,
	hash backofficeauth.Digest,
	clock backofficeauth.Clock,
	refreshSkew time.Duration,
	refresh backofficeauth.RefreshSessionFunc,
) (backofficeauth.SessionRecord, error) {
	r.mu.Lock()
	r.observe(ctx)
	session, exists := r.sessions[hash]
	r.mu.Unlock()
	if !exists {
		return backofficeauth.SessionRecord{}, backofficeauth.ErrSessionNotFound
	}
	if session.AccessExpiresAt.After(clock.Now().Add(refreshSkew)) {
		return session, nil
	}
	updated, err := refresh(ctx, session)
	if err != nil {
		return backofficeauth.SessionRecord{}, err
	}
	session.Tokens = updated.Tokens
	session.AccessExpiresAt = updated.AccessExpiresAt
	session.RefreshExpiresAt = updated.RefreshExpiresAt
	r.mu.Lock()
	r.sessions[hash] = session
	r.mu.Unlock()
	return session, nil
}

func (r *backofficeBoundaryRepository) TouchSession(
	ctx context.Context,
	hash backofficeauth.Digest,
	now, idleExpiresAt, touchBefore time.Time,
) (backofficeauth.SessionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observe(ctx)
	session, exists := r.sessions[hash]
	if !exists {
		return backofficeauth.SessionRecord{}, backofficeauth.ErrSessionNotFound
	}
	if !session.RefreshExpiresAt.After(now) || !session.IdleExpiresAt.After(now) || !session.AbsoluteExpiresAt.After(now) {
		delete(r.sessions, hash)
		return backofficeauth.SessionRecord{}, backofficeauth.ErrSessionExpired
	}
	if session.LastSeenAt.After(touchBefore) {
		return session, nil
	}
	session.LastSeenAt = now
	session.IdleExpiresAt = idleExpiresAt
	session.UpdatedAt = now
	r.sessions[hash] = session
	return session, nil
}

func (r *backofficeBoundaryRepository) DeleteSession(ctx context.Context, hash backofficeauth.Digest) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observe(ctx)
	if _, exists := r.sessions[hash]; !exists {
		return false, nil
	}
	delete(r.sessions, hash)
	return true, nil
}

func (r *backofficeBoundaryRepository) allContextsBounded(maximum time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.deadlines) == 0 {
		return false
	}
	for _, remaining := range r.deadlines {
		if remaining <= 0 || remaining > maximum {
			return false
		}
	}
	return true
}

type backofficeBoundaryMembership struct {
	tenant       string
	organization string
	roles        []tenantauth.Role
	permissions  []tenantauth.Permission
}

func backofficeBoundaryClaims(t *testing.T, memberships []backofficeBoundaryMembership) tenantauth.Claims {
	t.Helper()
	organizations := make(map[string]tenantauth.Organization, len(memberships))
	for _, membership := range memberships {
		organization, err := tenantauth.NewOrganization(membership.organization, membership.roles, membership.permissions)
		if err != nil {
			t.Fatal(err)
		}
		organizations[membership.tenant] = organization
	}
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	claims, err := tenantauth.NewClaims(tenantauth.Identity{
		Issuer: backofficeBoundaryIssuer, Subject: backofficeBoundarySubject, AuthorizedParty: backofficeClientID,
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(2 * time.Hour),
	}, organizations)
	if err != nil {
		t.Fatal(err)
	}
	return claims
}

type backofficeUpstreamCapture struct {
	method     string
	requestURI string
	header     http.Header
	body       string
	caller     string
}

func backofficeBoundaryRequest(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, body)
	request.Host = backofficeBoundaryHost
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	return request
}

func backofficeBoundaryDo(t *testing.T, app *fiber.App, request *http.Request) *http.Response {
	t.Helper()
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func backofficeBoundaryBody(t *testing.T, response *http.Response) string {
	t.Helper()
	if response.Body == nil {
		return ""
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return string(body)
}

func backofficeBoundaryCookie(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response did not set cookie %q", name)
	return nil
}

func assertBackofficeNoStore(t *testing.T, response *http.Response) {
	t.Helper()
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Pragma") != "no-cache" {
		t.Fatalf("cache headers = %q %q", response.Header.Get("Cache-Control"), response.Header.Get("Pragma"))
	}
}

func assertBackofficeSecurityHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	assertBackofficeNoStore(t, response)
	if response.Header.Get("X-Content-Type-Options") != "nosniff" ||
		response.Header.Get("Referrer-Policy") != "no-referrer" ||
		response.Header.Get("Content-Security-Policy") == "" ||
		response.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("security headers = %v", response.Header)
	}
}

func assertBackofficeFiberHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	assertBackofficeSecurityHeaders(t, response)
}

func assertBackofficeCookieAbsent(t *testing.T, response *http.Response, name string) {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			t.Fatalf("response unexpectedly set cookie %q: %#v", name, cookie)
		}
	}
}

func backofficeBoundaryCapture(t *testing.T, captures <-chan backofficeUpstreamCapture) backofficeUpstreamCapture {
	t.Helper()
	select {
	case capture := <-captures:
		return capture
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upstream capture")
		return backofficeUpstreamCapture{}
	}
}

func backofficeBoundaryCall(t *testing.T, calls <-chan string) string {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upstream call")
		return ""
	}
}

func assertNoBackofficeBoundaryCall(t *testing.T, calls <-chan string) {
	t.Helper()
	select {
	case call := <-calls:
		t.Fatalf("unauthorized request reached upstream %q", call)
	default:
	}
}

var _ backofficeauth.FlowRepository = (*backofficeBoundaryRepository)(nil)
var _ backofficeauth.SessionRepository = (*backofficeBoundaryRepository)(nil)
var _ backofficeauth.IDTokenVerifier = (*backofficeBoundaryIDVerifier)(nil)
var _ backofficeauth.AccessTokenVerifier = (*backofficeBoundaryAccessVerifier)(nil)
