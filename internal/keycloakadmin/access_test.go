package keycloakadmin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/tenantauth"
)

func accessAuthorityFixture(t *testing.T) (*AccessAuthority, *membershipFake) {
	t.Helper()
	state := membershipTestDesiredState(t)
	fake := newMembershipFake(state)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/clients") {
			t.Error("access authority queried global clients")
			http.Error(w, "forbidden", 403)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/token") {
			if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "noebs-account-enroller" {
				t.Error("incorrect credential")
			}
		}
		// Native selected-member GET returns enabled, unlike the older shared
		// membership planning fake. Preserve that explicit protocol field here.
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/organizations/") && strings.Contains(r.URL.Path, "/members/") && !strings.HasSuffix(r.URL.Path, "/groups") {
			recorder := httptest.NewRecorder()
			fake.ServeHTTP(recorder, r)
			if recorder.Code == http.StatusOK {
				var member map[string]any
				if err := json.Unmarshal(recorder.Body.Bytes(), &member); err != nil {
					t.Fatal(err)
				}
				member["enabled"] = true
				writeJSON(w, http.StatusOK, member)
			} else {
				w.WriteHeader(recorder.Code)
				_, _ = w.Write(recorder.Body.Bytes())
			}
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	policy := accountenrollment.RuntimeConfig{Enabled: true, Issuer: "https://api.noebs.sd/auth/realms/noebs", TenantIDs: []string{"noebs"}, AllowedSubjects: []string{membershipTestSubject}, KeycloakBaseURL: server.URL, KeycloakClientID: "noebs-account-enroller", KeycloakClientSecret: "test-enroller-secret"}
	authority, err := NewAccessAuthority(policy, state.tenantCatalog, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return authority, fake
}

func TestAccessAuthorityComposesAndRevokesOnlySelectedGroups(t *testing.T) {
	authority, fake := accessAuthorityFixture(t)
	ctx := context.Background()
	fake.setMembership("tenant-sandbox", membershipTestSubject, MembershipClassTenantAdmin)
	fake.setMembership("noebs", membershipTestSubject, MembershipClassUser)
	// An unrelated group with no Noebs API roles survives every managed delta.
	other := MembershipClass("unrelated-team")
	fake.groups["noebs"][other] = groupRepresentation{ID: "unrelated-group", Name: string(other)}
	fake.roleMappings["unrelated-group"] = roleMappingsRepresentation{ClientMappings: map[string]clientRoleMappingRepresentation{"other-client": {ID: "other-id", Client: "other-client", Mappings: []roleRepresentation{{ID: "other-role", Name: "other-team", ClientRole: true, ContainerID: "other-id"}}}}}
	fake.members["noebs"][membershipTestSubject][other] = true
	if err := authority.ApplyDelta(ctx, "noebs", membershipTestSubject, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleTenantAdmin}, nil); err != nil {
		t.Fatal(err)
	}
	composed, err := authority.Account(ctx, "noebs", membershipTestSubject)
	if err != nil || !slices.Equal(composed.Roles, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleTenantAdmin, tenantauth.RoleUser}) || !fake.members["noebs"][membershipTestSubject][other] {
		t.Fatalf("composition=%+v err=%v", composed, err)
	}
	if err := authority.ApplyDelta(ctx, "noebs", membershipTestSubject, nil, []tenantauth.Role{tenantauth.RoleTenantAdmin}); err != nil {
		t.Fatal(err)
	}
	before := fake.writeCount()
	if err := authority.ApplyDelta(ctx, "noebs", membershipTestSubject, nil, []tenantauth.Role{tenantauth.RoleTenantAdmin}); err != nil || fake.writeCount() != before {
		t.Fatalf("repeat revoke wrote=%d err=%v", fake.writeCount()-before, err)
	}
	if err := authority.ApplyDelta(ctx, "noebs", membershipTestSubject, nil, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleUser}); err != nil {
		t.Fatal(err)
	}
	if !fake.isMember("noebs", membershipTestSubject) || len(fake.classes("noebs", membershipTestSubject)) != 0 || !fake.members["noebs"][membershipTestSubject][other] {
		t.Fatal("empty managed roles removed unrelated membership/group")
	}
	if !slices.Equal(fake.classes("tenant-sandbox", membershipTestSubject), []MembershipClass{MembershipClassTenantAdmin}) {
		t.Fatal("other tenant changed")
	}
	for _, path := range fake.writePaths() {
		if strings.Contains(path, "/role-mappings") || strings.Contains(path, "tenant-sandbox") || !strings.Contains(path, "/groups/") {
			t.Fatalf("unexpected write=%s", path)
		}
	}
}

func TestAccessAuthorityRejectsManagedRoleDriftBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*membershipFake)
	}{
		{"missing managed group", func(f *membershipFake) { delete(f.groups["noebs"], MembershipClassUser) }},
		{"group hierarchy", func(f *membershipFake) {
			id := f.groups["noebs"][MembershipClassUser].ID
			f.children[id] = []groupRepresentation{{ID: "child", Name: "hidden"}}
		}},
		{"extra permission", func(f *membershipFake) {
			id := f.groups["noebs"][MembershipClassUser].ID
			m := f.roleMappings[id]
			c := m.ClientMappings["noebs-api"]
			c.Mappings = append(c.Mappings, f.clientRoles["identity:access:write"])
			m.ClientMappings["noebs-api"] = c
		}},
		{"composite role", func(f *membershipFake) {
			id := f.groups["noebs"][MembershipClassTenantAdmin].ID
			m := f.roleMappings[id]
			c := m.ClientMappings["noebs-api"]
			c.Mappings[0].Composite = true
		}},
		{"wrong client role", func(f *membershipFake) {
			id := f.groups["noebs"][MembershipClassTenantAdmin].ID
			m := f.roleMappings[id]
			c := m.ClientMappings["noebs-api"]
			c.Mappings[0].ContainerID = "other-client"
		}},
		{"missing access permission", func(f *membershipFake) {
			id := f.groups["noebs"][MembershipClassTenantAdmin].ID
			m := f.roleMappings[id]
			c := m.ClientMappings["noebs-api"]
			c.Mappings = c.Mappings[:len(c.Mappings)-1]
			m.ClientMappings["noebs-api"] = c
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authority, fake := accessAuthorityFixture(t)
			tc.mutate(fake)
			err := authority.ApplyDelta(context.Background(), "noebs", membershipTestSubject, []tenantauth.Role{tenantauth.RoleUser}, nil)
			if !errors.Is(err, ErrMembershipTopology) || fake.writeCount() != 0 {
				t.Fatalf("drift=%v writes=%d", err, fake.writeCount())
			}
		})
	}
}

func TestAccessAuthorityPreservesSubjectAndRejectsInvalidInputs(t *testing.T) {
	authority, fake := accessAuthorityFixture(t)
	ctx := context.Background()
	account, err := authority.Account(ctx, "noebs", membershipTestSubject)
	if err != nil || account.Member || account.Email != "" {
		t.Fatalf("nonmember leaked profile=%+v %v", account, err)
	}
	if exists, err := authority.SubjectExists(ctx, membershipTestSubject); err != nil || !exists {
		t.Fatalf("exact subject=%t %v", exists, err)
	}
	for _, tc := range []struct {
		tenant, subject string
		grant, revoke   []tenantauth.Role
	}{
		{"unknown", membershipTestSubject, []tenantauth.Role{tenantauth.RoleUser}, nil},
		{"noebs", "../users", []tenantauth.Role{tenantauth.RoleUser}, nil},
		{"noebs", membershipTestSubject, []tenantauth.Role{"realm-admin"}, nil},
		{"noebs", membershipTestSubject, []tenantauth.Role{tenantauth.RoleUser, tenantauth.RoleUser}, nil},
		{"noebs", membershipTestSubject, []tenantauth.Role{tenantauth.RoleUser}, []tenantauth.Role{tenantauth.RoleUser}},
	} {
		if err := authority.ApplyDelta(ctx, tc.tenant, tc.subject, tc.grant, tc.revoke); err == nil {
			t.Fatal("accepted invalid delta")
		}
	}
	if fake.writeCount() != 0 {
		t.Fatal("invalid boundary mutated authority")
	}
}

func TestAccessAuthorityRequiresExplicitNativeMemberStatus(t *testing.T) {
	for _, test := range []struct {
		name         string
		enabled      *bool
		wantDisabled bool
		wantError    bool
	}{
		{"enabled", func() *bool { v := true; return &v }(), false, false},
		{"disabled", func() *bool { v := false; return &v }(), true, false},
		{"missing status", nil, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority, _ := accessAuthorityFixture(t)
			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/token") {
					writeJSON(w, 200, map[string]any{"access_token": "test-token", "expires_in": 300, "token_type": "Bearer"})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/groups") {
					writeJSON(w, 200, []groupRepresentation{})
					return
				}
				writeJSON(w, 200, accessMember{ID: membershipTestSubject, Enabled: test.enabled})
			}))
			defer source.Close()
			authority.enrollment.client.config.BaseURL = source.URL
			authority.enrollment.client.http = source.Client()
			session, err := authority.enrollment.client.session(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			account, err := readAccessAccount(context.Background(), session, accessOrganization{base: "/organizations/selected"}, membershipTestSubject)
			if test.wantError {
				if !errors.Is(err, ErrMembershipTopology) {
					t.Fatalf("missing status accepted: %v", err)
				}
				return
			}
			if err != nil || !account.Member || account.Disabled != test.wantDisabled {
				t.Fatalf("native status=%+v err=%v", account, err)
			}
		})
	}
}
