package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/gofiber/fiber/v2"
)

const accountWebTestToken = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type accountWebTestSession struct {
	claims    tenantauth.Claims
	starts    int
	callbacks int
	revoked   bool
}

func (s *accountWebTestSession) BeginLogin(_ context.Context, path string) (backofficeauth.LoginStart, error) {
	if path != accountWebHomePath {
		return backofficeauth.LoginStart{}, errors.New("unexpected return")
	}
	s.starts++
	return backofficeauth.LoginStart{AuthorizationURL: "https://identity.example/auth?client_id=noebs-web&code_challenge=pkce&code_challenge_method=S256", FlowCookie: &http.Cookie{Name: "__Host-noebs_account_flow", Value: accountWebTestToken, Secure: true, HttpOnly: true}}, nil
}
func (s *accountWebTestSession) CompleteLogin(context.Context, string, string, string) (backofficeauth.LoginComplete, error) {
	s.callbacks++
	return backofficeauth.LoginComplete{}, errors.New("invalid callback")
}
func (s *accountWebTestSession) Authenticate(context.Context, string) (backofficeauth.AuthenticatedSession, error) {
	if s.revoked {
		return backofficeauth.AuthenticatedSession{}, backofficeauth.ErrSessionRevoked
	}
	return backofficeauth.AuthenticatedSession{Claims: s.claims, CSRFToken: accountWebTestToken}, nil
}
func (s *accountWebTestSession) Logout(context.Context, string) (backofficeauth.LogoutComplete, error) {
	return backofficeauth.LogoutComplete{}, errors.New("unused")
}

type accountWebTestProfiles struct {
	exists            bool
	resolves, creates int
	tenant, fullname  string
}

func (p *accountWebTestProfiles) Resolve(_ context.Context, principal tenantauth.Principal, _, _ string) (int64, error) {
	p.resolves++
	p.tenant = principal.Tenant()
	if !p.exists {
		return 0, errProfileProjectionNotFound
	}
	return 7, nil
}
func (p *accountWebTestProfiles) Create(_ context.Context, principal tenantauth.Principal, name, _, _ string) error {
	p.creates++
	p.tenant = principal.Tenant()
	p.fullname = name
	p.exists = true
	return nil
}

type accountWebFixture struct {
	handler  *accountWebHTTP
	session  *accountWebTestSession
	profiles *accountWebTestProfiles
	state    accountenrollment.Context
	enrolls  int
	selected string
}

func newAccountWebFixture(t *testing.T) *accountWebFixture {
	t.Helper()
	org, _ := tenantauth.NewOrganization("org-a", []tenantauth.Role{tenantauth.RoleUser}, nil)
	claims, err := tenantauth.NewClaims(tenantauth.Identity{Issuer: "https://identity.example/realms/noebs", Subject: "customer", AuthorizedParty: accountWebClientID, IssuedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)}, map[string]tenantauth.Organization{"tenant-a": org, "tenant-b": org})
	if err != nil {
		t.Fatal(err)
	}
	cookies, err := backofficeauth.NewCookiePolicy(backofficeauth.CookiePolicyConfig{FlowName: "__Host-noebs_account_flow", SessionName: "__Host-noebs_account_session"})
	if err != nil {
		t.Fatal(err)
	}
	csrf, _ := backofficeauth.NewCSRFProtector("https://api.example")
	f := &accountWebFixture{session: &accountWebTestSession{claims: claims}, profiles: &accountWebTestProfiles{}, state: accountenrollment.Context{EnrollmentStatus: "complete", Tenants: []accountenrollment.Tenant{{ID: "tenant-a", Name: "Account A"}}, SuggestedFullname: "Customer <Name>"}}
	f.handler = &accountWebHTTP{service: f.session, cookies: cookies, csrf: csrf, host: "api.example", issuer: claims.Identity().Issuer, tenantID: "tenant-a", tenantName: "Account A", profiles: f.profiles, enrollment: func(_ context.Context, identity tenantauth.Identity, tenantID, requestID, sourceIP string, enroll bool) (accountenrollment.Context, error) {
		if identity.Subject != "customer" || sourceIP != "192.0.2.1" || requestID == "" {
			t.Fatal("lost verified request identity")
		}
		f.selected = tenantID
		if enroll {
			f.enrolls++
		}
		return f.state, nil
	}}
	return f
}
func accountWebRequest(method, path string, form url.Values) *http.Request {
	r := httptest.NewRequest(method, "https://api.example"+path, strings.NewReader(form.Encode()))
	r.Host = "api.example"
	r.Header.Set("X-Forwarded-For", "192.0.2.1")
	r.AddCookie(&http.Cookie{Name: "__Host-noebs_account_session", Value: accountWebTestToken})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://api.example")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	return r.WithContext(context.WithValue(r.Context(), accountWebRequestKey{}, accountWebRequestInfo{ID: "request", SourceIP: "192.0.2.1"}))
}
func TestAccountWebResumesConfiguredAccountWithoutTenantPicker(t *testing.T) {
	f := newAccountWebFixture(t)
	w := httptest.NewRecorder()
	f.handler.home(w, accountWebRequest(http.MethodGet, accountWebHomePath, nil))
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "Customer &lt;Name&gt;") || !strings.Contains(body, "/account/profile") || strings.Contains(body, "Choose your") || strings.Contains(body, "tenant-b") || f.selected != "tenant-a" {
		t.Fatalf("incorrect profile setup: %d %s", w.Code, body)
	}
	if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self'") {
		t.Fatal("missing account security headers")
	}
	form := url.Values{"_csrf": {accountWebTestToken}, "fullname": {"  Confirmed Name  "}}
	w = httptest.NewRecorder()
	f.handler.createProfile(w, accountWebRequest(http.MethodPost, "/account/profile", form))
	if w.Code != http.StatusSeeOther || f.profiles.creates != 1 || f.profiles.tenant != "tenant-a" || f.profiles.fullname != "Confirmed Name" {
		t.Fatalf("profile result %d %+v", w.Code, f.profiles)
	}
	w = httptest.NewRecorder()
	f.handler.createProfile(w, accountWebRequest(http.MethodPost, "/account/profile", form))
	if w.Code != http.StatusSeeOther || f.profiles.creates != 1 {
		t.Fatal("profile retry duplicated creation")
	}
	w = httptest.NewRecorder()
	f.handler.home(w, accountWebRequest(http.MethodGet, accountWebHomePath, nil))
	if !strings.Contains(w.Body.String(), "Your account<br>is ready.") {
		t.Fatal("ready profile did not resume")
	}
}
func TestAccountWebRejectsChangedAccountCSRFAndCallerTenant(t *testing.T) {
	for _, kind := range []string{"csrf", "origin", "tenant", "query", "duplicates"} {
		t.Run(kind, func(t *testing.T) {
			f := newAccountWebFixture(t)
			form := url.Values{"_csrf": {accountWebTestToken}}
			if kind == "csrf" {
				form.Set("_csrf", strings.Repeat("B", 43))
			}
			if kind == "tenant" {
				form.Set("tenant_id", "tenant-b")
			}
			if kind == "duplicates" {
				form.Add("_csrf", accountWebTestToken)
			}
			r := accountWebRequest(http.MethodPost, "/account/enrollment", form)
			if kind == "origin" {
				r.Header.Set("Origin", "https://attacker.example")
			}
			if kind == "query" {
				r.URL.RawQuery = "tenant=tenant-b"
			}
			w := httptest.NewRecorder()
			f.handler.enroll(w, r)
			if w.Code < 400 || f.enrolls != 0 {
				t.Fatalf("request admitted %d enrolls %d", w.Code, f.enrolls)
			}
		})
	}
}
func TestAccountWebEnrollmentRefreshesSharedHostedIdentity(t *testing.T) {
	f := newAccountWebFixture(t)
	f.state.RefreshRequired = true
	w := httptest.NewRecorder()
	f.handler.enroll(w, accountWebRequest(http.MethodPost, "/account/enrollment", url.Values{"_csrf": {accountWebTestToken}}))
	location, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusSeeOther || f.enrolls != 1 || f.selected != "tenant-a" || f.session.starts != 1 || location.Query().Get("acr_values") != "urn:noebs:acr:primary" || location.Query().Get("client_id") != "noebs-web" {
		t.Fatalf("enrollment result %d %s", w.Code, w.Header().Get("Location"))
	}
}
func TestAccountWebCannotCreateProfileForAnotherOrRevokedTenant(t *testing.T) {
	for _, tenants := range [][]accountenrollment.Tenant{nil, {{ID: "tenant-b", Name: "Other"}}, {{ID: "tenant-a", Name: "A"}, {ID: "tenant-b", Name: "B"}}} {
		f := newAccountWebFixture(t)
		f.state.Tenants = tenants
		w := httptest.NewRecorder()
		f.handler.createProfile(w, accountWebRequest(http.MethodPost, "/account/profile", url.Values{"_csrf": {accountWebTestToken}, "fullname": {"Name"}}))
		if w.Code != http.StatusForbidden || f.profiles.creates != 0 || f.profiles.resolves != 0 {
			t.Fatalf("unexpected tenant admitted %d", w.Code)
		}
	}
}
func TestAccountWebRoutesDoNotExposeTenantOrCredentialForms(t *testing.T) {
	f := newAccountWebFixture(t)
	app := fiber.New()
	if err := registerAccountWebRoutes(app, f.handler); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/account/t/tenant-a", "/account/t/tenant-b/profile"} {
		r := accountWebRequest(http.MethodGet, path, nil)
		resp, err := app.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("tenant route mounted %s %d", path, resp.StatusCode)
		}
	}
	r := accountWebRequest(http.MethodHead, accountWebLoginPath, nil)
	resp, err := app.Test(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed || f.session.starts != 0 {
		t.Fatal("HEAD initiated login")
	}
	f.session.revoked = true
	w := httptest.NewRecorder()
	f.handler.home(w, accountWebRequest(http.MethodGet, accountWebHomePath, nil))
	if !strings.Contains(w.Body.String(), "Sign in or create account") || strings.Contains(w.Body.String(), `type="password"`) || strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("competing credential flow rendered")
	}
	w = httptest.NewRecorder()
	f.handler.login(w, accountWebRequest(http.MethodGet, accountWebLoginPath+"?return_to=https%3A%2F%2Fattacker.example", nil))
	if w.Code != http.StatusBadRequest || f.session.starts != 0 {
		t.Fatal("open redirect accepted")
	}
}
func TestAccountWebCallbackRejectsMixedAndDuplicatedResults(t *testing.T) {
	for _, query := range []string{"state=x&code=y&iss=https%3A%2F%2Fidentity.example%2Frealms%2Fnoebs&error=cancelled", "state=x&state=y&code=z&iss=https%3A%2F%2Fidentity.example%2Frealms%2Fnoebs", "state=x&code=y&iss=https%3A%2F%2Fevil.example"} {
		f := newAccountWebFixture(t)
		w := httptest.NewRecorder()
		f.handler.callback(w, accountWebRequest(http.MethodGet, accountWebCallbackPath+"?"+query, nil))
		if w.Code != http.StatusBadRequest || f.session.callbacks != 0 {
			t.Fatal("invalid callback reached session service")
		}
	}
}

func TestAccountWebDeniedAccessStillAllowsSignOut(t *testing.T) {
	f := newAccountWebFixture(t)
	f.handler.enrollment = func(context.Context, tenantauth.Identity, string, string, string, bool) (accountenrollment.Context, error) {
		return accountenrollment.Context{}, &accountEnrollmentHTTPError{Status: http.StatusForbidden, Code: "enrollment_not_eligible"}
	}
	w := httptest.NewRecorder()
	f.handler.home(w, accountWebRequest(http.MethodGet, accountWebHomePath, nil))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `action="/account/logout"`) || !strings.Contains(w.Body.String(), "does not have access") {
		t.Fatal("denied account cannot switch sign-in")
	}
}

func TestAccountWebRefreshesStaleClaimsOnlyForCurrentAuthoritativeAdmission(t *testing.T) {
	for _, admitted := range []bool{true, false} {
		t.Run(map[bool]string{true: "migrated-membership", false: "revoked-membership"}[admitted], func(t *testing.T) {
			f := newAccountWebFixture(t)
			org, _ := tenantauth.NewOrganization("previous-org", []tenantauth.Role{tenantauth.RoleUser}, nil)
			stale, err := tenantauth.NewClaims(f.session.claims.Identity(), map[string]tenantauth.Organization{"previous-tenant": org})
			if err != nil {
				t.Fatal(err)
			}
			f.session.claims = stale
			if !admitted {
				f.state.Tenants = nil
			}
			w := httptest.NewRecorder()
			f.handler.home(w, accountWebRequest(http.MethodGet, accountWebHomePath, nil))
			if admitted {
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `href="/account/login"`) || !strings.Contains(w.Body.String(), "Continue through sign-in") {
					t.Fatalf("stale claims did not offer refresh: %d", w.Code)
				}
			} else if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "Continue through sign-in") {
				t.Fatalf("revoked context offered token refresh: %d", w.Code)
			}
			if f.enrolls != 0 || f.profiles.resolves != 0 || f.profiles.creates != 0 {
				t.Fatal("stale or revoked claims reached profile or enrollment")
			}
		})
	}
}
