package keycloakadmin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"html"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/tenantaccess"
	"github.com/adonese/noebs/internal/tenantauth"
)

// This isolated fixture proves the deployed enroller credential can inspect
// native organization role mappings and compose/revoke only selected grants.
func TestRealKeycloak26_7TenantAccess(t *testing.T) {
	baseURL := os.Getenv("NOEBS_TEST_KEYCLOAK_URL")
	if baseURL == "" {
		t.Skip("NOEBS_TEST_KEYCLOAK_URL is not set")
	}
	secret, caPath := os.Getenv("NOEBS_TEST_KEYCLOAK_BOOTSTRAP_SECRET"), os.Getenv("NOEBS_TEST_KEYCLOAK_CA")
	if secret == "" || caPath == "" {
		t.Fatal("isolated bootstrap secret and CA required")
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid fixture CA")
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}}}
	config := validTestConfig(baseURL)
	config.ClientSecret = secret
	mailbox, smtp := startRealSMTP(t)
	config.SMTP = smtp
	bootstrap, err := New(config, client)
	if err != nil {
		t.Fatal(err)
	}
	state := membershipTestDesiredState(t)
	if _, err = bootstrap.Reconcile(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	admin := mustRealAdminSession(t, bootstrap)
	ctx := context.Background()
	userPath := realmPath("noebs") + "/users"
	if err = admin.post(ctx, userPath, map[string]any{"username": "tenant-access-fixture", "enabled": true, "email": "access@example.invalid", "emailVerified": true, "firstName": "Access", "lastName": "Fixture"}); err != nil {
		t.Fatal(err)
	}
	var users []userRepresentation
	if found, err := admin.get(ctx, userPath+"?exact=true&username=tenant-access-fixture", &users); err != nil || !found || len(users) != 1 {
		t.Fatalf("fixture lookup: %v", err)
	}
	subject := users[0].ID
	t.Cleanup(func() {
		if err := admin.delete(context.Background(), userPath+"/"+url.PathEscape(subject), nil); err != nil {
			t.Error(err)
		}
	})
	policy := accountenrollment.RuntimeConfig{Enabled: true, Issuer: baseURL + "/realms/noebs", TenantIDs: []string{"noebs"}, AllowedSubjects: []string{subject}, KeycloakBaseURL: baseURL, KeycloakClientID: state.EnrollmentClient.ClientID, KeycloakClientSecret: config.ClientCredentials[state.EnrollmentClient.Credential].ClientSecret}
	authority, err := NewAccessAuthority(policy, state.tenantCatalog, client)
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := authority.SubjectExists(ctx, subject); err != nil || !exists {
		t.Fatalf("dedicated credential exact subject: %t %v", exists, err)
	}
	initial, err := authority.Account(ctx, "noebs", subject)
	if err != nil || initial.Member || initial.Email != "" {
		t.Fatalf("dedicated credential native topology/nonmember: %+v %v", initial, err)
	}
	t.Log("dedicated manage-organizations/manage-users credential read native group mappings; no permission expansion")
	if err = authority.ApplyDelta(ctx, "tenant-sandbox", subject, []tenantauth.Role{tenantauth.RoleBackoffice}, []tenantauth.Role{}); err != nil {
		t.Fatal(err)
	}
	for _, roles := range [][]tenantauth.Role{{tenantauth.RoleUser}, {tenantauth.RoleTenantAdmin}, {tenantauth.RoleBackoffice}} {
		if err = authority.ApplyDelta(ctx, "noebs", subject, roles, []tenantauth.Role{}); err != nil {
			t.Fatalf("grant %v: %v", roles, err)
		}
	}
	composed, err := authority.Account(ctx, "noebs", subject)
	if err != nil || !slices.Equal(composed.Roles, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleTenantAdmin, tenantauth.RoleUser}) {
		t.Fatalf("composed roles=%+v %v", composed, err)
	}
	members, err := authority.Members(ctx, "noebs")
	if err != nil || len(members) != 1 || members[0].Subject != subject || !slices.Equal(members[0].Roles, composed.Roles) {
		t.Fatalf("selected member list=%+v %v", members, err)
	}
	// Safety guards must receive every member's authoritative roles, including
	// two independently granted administrators, rather than a profile-only list.
	if err = admin.post(ctx, userPath, map[string]any{"username": "tenant-access-second-admin", "enabled": true}); err != nil {
		t.Fatal(err)
	}
	var secondUsers []userRepresentation
	if found, err := admin.get(ctx, userPath+"?exact=true&username=tenant-access-second-admin", &secondUsers); err != nil || !found || len(secondUsers) != 1 {
		t.Fatal("second administrator fixture lookup failed")
	}
	secondSubject := secondUsers[0].ID
	t.Cleanup(func() {
		if err := admin.delete(context.Background(), userPath+"/"+url.PathEscape(secondSubject), nil); err != nil {
			t.Error(err)
		}
	})
	if err = authority.ApplyDelta(ctx, "noebs", secondSubject, []tenantauth.Role{tenantauth.RoleTenantAdmin}, []tenantauth.Role{}); err != nil {
		t.Fatal(err)
	}
	members, err = authority.Members(ctx, "noebs")
	if err != nil || len(members) != 2 {
		t.Fatalf("two administrator listing: %+v %v", members, err)
	}
	for _, member := range members {
		if !slices.Contains(member.Roles, tenantauth.RoleTenantAdmin) {
			t.Fatalf("member list omitted authoritative administrator role: %+v", member)
		}
	}
	if err = admin.put(ctx, userPath+"/"+url.PathEscape(secondSubject), map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	members, err = authority.Members(ctx, "noebs")
	if err != nil || len(members) != 2 {
		t.Fatalf("disabled administrator listing: %+v %v", members, err)
	}
	for _, member := range members {
		if member.Disabled != (member.Subject == secondSubject) {
			t.Fatalf("native member enabled status was lost: %+v", member)
		}
	}
	if err = admin.put(ctx, userPath+"/"+url.PathEscape(secondSubject), map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	t.Log("native selected-member enabled status distinguishes disabled administrator survivors")
	if err = authority.ApplyDelta(ctx, "noebs", subject, []tenantauth.Role{}, []tenantauth.Role{tenantauth.RoleTenantAdmin}); err != nil {
		t.Fatal(err)
	}
	after, err := authority.Account(ctx, "noebs", subject)
	if err != nil || !slices.Equal(after.Roles, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleUser}) {
		t.Fatalf("user role not preserved=%+v %v", after, err)
	}
	if err = authority.ApplyDelta(ctx, "noebs", subject, []tenantauth.Role{}, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleUser}); err != nil {
		t.Fatal(err)
	}
	empty, err := authority.Account(ctx, "noebs", subject)
	if err != nil || !empty.Member || len(empty.Roles) != 0 {
		t.Fatalf("empty grant membership=%+v %v", empty, err)
	}
	unrelated, err := authority.Account(ctx, "tenant-sandbox", subject)
	if err != nil || !slices.Equal(unrelated.Roles, []tenantauth.Role{tenantauth.RoleBackoffice}) {
		t.Fatalf("other tenant changed=%+v %v", unrelated, err)
	}
	if err = authority.ApplyDelta(ctx, "noebs", subject, []tenantauth.Role{}, []tenantauth.Role{tenantauth.RoleUser}); err != nil {
		t.Fatalf("repeat revocation: %v", err)
	}
	t.Log("native additive composition, selective revocation, retained empty membership, cross-tenant preservation and retry passed")
	actor, err := authority.ServiceAccountSubject(ctx)
	if err != nil || accessSubject(actor) != nil {
		t.Fatalf("verified service-token subject: %v", err)
	}
	// A matching preclaimed handle/email is not operation-owned and must not
	// be adopted implicitly, even when it currently has no tenant membership.
	if err = admin.post(ctx, userPath, map[string]any{"username": BootstrapOperatorUsername, "email": BootstrapOperatorEmail, "enabled": true, "emailVerified": false}); err != nil {
		t.Fatal(err)
	}
	var preclaims []operatorIdentity
	if found, err := admin.get(ctx, userPath+"?exact=true&username="+BootstrapOperatorUsername, &preclaims); err != nil || !found || len(preclaims) != 1 {
		t.Fatal("preclaim lookup failed")
	}
	if _, err = authority.PrepareOperator(ctx, "noebs", BootstrapOperatorEmail, "", "22222222-2222-4222-8222-222222222222"); !errors.Is(err, tenantaccess.ErrOperationConflict) {
		t.Fatalf("preclaimed identity adopted: %v", err)
	}
	if err = admin.delete(ctx, userPath+"/"+url.PathEscape(preclaims[0].ID), nil); err != nil {
		t.Fatal(err)
	}
	operator, err := authority.PrepareOperator(ctx, "noebs", BootstrapOperatorEmail, "", "22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatalf("prepare standalone operator: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.delete(context.Background(), userPath+"/"+url.PathEscape(operator), nil); err != nil {
			t.Error(err)
		}
	})
	inspectedOperator, err := authority.enrollment.Inspect(ctx, operator)
	if err != nil || inspectedOperator.BootstrapOperationID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("authoritative bootstrap operation proof missing: %v", err)
	}
	repeated, err := authority.PrepareOperator(ctx, "noebs", BootstrapOperatorEmail, "", "22222222-2222-4222-8222-222222222222")
	if err != nil || repeated != operator {
		t.Fatalf("operator retry: %v", err)
	}
	var user operatorIdentity
	if found, err := admin.get(ctx, userPath+"/"+url.PathEscape(operator), &user); err != nil || !found || user.EmailVerified || user.Username != BootstrapOperatorUsername {
		t.Fatalf("operator identity altered: %+v %v", user, err)
	}
	for _, action := range operatorSetupActions {
		if !slices.Contains(user.RequiredActions, action) {
			t.Fatalf("missing hosted action %s", action)
		}
	}
	var credentials []map[string]any
	if found, err := admin.get(ctx, userPath+"/"+url.PathEscape(operator)+"/credentials", &credentials); err != nil || !found || len(credentials) != 0 {
		t.Fatalf("bootstrap introduced credentials: %v", err)
	}
	// An in-progress preparation may add missing managed actions, while
	// preserving unrelated required actions and the operation marker/profile.
	pendingActions := []string{"UPDATE_PASSWORD", "CONFIGURE_TOTP", "UPDATE_PROFILE"}
	if err = admin.put(ctx, userPath+"/"+url.PathEscape(operator), map[string]any{"firstName": "Preserved", "requiredActions": pendingActions}); err != nil {
		t.Fatal(err)
	}
	if _, err = authority.PrepareOperator(ctx, "noebs", BootstrapOperatorEmail, operator, "22222222-2222-4222-8222-222222222222"); err != nil {
		t.Fatalf("partial required-action update: %v", err)
	}
	var preserved map[string]any
	if found, err := admin.get(ctx, userPath+"/"+url.PathEscape(operator), &preserved); err != nil || !found || preserved["firstName"] != "Preserved" || preserved["emailVerified"] != false {
		t.Fatal("required-action update changed profile/verification")
	}
	if found, err := admin.get(ctx, userPath+"/"+url.PathEscape(operator), &user); err != nil || !found || !slices.Contains(user.RequiredActions, "UPDATE_PROFILE") {
		t.Fatal("required-action update lost existing action")
	}
	if err = admin.put(ctx, userPath+"/"+url.PathEscape(operator), map[string]any{"requiredActions": operatorSetupActions}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(user.Attributes[bootstrapOperationAttribute], []string{"22222222-2222-4222-8222-222222222222"}) {
		t.Fatal("operation ownership marker was not persisted")
	}
	if _, err = authority.PrepareOperator(ctx, "noebs", BootstrapOperatorEmail, "", "33333333-3333-4333-8333-333333333333"); !errors.Is(err, tenantaccess.ErrOperationConflict) {
		t.Fatalf("other bootstrap operation adopted identity: %v", err)
	}
	if err = authority.SendOperatorSetup(ctx, operator, state.BackofficeOrigin); err != nil {
		t.Fatalf("native hosted setup email: %v", err)
	}
	browser := realLocalBrowser(t, client.Transport)
	page, body := realLocalGet(t, browser, realLocalEmailLink(t, mailbox, baseURL))
	passwordSet, totpSet := false, false
	for steps := 0; !realAccessIsConfiguredCallback(t, page) && steps < 8; steps++ {
		switch {
		case strings.Contains(string(body), `id="kc-passwd-update-form"`):
			page, body = realLocalPost(t, browser, realLocalForm(t, page, body, "kc-passwd-update-form"), url.Values{"password-new": {"Operator-owned-fixture-73591"}, "password-confirm": {"Operator-owned-fixture-73591"}})
			passwordSet = true
		case strings.Contains(string(body), `id="kc-totp-settings-form"`):
			secret := realLocalMatch(t, body, `<input[^>]*name="totpSecret"[^>]*value="([^"]+)"`, "operator OTP secret")
			page, body = realLocalPost(t, browser, realLocalForm(t, page, body, "kc-totp-settings-form"), url.Values{"totp": {realLocalTOTP(secret, time.Now())}, "totpSecret": {secret}, "userLabel": {"owned-operator-fixture"}})
			totpSet = true
		case strings.Contains(string(body), "verify-email") || strings.Contains(string(body), "verification link"):
			page, body = realLocalGet(t, browser, realLocalEmailLink(t, mailbox, baseURL))
		default:
			links := regexp.MustCompile(`<a[^>]*href="([^"]+)"[^>]*>([^<]+)</a>`).FindAllSubmatch(body, -1)
			next := ""
			for _, link := range links {
				target := html.UnescapeString(string(link[1]))
				if target == state.BackofficeOrigin+"/backoffice/setup-complete" {
					page, err = url.Parse(target)
					if err != nil {
						t.Fatal(err)
					}
					next = target
					break
				}
				if strings.Contains(strings.ToLower(string(link[2])), "proceed") {
					next = target
				}
			}
			if realAccessIsConfiguredCallback(t, page) {
				continue
			}
			if next == "" {
				t.Fatalf("operator setup stopped at %s: %s", page.Path, realLocalPageSummary(body))
			}
			page, body = realLocalGet(t, browser, realLocalURL(t, page, next))
		}
	}
	if page.String() != state.BackofficeOrigin+"/backoffice/setup-complete" || !passwordSet || !totpSet {
		t.Fatalf("operator hosted setup did not finish expected stages at %s", page.Path)
	}
	if found, err := admin.get(ctx, userPath+"/"+url.PathEscape(operator), &user); err != nil || !found || !user.EmailVerified || len(user.RequiredActions) != 0 {
		t.Fatal("native operator setup did not complete required actions")
	}
	if resolved, err := authority.ResolveOperator(ctx, "noebs", BootstrapOperatorEmail, operator); err != nil || resolved != operator {
		t.Fatalf("read-only completed setup identity: %v", err)
	}
	if err = authority.SendOperatorSetup(ctx, operator, state.BackofficeOrigin); err != nil {
		t.Fatal(err)
	}
	select {
	case <-mailbox:
		t.Fatal("completed setup resend dispatched reset actions")
	default:
	}
	if found, err := admin.get(ctx, userPath+"/"+url.PathEscape(operator), &user); err != nil || !found || len(user.RequiredActions) != 0 {
		t.Fatal("completed setup resend reset actions")
	}
	t.Log("native hosted email verification/password/TOTP completed at private terminal; operation-owned resume and completed resend preserved state")
	if err = authority.ApplyDelta(ctx, "noebs", operator, []tenantauth.Role{tenantauth.RoleUser}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = authority.PrepareOperator(ctx, "noebs", BootstrapOperatorEmail, operator, "22222222-2222-4222-8222-222222222222"); !errors.Is(err, tenantaccess.ErrForbidden) {
		t.Fatalf("consumer identity accepted as operator: %v", err)
	}
	t.Log("native operator preparation preserved unverified email and empty credentials, queued hosted requirements, rejected consumer role, and cryptographically verified service actor")

}

func realAccessIsConfiguredCallback(t *testing.T, target *url.URL) bool {
	t.Helper()
	if target == nil || target.User != nil || target.Fragment != "" {
		return false
	}
	callback := *target
	callback.RawQuery = ""
	for _, client := range repositoryDesiredState(t).InteractiveClients {
		for _, allowed := range client.RedirectURIs {
			if callback.String() == allowed {
				return true
			}
		}
	}
	return false
}
