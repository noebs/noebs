package keycloakadmin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/tenantaccess"
)

func TestBootstrapOperationProofIsAdminOnlyProfileData(t *testing.T) {
	attributes := desiredLocalAccountProfile()["attributes"].([]map[string]any)
	count := 0
	for _, attribute := range attributes {
		if attribute["name"] != bootstrapOperationAttribute {
			continue
		}
		count++
		expected := map[string]any{"view": []string{"admin"}, "edit": []string{"admin"}}
		if !reflect.DeepEqual(attribute["permissions"], expected) || attribute["required"] != nil || attribute["multivalued"] != false {
			t.Fatal("operation proof is editable/required for consumer registration")
		}
	}
	if count != 1 {
		t.Fatal("bootstrap operation marker missing or ambiguous")
	}
}

func TestPrepareOperatorRejectsCollisionsWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		emailUser, usernameUser *operatorIdentity
		expected                string
	}{
		{name: "preclaimed exact identity", emailUser: &operatorIdentity{ID: membershipTestSubject, Email: BootstrapOperatorEmail, Username: BootstrapOperatorUsername, Enabled: true}, usernameUser: &operatorIdentity{ID: membershipTestSubject, Email: BootstrapOperatorEmail, Username: BootstrapOperatorUsername, Enabled: true}},
		{name: "email belongs to different handle", emailUser: &operatorIdentity{ID: membershipTestSubject, Email: BootstrapOperatorEmail, Username: "consumer", Enabled: true}},
		{name: "handle belongs to different email", usernameUser: &operatorIdentity{ID: membershipTestSubject, Email: "other@example.test", Username: BootstrapOperatorUsername, Enabled: true}},
		{name: "known subject disappeared", expected: membershipTestSubject},
		{name: "disabled operator", emailUser: &operatorIdentity{ID: membershipTestSubject, Email: BootstrapOperatorEmail, Username: BootstrapOperatorUsername}, usernameUser: &operatorIdentity{ID: membershipTestSubject, Email: BootstrapOperatorEmail, Username: BootstrapOperatorUsername}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			state := membershipTestDesiredState(t)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/realms/noebs/protocol/openid-connect/token" {
					writeJSON(w, 200, map[string]string{"access_token": "fixture", "token_type": "Bearer"})
					return
				}
				if r.Method != http.MethodGet {
					writes++
					w.WriteHeader(500)
					return
				}
				if r.URL.Path == "/admin/realms/noebs/users" {
					user := tc.emailUser
					if r.URL.Query().Get("username") != "" {
						user = tc.usernameUser
					}
					users := []operatorIdentity{}
					if user != nil {
						users = append(users, *user)
					}
					writeJSON(w, 200, users)
					return
				}
				// A profile read is only used by the preclaim case before ownership check.
				if r.URL.Path == "/admin/realms/noebs/users/"+membershipTestSubject {
					writeJSON(w, 200, tc.emailUser)
					return
				}
				if r.URL.Path == "/admin/realms/noebs/organizations" {
					writeJSON(w, 200, []organizationRepresentation{{ID: "org-a", Alias: "noebs"}, {ID: "org-b", Alias: "tenant-sandbox"}})
					return
				}
				w.WriteHeader(404)
			}))
			defer server.Close()
			policy := accountenrollment.RuntimeConfig{Enabled: true, Issuer: "https://api.noebs.sd/auth/realms/noebs", TenantIDs: []string{"noebs"}, AllowedSubjects: []string{membershipTestSubject}, KeycloakBaseURL: server.URL, KeycloakClientID: "noebs-account-enroller", KeycloakClientSecret: "fixture"}
			authority, err := NewAccessAuthority(policy, state.tenantCatalog, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = authority.PrepareOperator(context.Background(), "noebs", BootstrapOperatorEmail, tc.expected, "22222222-2222-4222-8222-222222222222")
			if !errors.Is(err, tenantaccess.ErrOperationConflict) || writes != 0 {
				t.Fatalf("collision err=%v writes=%d", err, writes)
			}
		})
	}
}
