package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/fiber/v2"
)

func TestAccountDisplayNameResolverSignsActorAndExactTarget(t *testing.T) {
	verifier := newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleAPIGateway))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if r.Method != http.MethodPost || r.URL.Path != accountDisplayNamePath || string(body) != `{"user_id":7}` {
			t.Errorf("unexpected exact-owner name request: %s %s %s", r.Method, r.URL.Path, body)
		}
		if _, err := verifier.Verify(r, body); err != nil {
			t.Errorf("signed body verification: %v", err)
		}
		if r.Header.Get(workloadauth.HeaderUserID) != "42" || r.Header.Get(workloadauth.HeaderSubject) != "subject-1" || r.Header.Get(workloadauth.HeaderTenantID) != "tenant-a" {
			t.Error("recipient lookup impersonated recipient or lost actor tenant")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"display_name":"Recipient account"}`))
	}))
	defer upstream.Close()
	r := &identityProfileProjectionResolver{endpoint: upstream.URL, client: upstream.Client(),
		signers: newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleIdentityAuth))}
	name, err := r.AccountDisplayName(context.Background(), gatewayPrincipalForTest(t), 42, 7, "name-lookup-1", "203.0.113.9")
	if err != nil || name != "Recipient account" {
		t.Fatalf("lookup = %q, %v", name, err)
	}
}

func TestAccountDisplayNameResolverRejectsBroadOrMalformedResponses(t *testing.T) {
	for _, body := range []string{
		`{"display_name":"Recipient","email":"private@example.test"}`,
		`{"display_name":"Recipient"}{}`, `{"display_name":""}`, `{"display_name":" Recipient "}`,
	} {
		t.Run(body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer upstream.Close()
			r := &identityProfileProjectionResolver{endpoint: upstream.URL, client: upstream.Client(),
				signers: newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleIdentityAuth))}
			if _, err := r.AccountDisplayName(context.Background(), gatewayPrincipalForTest(t), 42, 7, "name-lookup-2", "203.0.113.9"); err == nil {
				t.Fatal("invalid narrow response accepted")
			}
		})
	}
}

type recordingDisplayNameResolver struct {
	t           *testing.T
	target      int64
	calls       int
	unavailable bool
}

func (r *recordingDisplayNameResolver) AccountDisplayName(_ context.Context, principal tenantauth.Principal, actorID, targetID int64, _, _ string) (string, error) {
	r.calls++
	if actorID != 42 || targetID != r.target || principal.Tenant() != walletAuthorizationTestTenant {
		r.t.Errorf("actor/target mismatch: actor %d target %d tenant %s", actorID, targetID, principal.Tenant())
	}
	if r.unavailable {
		return "", errors.New("identity unavailable")
	}
	return "Canonical account name", nil
}

func TestGatewayAccountNameEnrichmentFollowsLedgerAuthority(t *testing.T) {
	const reference = "550e8400-e29b-41d4-a716-446655440000"
	for _, tc := range []struct {
		name, method, path, body          string
		upstreamStatus, wantStatus, calls int
		target                            int64
		unavailable, nameExpected         bool
	}{
		{"preview", http.MethodPost, "/wallet/p2p/preview", `{"to_owner_id":"7","recipient_name":"","amount_minor":"9007199254740993"}`, 200, 200, 1, 7, false, true},
		{"preview requires name", http.MethodPost, "/wallet/p2p/preview", `{"to_owner_id":"7","recipient_name":""}`, 200, 502, 1, 7, true, false},
		{"ledger rejection no lookup", http.MethodPost, "/wallet/p2p/preview", `{"code":"recipient_unavailable"}`, 404, 404, 0, 7, false, false},
		{"durable success survives name outage", http.MethodGet, "/wallet/p2p/status", `{"to_owner_id":"7","recipient_name":"","status":"succeeded","transaction_id":"99"}`, 200, 200, 1, 7, true, false},
		{"legacy status without numeric name owner", http.MethodGet, "/wallet/p2p/status", `{"to_owner_id":"legacy-owner","recipient_name":"","status":"succeeded","transaction_id":"99"}`, 200, 200, 0, 7, false, false},
		{"funding own reference", http.MethodGet, "/wallet/funding-methods", `{"methods":[{"id":"noebs:receive","provider_id":"noebs","mode":"account_transfer","available":true,"account_identifier":"` + reference + `","account_name":""}]}`, 200, 200, 1, 42, false, true},
		{"unavailable receiving no lookup", http.MethodGet, "/wallet/funding-methods", `{"methods":[{"id":"noebs:receive","provider_id":"noebs","mode":"account_transfer","available":false,"account_identifier":""}]}`, 200, 200, 0, 42, false, false},
		{"custom external name untouched", http.MethodGet, "/wallet/funding-methods", `{"methods":[{"id":"mojaloop:receive","provider_id":"mojaloop","mode":"external_transfer","available":true,"account_identifier":"249900000088","account_name":"Custom alias"}]}`, 200, 200, 0, 42, false, false},
		{"invalid target owner", http.MethodPost, "/wallet/p2p/preview", `{"to_owner_id":"not-a-user"}`, 200, 502, 0, 7, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier, token := walletAuthorizationOIDCVerifierAndTokenForTest(t)
			auth, err := gateway.NewOIDCAuthMiddleware(gateway.OIDCAuthConfig{Verifier: verifier,
				SelectTenant:   func(*fiber.Ctx) (string, error) { return walletAuthorizationTestTenant, nil },
				AllowedClients: []string{"noebs-mobile"}, AllowedRoles: []tenantauth.Role{tenantauth.RoleUser}})
			if err != nil {
				t.Fatal(err)
			}
			r := &recordingDisplayNameResolver{t: t, target: tc.target, unavailable: tc.unavailable}
			app := fiber.New(fiber.Config{DisableStartupMessage: true, ErrorHandler: gateway.JSONErrorHandler})
			app.Add(tc.method, tc.path, auth, func(c *fiber.Ctx) error {
				c.Request().Header.Set(workloadauth.HeaderUserID, "42")
				c.Request().Header.Set(workloadauth.HeaderSourceIP, "203.0.113.9")
				return c.Next()
			}, gatewayAccountNameHandler(gatewayRouteSpec{method: tc.method, path: tc.path}, r, func(c *fiber.Ctx) error {
				c.Set("Content-Type", "application/json")
				return c.Status(tc.upstreamStatus).SendString(tc.body)
			}))
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.wantStatus || r.calls != tc.calls {
				t.Fatalf("status/calls = %d/%d want %d/%d; %s", response.StatusCode, r.calls, tc.wantStatus, tc.calls, body)
			}
			if strings.Contains(string(body), "Canonical account name") != tc.nameExpected {
				t.Fatalf("name enrichment mismatch: %s", body)
			}
			if tc.name == "durable success survives name outage" && (!strings.Contains(string(body), `"status":"succeeded"`) || !strings.Contains(string(body), `"transaction_id":"99"`)) {
				t.Fatalf("durable outcome lost: %s", body)
			}
			if tc.name == "preview" {
				var result map[string]json.RawMessage
				if json.Unmarshal(body, &result) != nil || string(result["amount_minor"]) != `"9007199254740993"` {
					t.Fatalf("money precision changed: %s", body)
				}
			}
			if tc.name == "custom external name untouched" && string(body) != tc.body {
				t.Fatalf("external name changed: %s", body)
			}
		})
	}
}

func TestAccountDisplayNameInternalCapabilityRemainsGatewayOnly(t *testing.T) {
	capabilities := workloadCapabilities(serviceRoleIdentityAuth)
	if !authorizeWorkload(capabilities, string(serviceRoleAPIGateway), http.MethodPost, accountDisplayNamePath) {
		t.Fatal("gateway name capability missing")
	}
	for _, caller := range []string{string(serviceRoleWalletAPI), string(serviceRoleEBSAdapter), string(serviceRoleNotification)} {
		if authorizeWorkload(capabilities, caller, http.MethodPost, accountDisplayNamePath) {
			t.Fatalf("unauthorized name caller %s", caller)
		}
	}
	for _, spec := range gatewayProxyRouteSpecs() {
		if spec.path == accountDisplayNamePath {
			t.Fatal("private identity lookup exposed as public gateway route")
		}
	}
}
