package keycloakadmin

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/adonese/noebs/internal/accountenrollment"
)

func assertRealAccountEnrollmentAuthority(t *testing.T, r *Reconciler, state DesiredState) {
	t.Helper()
	if state.EnrollmentClient == nil {
		t.Fatal("real fixture must provision enrollment client")
	}
	ctx := context.Background()
	admin := mustRealAdminSession(t, r)
	const username = "account-enrollment-authority-fixture"
	if err := admin.post(ctx, realmPath(state.Realm.Name)+"/users", map[string]any{"username": username, "enabled": true, "email": "enroller@example.invalid", "emailVerified": true, "firstName": "Enrollment", "lastName": "Fixture"}); err != nil {
		t.Fatal(err)
	}
	var users []userRepresentation
	if _, err := admin.get(ctx, realmPath(state.Realm.Name)+"/users?exact=true&username="+url.QueryEscape(username), &users); err != nil || len(users) != 1 {
		t.Fatalf("lookup synthetic user: %v", err)
	}
	subject := users[0].ID
	t.Cleanup(func() {
		if err := admin.delete(context.Background(), realmPath(state.Realm.Name)+"/users/"+url.PathEscape(subject), nil); err != nil {
			t.Error(err)
		}
	})
	policy := accountenrollment.RuntimeConfig{Enabled: true, Issuer: "https://api.noebs.sd/auth/realms/noebs", TenantIDs: []string{"noebs"}, AllowedSubjects: []string{subject}, KeycloakBaseURL: r.config.BaseURL, KeycloakClientID: state.EnrollmentClient.ClientID, KeycloakClientSecret: r.config.ClientCredentials[state.EnrollmentClient.Credential].ClientSecret}
	authority, err := NewEnrollmentAuthority(policy, state.tenantCatalog, r.http)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := authority.Inspect(ctx, subject)
	if err != nil || len(initial.Memberships) != 0 || !initial.EmailVerified || initial.Fullname != "Enrollment Fixture" {
		t.Fatalf("inspect = %+v, %v", initial, err)
	}
	for _, tenant := range policy.TenantIDs {
		if err := authority.EnsureUserMembership(ctx, subject, tenant); err != nil {
			t.Fatalf("dedicated enroller add %s: %v", tenant, err)
		}
	}
	for _, tenant := range policy.TenantIDs {
		if err := authority.EnsureUserMembership(ctx, subject, tenant); err != nil {
			t.Fatal(err)
		}
	}
	admitted, err := authority.Inspect(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range policy.TenantIDs {
		if !slices.Equal(admitted.Memberships[tenant], []string{"user"}) {
			t.Fatalf("membership %s = %+v", tenant, admitted.Memberships[tenant])
		}
	}
	t.Run("OrganizationAliasMutationRejected", func(t *testing.T) {
		assertRealOrganizationAliasMutationRejected(t, admin, subject)
	})
	t.Run("ParallelOrganizationReassignmentPreservesCredentials", func(t *testing.T) {
		assertRealParallelOrganizationReassignment(t, r, state, subject)
	})
	// Operator assignment is an explicit administrative action. Enroller calls
	// must preserve it and may never issue role-mapping or removal requests.
	desired := Memberships{APIVersion: MembershipsAPIVersion, Subject: subject, Memberships: []TenantMembership{{Tenant: "noebs", Class: MembershipClassTenantAdmin}}}
	if _, err := r.AssignMemberships(ctx, state, desired, false); err != nil {
		t.Fatal(err)
	}
	if err := authority.EnsureUserMembership(ctx, subject, "noebs"); err != nil {
		t.Fatal(err)
	}
	after, err := authority.Inspect(ctx, subject)
	if err != nil || !slices.Equal(after.Memberships["noebs"], []string{"tenant-admin"}) {
		t.Fatalf("operator class changed: %+v, %v", after, err)
	}
}

func assertRealParallelOrganizationReassignment(t *testing.T, r *Reconciler, original DesiredState, subject string) {
	t.Helper()
	ctx := context.Background()
	admin := mustRealAdminSession(t, r)
	userPath := realmPath(original.Realm.Name) + "/users/" + url.PathEscape(subject)
	if err := admin.put(ctx, userPath+"/reset-password", map[string]any{"type": "password", "value": "Owned-cutover-fixture-64391", "temporary": false}); err != nil {
		t.Fatal(err)
	}
	var before []map[string]any
	if found, err := admin.get(ctx, userPath+"/credentials", &before); err != nil || !found || len(before) != 1 {
		t.Fatalf("owned initial credential: %v", err)
	}
	parallel := membershipTestDesiredState(t)
	if _, err := r.Reconcile(ctx, parallel); err != nil {
		t.Fatal(err)
	}
	defer func() {
		desired := Memberships{APIVersion: MembershipsAPIVersion, Subject: subject, Memberships: []TenantMembership{{Tenant: "noebs", Class: MembershipClassUser}}}
		if _, err := r.AssignMemberships(context.Background(), parallel, desired, false); err != nil {
			t.Errorf("restore owned membership: %v", err)
			return
		}
		if _, err := r.Reconcile(context.Background(), original); err != nil {
			t.Errorf("remove temporary organization: %v", err)
		}
	}()
	topology, err := readMembershipTopology(ctx, admin, parallel)
	if err != nil {
		t.Fatal(err)
	}
	oldID, newID := topology["noebs"].representation.ID, topology["tenant-sandbox"].representation.ID
	if oldID == "" || newID == "" || oldID == newID {
		t.Fatal("parallel organizations must retain distinct IDs")
	}
	// Admit the same subject to the new organization while the old one remains.
	both := Memberships{APIVersion: MembershipsAPIVersion, Subject: subject, Memberships: []TenantMembership{{Tenant: "noebs", Class: MembershipClassUser}, {Tenant: "tenant-sandbox", Class: MembershipClassUser}}}
	if _, err := r.AssignMemberships(ctx, parallel, both, false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{oldID, newID} {
		classes, member, err := enrollmentMemberClasses(ctx, admin, id, subject)
		if err != nil || !member || !slices.Equal(classes, []string{"user"}) {
			t.Fatalf("parallel admission failed: %v, %t, %v", classes, member, err)
		}
	}
	// Exact assignment now removes only the old unmanaged membership.
	both.Memberships = both.Memberships[1:]
	if _, err := r.AssignMemberships(ctx, parallel, both, false); err != nil {
		t.Fatal(err)
	}
	if _, member, err := enrollmentMemberClasses(ctx, admin, oldID, subject); err != nil || member {
		t.Fatalf("old membership remains: %t, %v", member, err)
	}
	classes, member, err := enrollmentMemberClasses(ctx, admin, newID, subject)
	if err != nil || !member || !slices.Equal(classes, []string{"user"}) {
		t.Fatalf("replacement membership lost: %v, %t, %v", classes, member, err)
	}
	var user userRepresentation
	if found, err := admin.get(ctx, userPath, &user); err != nil || !found || user.ID != subject {
		t.Fatalf("original subject lost: %v", err)
	}
	var after []map[string]any
	if found, err := admin.get(ctx, userPath+"/credentials", &after); err != nil || !found || len(after) != 1 || after[0]["id"] != before[0]["id"] || after[0]["type"] != before[0]["type"] {
		t.Fatalf("original credential changed: %v", err)
	}
}

func assertRealOrganizationAliasMutationRejected(t *testing.T, admin *adminSession, subject string) {
	t.Helper()
	ctx := context.Background()
	organizations, err := listOrganizations(ctx, admin, realmPath("noebs"))
	if err != nil {
		t.Fatal(err)
	}
	var original organizationRepresentation
	for _, organization := range organizations {
		if organization.Alias == "noebs" {
			original = organization
		}
	}
	if original.ID == "" {
		t.Fatal("Noebs organization missing")
	}
	base := realmPath("noebs") + "/organizations/" + url.PathEscape(original.ID)
	groupsPath := base + "/members/" + url.PathEscape(subject) + "/groups?briefRepresentation=false&first=0&max=1000"
	var before []groupRepresentation
	if found, err := admin.get(ctx, groupsPath, &before); err != nil || !found || len(before) != 1 || before[0].Name != "user" {
		t.Fatalf("initial member groups = %+v, %v", before, err)
	}
	renamed := original
	renamed.Alias, renamed.Name = "alias-migration-fixture", "Alias Migration Fixture"
	// Restore immediately so the remaining real tests keep their declared authority.
	defer func() {
		if err := admin.put(context.Background(), base, original); err != nil {
			t.Errorf("restore organization alias: %v", err)
		}
	}()
	if err := admin.put(ctx, base, renamed); err == nil || !strings.Contains(err.Error(), "Cannot change the alias") {
		t.Fatalf("alias mutation must be rejected by pinned Keycloak: %v", err)
	}
	var observed organizationRepresentation
	if found, err := admin.get(ctx, base, &observed); err != nil || !found || observed.ID != original.ID || observed.Alias != original.Alias || observed.Name != original.Name {
		t.Fatalf("rejected mutation changed organization = %+v, %v", observed, err)
	}
	classes, member, err := enrollmentMemberClasses(ctx, admin, original.ID, subject)
	if err != nil || !member || !slices.Equal(classes, []string{"user"}) {
		t.Fatalf("renamed membership = %v, %t, %v", classes, member, err)
	}
	var after []groupRepresentation
	if found, err := admin.get(ctx, groupsPath, &after); err != nil || !found || len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("renamed member groups = %+v, %v", after, err)
	}
}
