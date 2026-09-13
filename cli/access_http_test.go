package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/tenantaccess"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
)

const accessTestSubject = "11111111-1111-4111-8111-111111111111"
const accessTestOperation = "22222222-2222-4222-8222-222222222222"

var accessTestExpiry = time.Now().Add(time.Hour).UTC().Truncate(time.Second)

// The existing browser fixture has a fixed July clock. Its claims are rebuilt
// with a live expiry so the real downstream middleware can verify wall time.
func accessBoundaryClaims(t *testing.T, memberships []backofficeBoundaryMembership) tenantauth.Claims {
	t.Helper()
	claims := backofficeBoundaryClaims(t, memberships)
	identity := claims.Identity()
	identity.ExpiresAt = accessTestExpiry
	organizations := map[string]tenantauth.Organization{}
	for _, m := range claims.Memberships() {
		organization, err := tenantauth.NewOrganization(m.OrganizationID, m.Roles, m.Permissions)
		if err != nil {
			t.Fatal(err)
		}
		organizations[m.TenantID] = organization
	}
	result, err := tenantauth.NewClaims(identity, organizations)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type accessHTTPFixture struct {
	account     tenantaccess.Account
	history     []tenantaccess.Audit
	changeError error
	actors      []tenantaccess.Actor
	changes     []tenantaccess.ChangeRequest
}

func (f *accessHTTPFixture) List(_ context.Context, a tenantaccess.Actor) ([]tenantaccess.Account, error) {
	f.actors = append(f.actors, a)
	return []tenantaccess.Account{f.account}, nil
}
func (f *accessHTTPFixture) Inspect(_ context.Context, a tenantaccess.Actor, target string) (tenantaccess.Account, error) {
	f.actors = append(f.actors, a)
	if target != accessTestSubject {
		return tenantaccess.Account{}, tenantaccess.ErrNotFound
	}
	return f.account, nil
}
func (f *accessHTTPFixture) History(_ context.Context, a tenantaccess.Actor, _ string) ([]tenantaccess.Audit, error) {
	f.actors = append(f.actors, a)
	return f.history, nil
}
func (f *accessHTTPFixture) Change(_ context.Context, a tenantaccess.Actor, r tenantaccess.ChangeRequest) (tenantaccess.ChangeResult, error) {
	f.actors = append(f.actors, a)
	f.changes = append(f.changes, r)
	return tenantaccess.ChangeResult{Account: f.account}, f.changeError
}

type accessBoundary struct {
	app     *fiber.App
	fixture *backofficeBoundaryFixture
	service *accessHTTPFixture
	cookie  *http.Cookie
	csrf    string
}

func newAccessBoundary(t *testing.T) *accessBoundary {
	t.Helper()
	claims := accessBoundaryClaims(t, []backofficeBoundaryMembership{{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleUser, tenantauth.RoleTenantAdmin}, permissions: []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead, tenantauth.PermissionIdentityAccessWrite}}})
	fixture := newBackofficeBoundaryFixture(t, claims)
	cookie, csrf := fixture.completeSession(t, claims, backofficeHomePath)
	service := &accessHTTPFixture{account: tenantaccess.Account{TenantID: "tenant-a", Subject: accessTestSubject, Member: true, Roles: []tenantauth.Role{tenantauth.RoleUser}, Revision: strings.Repeat("a", 64), Email: "member@example.test", Fullname: "<script>bad()</script>"}}
	internal := fiber.New(fiber.Config{ErrorHandler: gateway.JSONErrorHandler})
	internal.Use(signedWorkloadBoundary(serviceRoleIdentityAuth, newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleAPIGateway))))
	if err := registerTenantAccessRoutes(internal, service); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(adaptor.FiberApp(internal))
	t.Cleanup(upstream.Close)
	previousSigners, previousTLS := workloadSigners, internalTransportClientTLS
	workloadSigners = newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleWalletAPI), string(serviceRoleAdminReporting), string(serviceRoleIdentityAuth))
	internalTransportClientTLS = nil
	t.Cleanup(func() { workloadSigners = previousSigners; internalTransportClientTLS = previousTLS })
	app := fiber.New()
	if err := registerBackofficeProxyRoutes(app, ebs_fields.NoebsConfig{ServiceDiscovery: map[string]string{string(serviceRoleWalletAPI): upstream.URL, string(serviceRoleAdminReporting): upstream.URL, string(serviceRoleIdentityAuth): upstream.URL}}, fixture.handler); err != nil {
		t.Fatal(err)
	}
	// A direct caller cannot substitute a principal without the signed workload envelope.
	response, err := http.Get(upstream.URL + "/admin/access")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned direct access=%d", response.StatusCode)
	}
	return &accessBoundary{app: app, fixture: fixture, service: service, cookie: cookie, csrf: csrf}
}
func (b *accessBoundary) request(t *testing.T, method, path, body, contentType string, csrf bool) *http.Response {
	t.Helper()
	request := backofficeBoundaryRequest(t, method, path, strings.NewReader(body))
	request.AddCookie(b.cookie)
	request.Header.Set(workloadauth.HeaderRequestID, fmt.Sprintf("access-http-%d", time.Now().UnixNano()))
	request.Header.Set(fiber.HeaderContentType, contentType)
	if csrf {
		request.Header.Set("X-CSRF-Token", b.csrf)
		request.Header.Set("Origin", "https://"+backofficeBoundaryHost)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	// These attacker-controlled headers must be replaced from the verified session.
	request.Header.Set(workloadauth.HeaderSubject, "attacker")
	request.Header.Set(workloadauth.HeaderTenantID, "tenant-b")
	request.Header.Set("X-Active-Tenant", "tenant-b")
	return backofficeBoundaryDo(t, b.app, request)
}

func TestTenantAccessBoundaryComposedRolesAndTenantIsolation(t *testing.T) {
	b := newAccessBoundary(t)
	tests := []struct {
		name         string
		roles        []tenantauth.Role
		perms        []tenantauth.Permission
		method, path string
		csrf         bool
		status       int
	}{
		{"user alone", []tenantauth.Role{tenantauth.RoleUser}, []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead}, "GET", "/backoffice/t/tenant-a/access", false, 403},
		{"read operator is not admin", []tenantauth.Role{tenantauth.RoleBackoffice}, []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead}, "GET", "/backoffice/t/tenant-a/access", false, 403},
		{"admin still needs read permission", []tenantauth.Role{tenantauth.RoleTenantAdmin}, []tenantauth.Permission{tenantauth.PermissionWalletRead}, "GET", "/backoffice/t/tenant-a/access", false, 403},
		{"composed roles preserve access", []tenantauth.Role{tenantauth.RoleUser, tenantauth.RoleTenantAdmin}, []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead}, "GET", "/backoffice/t/tenant-a/access", false, 200},
		{"cross tenant denied", []tenantauth.Role{tenantauth.RoleTenantAdmin}, []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead}, "GET", "/backoffice/t/tenant-b/access", false, 403},
		{"read permission cannot edit", []tenantauth.Role{tenantauth.RoleTenantAdmin}, []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead}, "GET", "/backoffice/t/tenant-a/access/" + accessTestSubject + "/edit", false, 403},
		{"write permission opens edit", []tenantauth.Role{tenantauth.RoleUser, tenantauth.RoleTenantAdmin}, []tenantauth.Permission{tenantauth.PermissionIdentityAccessWrite}, "GET", "/backoffice/t/tenant-a/access/" + accessTestSubject + "/edit", false, 200},
		{"write requires csrf", []tenantauth.Role{tenantauth.RoleTenantAdmin}, []tenantauth.Permission{tenantauth.PermissionIdentityAccessWrite}, "POST", "/backoffice/t/tenant-a/access/" + accessTestSubject + "/changes", false, 403},
		{"revocation preserves user but removes operator", []tenantauth.Role{tenantauth.RoleUser}, []tenantauth.Permission{}, "GET", "/backoffice/t/tenant-a/access", false, 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b.fixture.accessTokens.setClaims(accessBoundaryClaims(t, []backofficeBoundaryMembership{{tenant: "tenant-a", organization: "org-a", roles: tc.roles, permissions: tc.perms}}))
			before := len(b.service.actors)
			response := b.request(t, tc.method, tc.path, "{}", fiber.MIMEApplicationJSON, tc.csrf)
			body := backofficeBoundaryBody(t, response)
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.StatusCode, tc.status, body)
			}
			if tc.status != 200 && len(b.service.actors) != before {
				t.Fatal("denied request reached service")
			}
			for _, actor := range b.service.actors[before:] {
				if actor.Kind != "" || actor.Subject != backofficeBoundarySubject || actor.TenantID != "tenant-a" || actor.SourceIP != "100.64.0.7" || !strings.HasPrefix(actor.RequestID, "access-http-") || !slices.Contains(actor.Roles, tenantauth.RoleTenantAdmin) {
					t.Fatalf("actor=%+v", actor)
				}
			}
		})
	}
}

func TestTenantAccessChangeStrictBoundaryAndRetry(t *testing.T) {
	b := newAccessBoundary(t)
	path := "/backoffice/t/tenant-a/access/" + accessTestSubject + "/changes"
	base := url.Values{"operation_id": {accessTestOperation}, "expected_revision": {strings.Repeat("a", 64)}, "reason": {"Restore requested operator access"}, "grant_roles": {"user", "backoffice"}}
	for _, field := range []string{"actor", "issuer", "tenant_id", "target_subject", "unexpected"} {
		t.Run("reject form "+field, func(t *testing.T) {
			values := url.Values{}
			for key, value := range base {
				values[key] = slices.Clone(value)
			}
			values.Set(field, "spoof")
			response := b.request(t, "POST", path, values.Encode(), fiber.MIMEApplicationForm, true)
			backofficeBoundaryBody(t, response)
			if response.StatusCode != 400 {
				t.Fatalf("status=%d", response.StatusCode)
			}
		})
	}
	for _, body := range []string{
		`{"operation_id":"a","operation_id":"b"}`,
		`{"operation_id":"a","Operation_Id":"b"}`, `{"Operation_Id":"a"}`, `{"GRANT_ROLES":[]}`,
		`{"actor":"spoof"}`, `{"tenant_id":"tenant-b"}`, `{"target_subject":"other"}`, `{} {}`,
		`{"grant_roles":["user","user"]}`, `{"grant_roles":["realm-admin"]}`,
	} {
		response := b.request(t, "POST", path, body, fiber.MIMEApplicationJSON, true)
		backofficeBoundaryBody(t, response)
		if response.StatusCode != 400 {
			t.Fatalf("invalid JSON=%s status=%d", body, response.StatusCode)
		}
	}
	if len(b.service.changes) != 0 {
		t.Fatal("invalid body reached service")
	}
	b.service.changeError = tenantaccess.ErrUnavailable
	response := b.request(t, "POST", path, base.Encode(), fiber.MIMEApplicationForm, true)
	body := backofficeBoundaryBody(t, response)
	if response.StatusCode != 503 || !strings.Contains(body, `name="operation_id" value="`+accessTestOperation+`"`) || !strings.Contains(body, `name="expected_revision" value="`+strings.Repeat("a", 64)+`"`) {
		t.Fatalf("retry lost terms status=%d body=%s", response.StatusCode, body)
	}
	if len(b.service.changes) != 1 || b.service.changes[0].RevokeRoles == nil || !slices.Equal(b.service.changes[0].GrantRoles, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleUser}) || b.service.changes[0].TargetSubject != accessTestSubject {
		t.Fatalf("change=%+v", b.service.changes)
	}
	b.service.changeError = nil
	base.Set("_csrf", b.csrf)
	// Browser forms submit CSRF in the body. Do not send an additional CSRF header.
	request := backofficeBoundaryRequest(t, "POST", path, strings.NewReader(base.Encode()))
	request.AddCookie(b.cookie)
	request.Header.Set("Content-Type", fiber.MIMEApplicationForm)
	request.Header.Set("Origin", "https://"+backofficeBoundaryHost)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set(workloadauth.HeaderRequestID, "access-form-retry")
	response = backofficeBoundaryDo(t, b.app, request)
	backofficeBoundaryBody(t, response)
	if response.StatusCode != 303 {
		t.Fatalf("browser form status=%d", response.StatusCode)
	}
	if len(b.service.changes) != 2 || b.service.changes[0].OperationID != b.service.changes[1].OperationID {
		t.Fatal("retry changed operation")
	}
}

func TestTenantAccessDetailHistoryAndPendingRecovery(t *testing.T) {
	b := newAccessBoundary(t)
	b.service.account.PendingOperationID = accessTestOperation
	b.service.account.EnrollmentSuppressed = true
	b.service.history = []tenantaccess.Audit{{OperationID: accessTestOperation, Subject: accessTestSubject, ActorSubject: "admin-subject", ActorSourceIP: "100.64.0.8", ActorRequestID: "original-request", RecoveryAttempts: []tenantaccess.RecoveryAttempt{{ActorSubject: "recovering-admin", SourceIP: "100.64.0.9", RequestID: "recovery-request"}}, Status: "pending", Reason: "Approved restoration <script>", ExpectedRevision: strings.Repeat("b", 64), GrantRoles: []tenantauth.Role{tenantauth.RoleBackoffice}}}
	path := "/backoffice/t/tenant-a/access/" + accessTestSubject
	response := b.request(t, "GET", path, "", "", false)
	body := backofficeBoundaryBody(t, response)
	for _, expected := range []string{"Access change history", "admin-subject", "recovering-admin", "100.64.0.8", "recovery-request", "Approved restoration &lt;script&gt;", "Signup will not restore removed user access", "&lt;script&gt;bad()&lt;/script&gt;"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %q: %s", expected, body)
		}
	}
	response = b.request(t, "GET", path+"/edit", "", "", false)
	body = backofficeBoundaryBody(t, response)
	if !strings.Contains(body, `name="operation_id" value="`+accessTestOperation+`"`) || !strings.Contains(body, `name="expected_revision" value="`+strings.Repeat("b", 64)+`"`) || strings.Contains(body, "Apply access change") {
		t.Fatalf("pending recovery form=%s", body)
	}
	// JSON reads remain subject-scoped and return the same authority data/history.
	request := backofficeBoundaryRequest(t, "GET", path, nil)
	request.AddCookie(b.cookie)
	request.Header.Set("Accept", fiber.MIMEApplicationJSON)
	request.Header.Set(workloadauth.HeaderRequestID, "access-json-read")
	response = backofficeBoundaryDo(t, b.app, request)
	defer response.Body.Close()
	var result struct {
		Account tenantaccess.Account
		History []tenantaccess.Audit
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Account.Subject != accessTestSubject || len(result.History) != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestTenantAccessExactSubjectLookupAndQueryRejection(t *testing.T) {
	b := newAccessBoundary(t)
	response := b.request(t, "POST", "/backoffice/t/tenant-a/access/lookup", url.Values{"subject": {accessTestSubject}}.Encode(), fiber.MIMEApplicationForm, true)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 303 || response.Header.Get("Location") != "/backoffice/t/tenant-a/access/"+accessTestSubject {
		t.Fatalf("lookup=%d %s", response.StatusCode, response.Header.Get("Location"))
	}
	if len(b.service.actors) != 0 {
		t.Fatal("lookup queried global authority")
	}
	for _, path := range []string{"/backoffice/t/tenant-a/access?search=realm@example.test", "/backoffice/t/tenant-a/access/" + accessTestSubject + "?tenant_id=tenant-b", "/backoffice/t/tenant-a/access/not-a-uuid"} {
		response = b.request(t, "GET", path, "", "", false)
		backofficeBoundaryBody(t, response)
		if response.StatusCode != 400 {
			t.Fatalf("query/path=%s status=%d", path, response.StatusCode)
		}
	}
}

func TestTenantAccessNeverUnionsOtherTenantAdminRights(t *testing.T) {
	b := newAccessBoundary(t)
	b.fixture.accessTokens.setClaims(accessBoundaryClaims(t, []backofficeBoundaryMembership{
		{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleUser}, permissions: []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead}},
		{tenant: "tenant-b", organization: "org-b", roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, permissions: []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead, tenantauth.PermissionIdentityAccessWrite}},
	}))
	response := b.request(t, "GET", "/backoffice/t/tenant-a/access", "", "", false)
	backofficeBoundaryBody(t, response)
	if response.StatusCode != 403 || len(b.service.actors) != 0 {
		t.Fatalf("cross-tenant role union status=%d actors=%+v", response.StatusCode, b.service.actors)
	}
}

func TestTenantAccessHomeLinkRequiresAdminAndExactReadPermission(t *testing.T) {
	for _, tc := range []struct {
		name        string
		roles       []tenantauth.Role
		permissions []tenantauth.Permission
		visible     bool
	}{
		{"composed admin", []tenantauth.Role{tenantauth.RoleUser, tenantauth.RoleTenantAdmin}, []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead}, true},
		{"read operator", []tenantauth.Role{tenantauth.RoleBackoffice}, []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead}, false},
		{"admin without read", []tenantauth.Role{tenantauth.RoleTenantAdmin}, []tenantauth.Permission{tenantauth.PermissionIdentityAccessWrite}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := accessBoundaryClaims(t, []backofficeBoundaryMembership{{tenant: "tenant-a", organization: "org-a", roles: tc.roles, permissions: tc.permissions}})
			fixture := newBackofficeBoundaryFixture(t, claims)
			cookie, _ := fixture.completeSession(t, claims, backofficeHomePath)
			app := fiber.New()
			if err := registerBackofficeLifecycleRoutes(app, fixture.handler); err != nil {
				t.Fatal(err)
			}
			request := backofficeBoundaryRequest(t, http.MethodGet, backofficeHomePath, nil)
			request.AddCookie(cookie)
			response := backofficeBoundaryDo(t, app, request)
			body := backofficeBoundaryBody(t, response)
			if response.StatusCode != 200 || strings.Contains(body, `href="/backoffice/t/tenant-a/access"`) != tc.visible {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
		})
	}
}

func TestOperatorSetupLandingStartsOnlyExistingLogin(t *testing.T) {
	claims := accessBoundaryClaims(t, []backofficeBoundaryMembership{{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, permissions: []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead}}})
	fixture := newBackofficeBoundaryFixture(t, claims)
	app := fiber.New()
	if err := registerBackofficeLifecycleRoutes(app, fixture.handler); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, path, host string
		status             int
	}{
		{"GET", backofficeSetupCompletePath, backofficeBoundaryHost, 303},
		{"GET", backofficeSetupCompletePath + "?kc_action_status=success", backofficeBoundaryHost, 303},
		{"GET", backofficeSetupCompletePath + "?kc_action_status=cancelled", backofficeBoundaryHost, 303},
		{"GET", backofficeSetupCompletePath + "?kc_action_status=success&kc_action_status=success", backofficeBoundaryHost, 400},
		{"GET", backofficeSetupCompletePath + "?redirect_uri=https://evil.example", backofficeBoundaryHost, 400},
		{"GET", backofficeSetupCompletePath, "api.noebs.sd", 404},
		{"POST", backofficeSetupCompletePath, backofficeBoundaryHost, 405},
	} {
		request := backofficeBoundaryRequest(t, test.method, test.path, nil)
		request.Host = test.host
		response := backofficeBoundaryDo(t, app, request)
		body := backofficeBoundaryBody(t, response)
		if response.StatusCode != test.status {
			t.Fatalf("%s %s status=%d want=%d body=%s", test.method, test.path, response.StatusCode, test.status, body)
		}
		if len(response.Cookies()) != 0 || fixture.repository.flowCount() != 0 {
			t.Fatal("setup landing created browser authority")
		}
		if test.status == 303 && response.Header.Get("Location") != backofficeLoginPath {
			t.Fatal("setup landing did not use canonical login")
		}
	}
}

func TestTenantAccessLastAdministratorConflictAndCompletionAudit(t *testing.T) {
	b := newAccessBoundary(t)
	b.service.changeError = tenantaccess.ErrLastAdministrator
	path := "/backoffice/t/tenant-a/access/" + accessTestSubject
	body := url.Values{"operation_id": {accessTestOperation}, "expected_revision": {strings.Repeat("a", 64)}, "reason": {"Remove operator access"}, "revoke_roles": {"tenant-admin"}}
	response := b.request(t, "POST", path+"/changes", body.Encode(), fiber.MIMEApplicationForm, true)
	if content := backofficeBoundaryBody(t, response); response.StatusCode != 409 || !strings.Contains(content, "another account before revoking the last administrator") {
		t.Fatalf("last-admin status=%d body=%s", response.StatusCode, content)
	}
	b.service.history = []tenantaccess.Audit{{OperationID: accessTestOperation, Status: "complete", ActorKind: "deployment-bootstrap", AfterRoles: []tenantauth.Role{tenantauth.RoleBackoffice}, CompletedRoles: []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleUser}}}
	response = b.request(t, "GET", path, "", "", false)
	content := backofficeBoundaryBody(t, response)
	for _, expected := range []string{"planned roles: backoffice", "Completed roles: backoffice user", "deployment-bootstrap"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("completion audit missing %q: %s", expected, content)
		}
	}
}
