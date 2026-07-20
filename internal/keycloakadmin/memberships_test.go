package keycloakadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

const membershipTestSubject = "11111111-1111-4111-8111-11111111111a"

func TestLoadMembershipsStrictAndCanonical(t *testing.T) {
	state := repositoryDesiredState(t)
	catalog := repositoryTenantCatalog(t)
	document := `api_version: noebs.sd/keycloak-memberships/v1
subject: 11111111-1111-4111-8111-11111111111a
memberships:
  - tenant: tenant-sandbox
    class: user
  - tenant: tenant-cutover
    class: tenant-admin
`
	memberships, err := LoadMemberships(strings.NewReader(document), catalog, state)
	if err != nil {
		t.Fatalf("LoadMemberships() error = %v", err)
	}
	if got := memberships.Memberships; !reflect.DeepEqual(got, []TenantMembership{
		{Tenant: "tenant-cutover", Class: MembershipClassTenantAdmin},
		{Tenant: "tenant-sandbox", Class: MembershipClassUser},
	}) {
		t.Fatalf("memberships = %#v", got)
	}

	invalid := []struct {
		name     string
		document string
	}{
		{name: "unknown field", document: document + "unknown: true\n"},
		{name: "multiple documents", document: document + "---\n{}\n"},
		{name: "api version", document: strings.Replace(document, MembershipsAPIVersion, "noebs.sd/keycloak-memberships/v2", 1)},
		{name: "noncanonical subject", document: strings.Replace(document, membershipTestSubject, strings.ToUpper(membershipTestSubject), 1)},
		{name: "zero subject", document: strings.Replace(document, membershipTestSubject, "00000000-0000-0000-0000-000000000000", 1)},
		{name: "missing memberships", document: "api_version: " + MembershipsAPIVersion + "\nsubject: " + membershipTestSubject + "\n"},
		{name: "duplicate tenant", document: document + "  - tenant: tenant-cutover\n    class: user\n"},
		{name: "unknown tenant", document: strings.Replace(document, "tenant-sandbox", "tenant-unknown", 1)},
		{name: "unknown class", document: strings.Replace(document, "class: user", "class: platform-admin", 1)},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadMemberships(strings.NewReader(test.document), catalog, state)
			if !errors.Is(err, ErrInvalidMemberships) {
				t.Fatalf("LoadMemberships() error = %v, want ErrInvalidMemberships", err)
			}
		})
	}

	empty, err := LoadMemberships(strings.NewReader("api_version: "+MembershipsAPIVersion+"\nsubject: "+membershipTestSubject+"\nmemberships: []\n"), catalog, state)
	if err != nil || len(empty.Memberships) != 0 {
		t.Fatalf("empty exact memberships = %#v, %v", empty, err)
	}
}

func TestAssignMembershipsCreatesAndThenIsIdempotent(t *testing.T) {
	state := repositoryDesiredState(t)
	fake, reconciler := newMembershipTestReconciler(t, state)
	desired := loadMembershipTestDocument(t, state, `
  - tenant: tenant-cutover
    class: user
`)

	actions, err := reconciler.AssignMemberships(context.Background(), state, desired, false)
	if err != nil {
		t.Fatalf("AssignMemberships() error = %v", err)
	}
	wantActions := []PlannedMembershipAction{{
		Subject: membershipTestSubject,
		Tenant:  "tenant-cutover",
		Class:   MembershipClassUser,
		Action:  MembershipActionAdd,
	}}
	if !reflect.DeepEqual(actions, wantActions) {
		t.Fatalf("actions = %#v, want %#v", actions, wantActions)
	}
	if got := fake.classes("tenant-cutover", membershipTestSubject); !reflect.DeepEqual(got, []MembershipClass{MembershipClassUser}) {
		t.Fatalf("tenant-cutover classes = %v", got)
	}
	if got := fake.writeCount(); got != 2 {
		t.Fatalf("writes = %d, want add organization and add group", got)
	}
	assertNoRoleMappingWrites(t, fake.writePaths())

	writes := fake.writeCount()
	actions, err = reconciler.AssignMemberships(context.Background(), state, desired, false)
	if err != nil {
		t.Fatalf("idempotent AssignMemberships() error = %v", err)
	}
	if len(actions) != 0 || fake.writeCount() != writes {
		t.Fatalf("idempotent actions = %#v, writes = %d", actions, fake.writeCount()-writes)
	}
}

func TestAssignMembershipsDowngradesBeforeAddingClass(t *testing.T) {
	state := repositoryDesiredState(t)
	fake, reconciler := newMembershipTestReconciler(t, state)
	fake.setMembership("tenant-cutover", membershipTestSubject, MembershipClassTenantAdmin)
	desired := loadMembershipTestDocument(t, state, `
  - tenant: tenant-cutover
    class: user
`)

	actions, err := reconciler.AssignMemberships(context.Background(), state, desired, false)
	if err != nil {
		t.Fatalf("AssignMemberships() error = %v", err)
	}
	if len(actions) != 1 || actions[0].Action != MembershipActionSetClass {
		t.Fatalf("actions = %#v", actions)
	}
	paths := fake.writePaths()
	if len(paths) != 2 || !strings.HasPrefix(paths[0], http.MethodDelete+" ") || !strings.Contains(paths[0], "tenant-admin") ||
		!strings.HasPrefix(paths[1], http.MethodPut+" ") || !strings.Contains(paths[1], "-user/members/") {
		t.Fatalf("downgrade writes = %v", paths)
	}
	if got := fake.classes("tenant-cutover", membershipTestSubject); !reflect.DeepEqual(got, []MembershipClass{MembershipClassUser}) {
		t.Fatalf("classes after downgrade = %v", got)
	}
	assertNoRoleMappingWrites(t, paths)
}

func TestAssignMembershipsRemovesOmittedOrganization(t *testing.T) {
	state := repositoryDesiredState(t)
	fake, reconciler := newMembershipTestReconciler(t, state)
	fake.setMembership("tenant-cutover", membershipTestSubject, MembershipClassBackoffice)
	desired := loadMembershipTestDocument(t, state, " []\n")

	actions, err := reconciler.AssignMemberships(context.Background(), state, desired, false)
	if err != nil {
		t.Fatalf("AssignMemberships() error = %v", err)
	}
	if len(actions) != 1 || actions[0].Action != MembershipActionRemove || actions[0].Class != "" {
		t.Fatalf("actions = %#v", actions)
	}
	if got := fake.writePaths(); len(got) != 1 || !strings.Contains(got[0], "/members/"+membershipTestSubject) {
		t.Fatalf("removal writes = %v", got)
	}
	if fake.isMember("tenant-cutover", membershipTestSubject) {
		t.Fatal("subject remains an organization member")
	}
}

func TestAssignMembershipsDryRunIsStableAndReadOnly(t *testing.T) {
	state := repositoryDesiredState(t)
	fake, reconciler := newMembershipTestReconciler(t, state)
	fake.setMembership("tenant-cutover", membershipTestSubject, MembershipClassTenantAdmin)
	desired := loadMembershipTestDocument(t, state, `
  - tenant: tenant-sandbox
    class: backoffice
  - tenant: tenant-cutover
    class: user
`)

	first, err := reconciler.AssignMemberships(context.Background(), state, desired, true)
	if err != nil {
		t.Fatalf("dry-run AssignMemberships() error = %v", err)
	}
	second, err := reconciler.AssignMemberships(context.Background(), state, desired, true)
	if err != nil {
		t.Fatalf("second dry-run AssignMemberships() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) || len(first) != 2 || first[0].Tenant != "tenant-cutover" || first[1].Tenant != "tenant-sandbox" {
		t.Fatalf("dry-run plans = %#v and %#v", first, second)
	}
	if fake.writeCount() != 0 {
		t.Fatalf("dry-run writes = %d", fake.writeCount())
	}
	if got := fake.classes("tenant-cutover", membershipTestSubject); !reflect.DeepEqual(got, []MembershipClass{MembershipClassTenantAdmin}) {
		t.Fatalf("dry-run changed classes to %v", got)
	}
}

func TestAssignMembershipsFailuresAreTyped(t *testing.T) {
	state := repositoryDesiredState(t)
	desired := loadMembershipTestDocument(t, state, `
  - tenant: tenant-cutover
    class: user
`)

	t.Run("missing subject", func(t *testing.T) {
		fake, reconciler := newMembershipTestReconciler(t, state)
		fake.deleteUser(membershipTestSubject)
		_, err := reconciler.AssignMemberships(context.Background(), state, desired, false)
		if !errors.Is(err, ErrMembershipSubjectMissing) || fake.writeCount() != 0 {
			t.Fatalf("AssignMemberships() error = %v, writes = %d", err, fake.writeCount())
		}
	})

	t.Run("topology drift", func(t *testing.T) {
		fake, reconciler := newMembershipTestReconciler(t, state)
		fake.deleteGroup("tenant-cutover", MembershipClassUser)
		_, err := reconciler.AssignMemberships(context.Background(), state, desired, false)
		if !errors.Is(err, ErrMembershipTopology) || fake.writeCount() != 0 {
			t.Fatalf("AssignMemberships() error = %v, writes = %d", err, fake.writeCount())
		}
	})

	t.Run("descendant topology drift", func(t *testing.T) {
		fake, reconciler := newMembershipTestReconciler(t, state)
		fake.addChildGroup("tenant-cutover", MembershipClassUser, "rogue-child")
		_, err := reconciler.AssignMemberships(context.Background(), state, desired, false)
		if !errors.Is(err, ErrMembershipTopology) || fake.writeCount() != 0 {
			t.Fatalf("AssignMemberships() error = %v, writes = %d", err, fake.writeCount())
		}
	})

	t.Run("group role mapping privilege drift", func(t *testing.T) {
		fake, reconciler := newMembershipTestReconciler(t, state)
		fake.addGroupRoleMapping("tenant-cutover", MembershipClassUser, "tenant-admin")
		_, err := reconciler.AssignMemberships(context.Background(), state, desired, false)
		if !errors.Is(err, ErrMembershipTopology) || fake.writeCount() != 0 {
			t.Fatalf("AssignMemberships() error = %v, writes = %d", err, fake.writeCount())
		}
	})

	t.Run("composite resource role drift", func(t *testing.T) {
		fake, reconciler := newMembershipTestReconciler(t, state)
		fake.makeClientRoleComposite("user")
		_, err := reconciler.AssignMemberships(context.Background(), state, desired, false)
		if !errors.Is(err, ErrMembershipTopology) || fake.writeCount() != 0 {
			t.Fatalf("AssignMemberships() error = %v, writes = %d", err, fake.writeCount())
		}
	})

	t.Run("apply failure", func(t *testing.T) {
		fake, reconciler := newMembershipTestReconciler(t, state)
		fake.failGroupAdd = true
		_, err := reconciler.AssignMemberships(context.Background(), state, desired, false)
		if !errors.Is(err, ErrMembershipApply) || !errors.Is(err, ErrUnexpectedResponse) {
			t.Fatalf("AssignMemberships() error = %v", err)
		}
		if fake.writeCount() != 1 {
			t.Fatalf("successful writes before failure = %d, want organization membership only", fake.writeCount())
		}
	})

	t.Run("verification failure", func(t *testing.T) {
		fake, reconciler := newMembershipTestReconciler(t, state)
		fake.ignoreGroupAdd = true
		_, err := reconciler.AssignMemberships(context.Background(), state, desired, false)
		if !errors.Is(err, ErrMembershipVerification) {
			t.Fatalf("AssignMemberships() error = %v", err)
		}
	})
}

func TestLookupSubjectByEmailExact(t *testing.T) {
	state := repositoryDesiredState(t)
	fake, reconciler := newMembershipTestReconciler(t, state)

	subject, err := reconciler.LookupSubjectByEmail(context.Background(), "user@example.com")
	if err != nil || subject != membershipTestSubject {
		t.Fatalf("LookupSubjectByEmail() = %q, %v", subject, err)
	}
	if query := fake.lookupQuery(); query != "email=user%40example.com&exact=true&first=0&max=2" {
		t.Fatalf("lookup query = %q", query)
	}
	if fake.writeCount() != 0 {
		t.Fatalf("lookup writes = %d", fake.writeCount())
	}

	fake.deleteUser(membershipTestSubject)
	if _, err := reconciler.LookupSubjectByEmail(context.Background(), "user@example.com"); !errors.Is(err, ErrMembershipSubjectMissing) {
		t.Fatalf("missing LookupSubjectByEmail() error = %v", err)
	}
	fake.addUser("22222222-2222-4222-8222-222222222222", "duplicate@example.com")
	fake.addUser("33333333-3333-4333-8333-333333333333", "duplicate@example.com")
	if _, err := reconciler.LookupSubjectByEmail(context.Background(), "duplicate@example.com"); !errors.Is(err, ErrMembershipSubjectMany) {
		t.Fatalf("ambiguous LookupSubjectByEmail() error = %v", err)
	}
	if _, err := reconciler.LookupSubjectByEmail(context.Background(), "User <user@example.com>"); !errors.Is(err, ErrInvalidLookupEmail) {
		t.Fatalf("invalid LookupSubjectByEmail() error = %v", err)
	}
}

func loadMembershipTestDocument(t *testing.T, state DesiredState, memberships string) Memberships {
	t.Helper()
	document := "api_version: " + MembershipsAPIVersion + "\nsubject: " + membershipTestSubject + "\nmemberships:" + memberships
	result, err := LoadMemberships(strings.NewReader(document), repositoryTenantCatalog(t), state)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func newMembershipTestReconciler(t *testing.T, state DesiredState) (*membershipFake, *Reconciler) {
	t.Helper()
	fake := newMembershipFake(state)
	server := httptest.NewTLSServer(fake)
	t.Cleanup(server.Close)
	config := validTestConfig(server.URL)
	config.AdminRealm = "noebs"
	config.ClientID = "noebs-keycloak-reconciler"
	config.ClientSecret = config.ClientCredentials[config.ClientID].ClientSecret
	reconciler, err := New(config, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return fake, reconciler
}

func assertNoRoleMappingWrites(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		if strings.Contains(path, "role-mappings") {
			t.Fatalf("membership assignment wrote a role mapping: %s", path)
		}
	}
}

type membershipFake struct {
	mu sync.Mutex

	organizations  map[string]organizationRepresentation
	groups         map[string]map[MembershipClass]groupRepresentation
	children       map[string][]groupRepresentation
	resourceClient clientRepresentation
	clientRoles    map[string]roleRepresentation
	roleMappings   map[string]roleMappingsRepresentation
	users          map[string]membershipUserRepresentation
	members        map[string]map[string]map[MembershipClass]bool
	writes         []string
	query          string
	failGroupAdd   bool
	ignoreGroupAdd bool
}

func newMembershipFake(state DesiredState) *membershipFake {
	fake := &membershipFake{
		organizations: make(map[string]organizationRepresentation, len(state.Organizations)),
		groups:        make(map[string]map[MembershipClass]groupRepresentation, len(state.Organizations)),
		children:      map[string][]groupRepresentation{},
		resourceClient: clientRepresentation{
			ID:       "client-noebs-api",
			ClientID: state.ResourceClient.ClientID,
		},
		clientRoles:  make(map[string]roleRepresentation, len(state.ResourceClient.Roles)),
		roleMappings: map[string]roleMappingsRepresentation{},
		users:        map[string]membershipUserRepresentation{},
		members:      make(map[string]map[string]map[MembershipClass]bool, len(state.Organizations)),
	}
	for _, desiredRole := range state.ResourceClient.Roles {
		fake.clientRoles[desiredRole.Name] = roleRepresentation{
			ID:          "role-" + desiredRole.Name,
			Name:        desiredRole.Name,
			Description: managedDescription(desiredRole.Description),
			ClientRole:  true,
			ContainerID: fake.resourceClient.ID,
		}
	}
	for _, desiredOrganization := range state.Organizations {
		organizationID := "org-" + desiredOrganization.Alias
		fake.organizations[desiredOrganization.Alias] = organizationRepresentation{
			ID:         organizationID,
			Name:       desiredOrganization.Name,
			Alias:      desiredOrganization.Alias,
			Enabled:    true,
			Attributes: managedAttributes(desiredOrganization.Attributes),
		}
		fake.groups[desiredOrganization.Alias] = make(map[MembershipClass]groupRepresentation, len(desiredOrganization.Groups))
		for _, desiredGroup := range desiredOrganization.Groups {
			class := MembershipClass(desiredGroup.Name)
			group := groupRepresentation{
				ID:          "group-" + desiredOrganization.Alias + "-" + desiredGroup.Name,
				Name:        desiredGroup.Name,
				Description: desiredGroup.Description,
				Attributes:  managedAttributes(desiredGroup.Attributes),
			}
			fake.groups[desiredOrganization.Alias][class] = group
			mappedRoles := make([]roleRepresentation, 0, len(desiredGroup.ClientRoles))
			for _, roleName := range desiredGroup.ClientRoles {
				mappedRoles = append(mappedRoles, fake.clientRoles[roleName])
			}
			fake.roleMappings[group.ID] = roleMappingsRepresentation{ClientMappings: map[string]clientRoleMappingRepresentation{
				fake.resourceClient.ClientID: {
					ID:       fake.resourceClient.ID,
					Client:   fake.resourceClient.ClientID,
					Mappings: mappedRoles,
				},
			}}
		}
		fake.members[desiredOrganization.Alias] = map[string]map[MembershipClass]bool{}
	}
	fake.addUserLocked(membershipTestSubject, "user@example.com")
	return fake
}

func (f *membershipFake) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if request.URL.Path == "/realms/noebs/protocol/openid-connect/token" {
		writeJSON(writer, http.StatusOK, map[string]string{"access_token": "admin-token", "token_type": "Bearer"})
		return
	}
	if request.Header.Get("Authorization") != "Bearer admin-token" {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	const base = "/admin/realms/noebs"
	if request.Method == http.MethodGet && request.URL.Path == base+"/users" {
		f.query = request.URL.RawQuery
		email := request.URL.Query().Get("email")
		var users []membershipUserRepresentation
		for _, user := range f.users {
			if strings.EqualFold(user.Email, email) {
				users = append(users, user)
			}
		}
		sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
		if len(users) > 2 {
			users = users[:2]
		}
		writeJSON(writer, http.StatusOK, users)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == base+"/organizations" {
		organizations := make([]organizationRepresentation, 0, len(f.organizations))
		for _, organization := range f.organizations {
			organizations = append(organizations, organization)
		}
		sort.Slice(organizations, func(i, j int) bool { return organizations[i].Alias < organizations[j].Alias })
		writeJSON(writer, http.StatusOK, organizations)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == base+"/clients" && request.URL.Query().Get("clientId") == f.resourceClient.ClientID {
		writeJSON(writer, http.StatusOK, []clientRepresentation{f.resourceClient})
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == base+"/clients/"+f.resourceClient.ID+"/roles" {
		roles := make([]roleRepresentation, 0, len(f.clientRoles))
		for _, role := range f.clientRoles {
			roles = append(roles, role)
		}
		sort.Slice(roles, func(i, j int) bool { return roles[i].Name < roles[j].Name })
		writeJSON(writer, http.StatusOK, roles)
		return
	}
	if strings.HasPrefix(request.URL.Path, base+"/users/") && request.Method == http.MethodGet {
		subject := strings.TrimPrefix(request.URL.Path, base+"/users/")
		user, exists := f.users[subject]
		if !exists {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		writeJSON(writer, http.StatusOK, user)
		return
	}
	if !strings.HasPrefix(request.URL.Path, base+"/organizations/") {
		http.Error(writer, "unexpected request", http.StatusNotFound)
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, base+"/organizations/"), "/")
	if len(parts) < 2 {
		http.Error(writer, "invalid organization path", http.StatusNotFound)
		return
	}
	tenant, exists := f.tenantForOrganizationID(parts[0])
	if !exists {
		http.Error(writer, "organization not found", http.StatusNotFound)
		return
	}
	switch parts[1] {
	case "groups":
		f.handleGroups(writer, request, tenant, parts)
	case "members":
		f.handleMembers(writer, request, tenant, parts)
	default:
		http.Error(writer, "unexpected organization path", http.StatusNotFound)
	}
}

func (f *membershipFake) handleGroups(writer http.ResponseWriter, request *http.Request, tenant string, parts []string) {
	if len(parts) == 2 && request.Method == http.MethodGet {
		groups := make([]groupRepresentation, 0, len(f.groups[tenant]))
		for _, group := range f.groups[tenant] {
			groups = append(groups, group)
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
		writeJSON(writer, http.StatusOK, groups)
		return
	}
	if len(parts) == 4 && parts[3] == "children" && request.Method == http.MethodGet {
		if _, exists := f.classForGroupID(tenant, parts[2]); !exists {
			http.Error(writer, "group not found", http.StatusNotFound)
			return
		}
		writeJSON(writer, http.StatusOK, f.children[parts[2]])
		return
	}
	if len(parts) == 4 && parts[3] == "role-mappings" && request.Method == http.MethodGet {
		if _, exists := f.classForGroupID(tenant, parts[2]); !exists {
			http.Error(writer, "group not found", http.StatusNotFound)
			return
		}
		writeJSON(writer, http.StatusOK, f.roleMappings[parts[2]])
		return
	}
	if len(parts) != 5 || parts[3] != "members" {
		http.Error(writer, "unexpected group request", http.StatusNotFound)
		return
	}
	class, exists := f.classForGroupID(tenant, parts[2])
	if !exists {
		http.Error(writer, "group not found", http.StatusNotFound)
		return
	}
	subject := parts[4]
	classes, member := f.members[tenant][subject]
	if !member {
		http.Error(writer, "not an organization member", http.StatusBadRequest)
		return
	}
	if request.Method == http.MethodPut && f.failGroupAdd {
		http.Error(writer, "injected failure", http.StatusInternalServerError)
		return
	}
	if request.Method != http.MethodPut && request.Method != http.MethodDelete {
		http.Error(writer, "unexpected group method", http.StatusMethodNotAllowed)
		return
	}
	f.writes = append(f.writes, request.Method+" "+request.URL.Path)
	if !f.ignoreGroupAdd || request.Method != http.MethodPut {
		classes[class] = request.Method == http.MethodPut
		if request.Method == http.MethodDelete {
			delete(classes, class)
		}
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (f *membershipFake) handleMembers(writer http.ResponseWriter, request *http.Request, tenant string, parts []string) {
	if len(parts) == 2 && request.Method == http.MethodPost {
		var subject string
		if err := json.NewDecoder(request.Body).Decode(&subject); err != nil {
			http.Error(writer, "invalid subject", http.StatusBadRequest)
			return
		}
		if _, exists := f.users[subject]; !exists {
			http.Error(writer, "subject not found", http.StatusNotFound)
			return
		}
		if _, exists := f.members[tenant][subject]; exists {
			http.Error(writer, "already member", http.StatusConflict)
			return
		}
		f.members[tenant][subject] = map[MembershipClass]bool{}
		f.writes = append(f.writes, request.Method+" "+request.URL.Path)
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) < 3 {
		http.Error(writer, "unexpected member request", http.StatusNotFound)
		return
	}
	subject := parts[2]
	classes, member := f.members[tenant][subject]
	if len(parts) == 3 && request.Method == http.MethodGet {
		if !member {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		writeJSON(writer, http.StatusOK, f.users[subject])
		return
	}
	if len(parts) == 3 && request.Method == http.MethodDelete {
		if !member {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		delete(f.members[tenant], subject)
		f.writes = append(f.writes, request.Method+" "+request.URL.Path)
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 4 && parts[3] == "groups" && request.Method == http.MethodGet {
		if !member {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		var groups []groupRepresentation
		for class := range classes {
			groups = append(groups, f.groups[tenant][class])
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
		writeJSON(writer, http.StatusOK, groups)
		return
	}
	http.Error(writer, "unexpected member method", http.StatusNotFound)
}

func (f *membershipFake) tenantForOrganizationID(id string) (string, bool) {
	for tenant, organization := range f.organizations {
		if organization.ID == id {
			return tenant, true
		}
	}
	return "", false
}

func (f *membershipFake) classForGroupID(tenant, id string) (MembershipClass, bool) {
	for class, group := range f.groups[tenant] {
		if group.ID == id {
			return class, true
		}
	}
	return "", false
}

func (f *membershipFake) addUserLocked(subject, email string) {
	f.users[subject] = membershipUserRepresentation{ID: subject, Email: email}
}

func (f *membershipFake) addUser(subject, email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addUserLocked(subject, email)
}

func (f *membershipFake) deleteUser(subject string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.users, subject)
}

func (f *membershipFake) deleteGroup(tenant string, class MembershipClass) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.groups[tenant], class)
}

func (f *membershipFake) addChildGroup(tenant string, class MembershipClass, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parent := f.groups[tenant][class]
	f.children[parent.ID] = append(f.children[parent.ID], groupRepresentation{ID: "child-" + tenant + "-" + name, Name: name})
}

func (f *membershipFake) addGroupRoleMapping(tenant string, class MembershipClass, roleName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	group := f.groups[tenant][class]
	mappings := f.roleMappings[group.ID]
	clientMapping := mappings.ClientMappings[f.resourceClient.ClientID]
	clientMapping.Mappings = append(clientMapping.Mappings, f.clientRoles[roleName])
	mappings.ClientMappings[f.resourceClient.ClientID] = clientMapping
	f.roleMappings[group.ID] = mappings
}

func (f *membershipFake) makeClientRoleComposite(roleName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	role := f.clientRoles[roleName]
	role.Composite = true
	f.clientRoles[roleName] = role
}

func (f *membershipFake) setMembership(tenant, subject string, classes ...MembershipClass) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[tenant][subject] = map[MembershipClass]bool{}
	for _, class := range classes {
		f.members[tenant][subject][class] = true
	}
}

func (f *membershipFake) isMember(tenant, subject string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, exists := f.members[tenant][subject]
	return exists
}

func (f *membershipFake) classes(tenant, subject string) []MembershipClass {
	f.mu.Lock()
	defer f.mu.Unlock()
	classes := f.members[tenant][subject]
	result := make([]MembershipClass, 0, len(classes))
	for _, class := range membershipClassOrder {
		if classes[class] {
			result = append(result, class)
		}
	}
	return result
}

func (f *membershipFake) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func (f *membershipFake) writePaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

func (f *membershipFake) lookupQuery() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.query
}

func (f *membershipFake) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fmt.Sprintf("membershipFake{writes:%v}", f.writes)
}
