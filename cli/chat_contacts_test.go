package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/fiber/v2"
)

type fixedContactResolver struct {
	users   []consumer.IdentityUserByMobileResult
	err     error
	tenant  string
	mobiles []string
}

func (r *fixedContactResolver) Resolve(_ context.Context, tenantID string, mobiles []string) ([]consumer.IdentityUserByMobileResult, error) {
	r.tenant = tenantID
	r.mobiles = append([]string(nil), mobiles...)
	return append([]consumer.IdentityUserByMobileResult(nil), r.users...), r.err
}

func TestSubmitChatContactsResolvesThenStoresTenantScopedIDs(t *testing.T) {
	ensureInit()
	for _, tenantID := range []string{"chat-tenant-a", "chat-tenant-b"} {
		resolver := &fixedContactResolver{users: []consumer.IdentityUserByMobileResult{
			{UserID: 84, Mobile: "0912141660"},
		}}
		response := submitChatContactsRequest(t, tenantID, 42, resolver, `[
			{"name":"One","mobile":"0912141660"},
			{"name":"Duplicate","mobile":"0912141660"},
			{"name":"Unknown","mobile":"0912141661"}
		]`)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", tenantID, response.Code, http.StatusOK)
		}
		if resolver.tenant != tenantID || !reflect.DeepEqual(resolver.mobiles, []string{"0912141660", "0912141661"}) {
			t.Fatalf("resolver input = tenant:%q mobiles:%v", resolver.tenant, resolver.mobiles)
		}
		var contacts []chatContactResponse
		if err := json.Unmarshal(response.Body.Bytes(), &contacts); err != nil {
			t.Fatalf("decode contacts: %v", err)
		}
		if len(contacts) != 1 || contacts[0].UserID != 84 || contacts[0].Name != "One" {
			t.Fatalf("contacts = %+v", contacts)
		}
	}

	var count int
	if err := database.DB.Get(&count, `
		SELECT COUNT(*) FROM contacts_v2
		WHERE owner_user_id = 42 AND contact_user_id = 84
		  AND tenant_id IN ('chat-tenant-a', 'chat-tenant-b')`); err != nil {
		t.Fatalf("count tenant contacts: %v", err)
	}
	if count != 2 {
		t.Fatalf("tenant-scoped rows = %d, want 2", count)
	}
}

func TestSubmitChatContactsFailsBeforePersistenceWhenResolutionFails(t *testing.T) {
	ensureInit()
	const tenantID = "chat-resolution-failure"
	response := submitChatContactsRequest(t, tenantID, 42, &fixedContactResolver{err: errors.New("identity unavailable")},
		`[{"name":"One","mobile":"0912141660"}]`)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	var count int
	if err := database.DB.Get(&count, "SELECT COUNT(*) FROM contacts_v2 WHERE tenant_id = $1", tenantID); err != nil {
		t.Fatalf("count contacts: %v", err)
	}
	if count != 0 {
		t.Fatalf("rows persisted after resolver failure = %d", count)
	}
}

func TestSubmitChatContactsRejectsAmbiguousWireShapesBeforeResolution(t *testing.T) {
	ensureInit()
	tooMany := strings.Repeat(`{"name":"One","mobile":"0912141660"},`, maxChatContacts) +
		`{"name":"Last","mobile":"0912141661"}`
	tests := []string{
		`[]`,
		`[{"name":"One","mobile":"0912141660","user_id":999}]`,
		`[{"name":"One","mobile":"0912141660"}] {}`,
		`[` + tooMany + `]`,
		`[{"name":"One","mobile":"6011000073184629"}]`,
	}
	for index, body := range tests {
		resolver := &fixedContactResolver{}
		response := submitChatContactsRequest(t, "chat-invalid-input", 42, resolver, body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("case %d status = %d, want %d", index, response.Code, http.StatusBadRequest)
		}
		if resolver.tenant != "" || len(resolver.mobiles) != 0 {
			t.Fatalf("case %d reached resolver: %+v", index, resolver)
		}
	}
}

func TestIdentityContactResolverUsesSignedTenantScopedBatch(t *testing.T) {
	verifier := newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleNotification))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := verifier.VerifyRequest(r)
		if err != nil || principal.Caller != string(serviceRoleNotification) {
			t.Errorf("verify signed resolver request: principal=%+v err=%v", principal, err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/internal/identity-auth/users/resolve-batch" ||
			r.Header.Get(gateway.GatewayTenantIDHeader) != "chat-tenant" ||
			r.Header.Get(gateway.GatewaySessionTokenHeader) != "" {
			t.Errorf("unexpected resolver request: %s %s headers=%v", r.Method, r.URL.Path, r.Header)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var command consumer.IdentityUsersBatchCommand
		if err := json.NewDecoder(r.Body).Decode(&command); err != nil || !reflect.DeepEqual(command.Mobiles, []string{"0912141660"}) {
			t.Errorf("command = %+v, err=%v", command, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(consumer.IdentityUsersBatchResult{Users: []consumer.IdentityUserByMobileResult{
			{UserID: 84, Mobile: "0912141660"},
		}})
	}))
	t.Cleanup(server.Close)
	resolver, err := newIdentityContactResolver(ebs_fields.NoebsConfig{
		ServiceDiscovery: map[string]string{string(serviceRoleIdentityAuth): server.URL},
	}, newTestWorkloadSigners(t, string(serviceRoleNotification), string(serviceRoleIdentityAuth)))
	if err != nil {
		t.Fatalf("newIdentityContactResolver(): %v", err)
	}
	users, err := resolver.Resolve(context.Background(), "chat-tenant", []string{"0912141660"})
	if err != nil {
		t.Fatalf("Resolve(): %v", err)
	}
	if len(users) != 1 || users[0].UserID != 84 {
		t.Fatalf("users = %+v", users)
	}
}

func TestIdentityContactResolverRejectsForgedResponseMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(consumer.IdentityUsersBatchResult{Users: []consumer.IdentityUserByMobileResult{
			{UserID: 84, Mobile: "0999999999"},
		}})
	}))
	t.Cleanup(server.Close)
	resolver, err := newIdentityContactResolver(ebs_fields.NoebsConfig{
		ServiceDiscovery: map[string]string{string(serviceRoleIdentityAuth): server.URL},
	}, newTestWorkloadSigners(t, string(serviceRoleNotification), string(serviceRoleIdentityAuth)))
	if err != nil {
		t.Fatalf("newIdentityContactResolver(): %v", err)
	}
	if _, err := resolver.Resolve(context.Background(), "chat-tenant", []string{"0912141660"}); err == nil {
		t.Fatal("forged resolver response was accepted")
	}
}

func TestIdentityContactResolverFailsClosedWithoutSigner(t *testing.T) {
	_, err := newIdentityContactResolver(ebs_fields.NoebsConfig{}, nil)
	if !errors.Is(err, workloadauth.ErrMissingSigner) {
		t.Fatalf("error = %v, want %v", err, workloadauth.ErrMissingSigner)
	}
}

func submitChatContactsRequest(t *testing.T, tenantID string, userID int64, resolver contactIdentityResolver, body string) *httptest.ResponseRecorder {
	t.Helper()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/contacts", func(c *fiber.Ctx) error {
		c.Locals("tenant_id", tenantID)
		c.Locals("user_id", userID)
		return submitChatContacts(c, resolver, database.DB)
	})
	req := httptest.NewRequest(http.MethodPost, "/contacts", strings.NewReader(body))
	response, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test(): %v", err)
	}
	recorder := httptest.NewRecorder()
	recorder.Code = response.StatusCode
	if _, err := recorder.Body.ReadFrom(response.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	_ = response.Body.Close()
	return recorder
}
