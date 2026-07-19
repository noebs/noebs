package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/fiber/v2"
)

func TestWorkloadCapabilitiesMatchReviewedMatrix(t *testing.T) {
	expected := make(map[string]bool)
	add := func(role serviceRole, caller, method, path string) {
		expected[fmt.Sprintf("%s|%s|%s|%s", role, caller, method, path)] = true
	}
	for _, spec := range gatewayProxyRouteSpecs() {
		add(spec.role, string(serviceRoleAPIGateway), spec.method, spec.path)
	}
	add(serviceRoleIdentityAuth, string(serviceRoleAPIGateway), http.MethodPost, "/internal/identity-auth/principals/resolve")
	add(serviceRoleIdentityAuth, string(serviceRoleNotification), http.MethodPost, "/internal/identity-auth/users/resolve-batch")
	add(serviceRoleIdentityAuth, string(serviceRoleEBSAdapter), http.MethodPost, "/internal/identity-auth/users/by-mobile")
	add(serviceRoleCardVault, string(serviceRoleIdentityAuth), http.MethodPost, "/internal/card-vault/cards/masked")
	for _, path := range []string{
		"/internal/card-vault/enrollment-intents",
		"/internal/card-vault/enrollment-intents/begin",
		"/internal/card-vault/enrollment-intents/claim-rail",
		"/internal/card-vault/enrollment-intents/complete",
		"/internal/card-vault/enrollment-intents/fail",
		"/internal/card-vault/funded-operations/claim",
	} {
		add(serviceRoleCardVault, string(serviceRoleEBSAdapter), http.MethodPost, path)
	}
	add(serviceRoleNotification, string(serviceRoleEBSAdapter), http.MethodPost, "/internal/notification-chat/push-data")
	add(serviceRoleNotification, string(serviceRoleEBSAdapter), http.MethodPost, "/internal/notification-chat/biller-hook")

	receivers := []serviceRole{
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
		serviceRoleWalletAPI,
	}
	actual := make(map[string]bool)
	for _, role := range receivers {
		for _, capability := range workloadCapabilities(role) {
			key := fmt.Sprintf("%s|%s|%s|%s", role, capability.caller, capability.method, capability.path)
			if actual[key] {
				t.Fatalf("duplicate capability %s", key)
			}
			actual[key] = true
		}
	}
	for key := range expected {
		if !actual[key] {
			t.Errorf("missing capability %s", key)
		}
	}
	for key := range actual {
		if !expected[key] {
			t.Errorf("unreviewed capability %s", key)
		}
	}
}

func TestSignedWorkloadBoundaryVerifiesBeforeExactCapability(t *testing.T) {
	tests := []struct {
		name       string
		caller     string
		audience   string
		method     string
		target     string
		mutate     func(*http.Request)
		wantStatus int
	}{
		{
			name:       "valid dynamic card route",
			caller:     string(serviceRoleAPIGateway),
			audience:   string(serviceRoleCardVault),
			method:     http.MethodPatch,
			target:     "/consumer/cards/019f73cf-203f-7eb1-b6a8-d50d75ca3a9f?view=compact&view=full",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "encoded slash cannot widen dynamic segment",
			caller:     string(serviceRoleAPIGateway),
			audience:   string(serviceRoleCardVault),
			method:     http.MethodPatch,
			target:     "/consumer/cards/a%2Fb",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "encoded dot segment rejected",
			caller:     string(serviceRoleAPIGateway),
			audience:   string(serviceRoleCardVault),
			method:     http.MethodPatch,
			target:     "/consumer/cards/%2e",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "nested encoded slash rejected",
			caller:     string(serviceRoleAPIGateway),
			audience:   string(serviceRoleCardVault),
			method:     http.MethodPatch,
			target:     "/consumer/cards/a%252Fb",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "nested encoded dot dot rejected",
			caller:     string(serviceRoleAPIGateway),
			audience:   string(serviceRoleCardVault),
			method:     http.MethodPatch,
			target:     "/consumer/cards/%252e%252e",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "invalid utf8 path rejected",
			caller:     string(serviceRoleAPIGateway),
			audience:   string(serviceRoleCardVault),
			method:     http.MethodPatch,
			target:     "/consumer/cards/%FF",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "wrong caller capability",
			caller:     string(serviceRoleEBSAdapter),
			audience:   string(serviceRoleCardVault),
			method:     http.MethodPatch,
			target:     "/consumer/cards/019f73cf-203f-7eb1-b6a8-d50d75ca3a9f",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "legacy internal route has no capability",
			caller:     string(serviceRoleEBSAdapter),
			audience:   string(serviceRoleCardVault),
			method:     http.MethodPost,
			target:     "/internal/card-vault/cards/by-mobile-pan",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "wrong audience",
			caller:     string(serviceRoleAPIGateway),
			audience:   string(serviceRoleIdentityAuth),
			method:     http.MethodPatch,
			target:     "/consumer/cards/019f73cf-203f-7eb1-b6a8-d50d75ca3a9f",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "unknown key",
			caller:     "unknown-workload",
			audience:   string(serviceRoleCardVault),
			method:     http.MethodPatch,
			target:     "/consumer/cards/019f73cf-203f-7eb1-b6a8-d50d75ca3a9f",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:     "raw query order mutation",
			caller:   string(serviceRoleAPIGateway),
			audience: string(serviceRoleCardVault),
			method:   http.MethodPatch,
			target:   "/consumer/cards/019f73cf-203f-7eb1-b6a8-d50d75ca3a9f?z=last&z=first",
			mutate: func(req *http.Request) {
				req.URL.RawQuery = "z=first&z=last"
				req.RequestURI = req.URL.RequestURI()
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:     "duplicate asserted identity header",
			caller:   string(serviceRoleAPIGateway),
			audience: string(serviceRoleCardVault),
			method:   http.MethodPatch,
			target:   "/consumer/cards/019f73cf-203f-7eb1-b6a8-d50d75ca3a9f",
			mutate: func(req *http.Request) {
				req.Header[workloadauth.HeaderUserID] = []string{"42", "42"}
			},
			wantStatus: http.StatusUnauthorized,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := newTestWorkloadVerifier(t, string(serviceRoleCardVault), string(serviceRoleAPIGateway), string(serviceRoleEBSAdapter))
			app := fiber.New(fiber.Config{DisableStartupMessage: true})
			app.Use(signedWorkloadBoundary(serviceRoleCardVault, verifier))
			app.Use(func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })

			req := newSignedTestRequest(t, test.caller, test.audience, test.method, test.target, []byte(`{"name":"primary"}`))
			if test.mutate != nil {
				test.mutate(req)
			}
			response, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.wantStatus {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status = %d, want %d: %s", response.StatusCode, test.wantStatus, body)
			}
		})
	}
}

func TestSignedWorkloadBoundaryFailsClosedWhenVerifierUnavailable(t *testing.T) {
	tests := []struct {
		name     string
		verifier workloadRequestVerifier
	}{
		{name: "missing verifier"},
		{name: "nonce database unavailable", verifier: fixedErrorWorkloadVerifier{err: fmt.Errorf("%w: database unavailable", workloadauth.ErrNonceStore)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var hits atomic.Int32
			app := fiber.New(fiber.Config{DisableStartupMessage: true})
			app.Use(signedWorkloadBoundary(serviceRoleCardVault, test.verifier))
			app.Use(func(c *fiber.Ctx) error {
				hits.Add(1)
				return c.SendStatus(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPatch, "/consumer/cards/card-1", nil)
			response, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
			}
			if hits.Load() != 0 {
				t.Fatalf("handler hits = %d", hits.Load())
			}
		})
	}
}

type fixedErrorWorkloadVerifier struct{ err error }

func (v fixedErrorWorkloadVerifier) Verify(*http.Request, []byte) (workloadauth.Principal, error) {
	return workloadauth.Principal{}, v.err
}

func TestSignedWorkloadBoundaryClaimsReplayBeforeHandler(t *testing.T) {
	verifier := newTestWorkloadVerifier(t, string(serviceRoleCardVault), string(serviceRoleAPIGateway))
	var hits atomic.Int32
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(signedWorkloadBoundary(serviceRoleCardVault, verifier))
	app.Patch("/consumer/cards/:card_id", func(c *fiber.Ctx) error {
		hits.Add(1)
		return c.SendStatus(http.StatusNoContent)
	})
	body := []byte(`{"name":"primary"}`)
	original := newSignedTestRequest(t, string(serviceRoleAPIGateway), string(serviceRoleCardVault), http.MethodPatch, "/consumer/cards/card-1", body)
	for attempt := 0; attempt < 2; attempt++ {
		req := original.Clone(original.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
		response, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		want := http.StatusNoContent
		if attempt == 1 {
			want = http.StatusUnauthorized
		}
		if response.StatusCode != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.StatusCode, want)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("handler hits = %d, want 1", hits.Load())
	}
}

func TestGatewayStripsPublicCredentialsThenPropagatesSignature(t *testing.T) {
	verifier := newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleAPIGateway))
	var sawRequest atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		principal, err := verifier.Verify(r, body)
		if err != nil {
			t.Errorf("verify gateway request: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if principal.Caller != string(serviceRoleAPIGateway) || r.Header.Get(workloadauth.HeaderTenantID) != "tenant_1" || r.Header.Get(workloadauth.HeaderUserID) != "" {
			t.Errorf("unexpected signed gateway identity: caller=%q tenant=%q user=%q", principal.Caller, r.Header.Get(workloadauth.HeaderTenantID), r.Header.Get(workloadauth.HeaderUserID))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.URL.RawQuery != "flow=login&view=compact&view=full" {
			t.Errorf("raw query = %q", r.URL.RawQuery)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sawRequest.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	discovery := make(map[string]string)
	for _, spec := range gatewayProxyRouteSpecs() {
		discovery[string(spec.role)] = upstream.URL
	}
	previousSigners := workloadSigners
	workloadSigners = newTestWorkloadSigners(t, string(serviceRoleAPIGateway),
		string(serviceRoleIdentityAuth), string(serviceRoleCardVault), string(serviceRoleEBSAdapter),
		string(serviceRolePSPWebhook), string(serviceRoleAdminReporting), string(serviceRoleNotification),
		string(serviceRoleBeneficiary), string(serviceRoleWalletAPI))
	t.Cleanup(func() { workloadSigners = previousSigners })

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(gateway.RequestID())
	if err := registerAPIGatewayProxyRoutes(app, ebs_fields.NoebsConfig{
		DefaultTenantID:  "tenant_1",
		ServiceDiscovery: discovery,
	}, gateway.JWTAuth{}, func(c *fiber.Ctx) error { return c.Next() }); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/consumer/login?flow=login&view=compact&view=full", strings.NewReader(`{"mobile":"0990000000"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant_1")
	req.Header.Set(workloadauth.HeaderKeyID, "attacker-key")
	req.Header.Set(workloadauth.HeaderSignature, "attacker-signature")
	req.Header.Set(workloadauth.HeaderUserID, "999")
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent || !sawRequest.Load() {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, saw upstream=%t: %s", response.StatusCode, sawRequest.Load(), body)
	}
}

func TestGatewayDoesNotProxyWithoutWorkloadSigner(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	discovery := make(map[string]string)
	for _, spec := range gatewayProxyRouteSpecs() {
		discovery[string(spec.role)] = upstream.URL
	}
	previousSigners := workloadSigners
	workloadSigners = nil
	t.Cleanup(func() { workloadSigners = previousSigners })

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(gateway.RequestID())
	if err := registerAPIGatewayProxyRoutes(app, ebs_fields.NoebsConfig{
		DefaultTenantID:  "tenant_1",
		ServiceDiscovery: discovery,
	}, gateway.JWTAuth{}, func(c *fiber.Ctx) error { return c.Next() }); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/consumer/login", strings.NewReader(`{"mobile":"0990000000"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant_1")
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadGateway)
	}
	if hits.Load() != 0 {
		t.Fatalf("upstream hits = %d", hits.Load())
	}
}

func newSignedTestRequest(t *testing.T, caller, audience, method, target string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(workloadauth.HeaderRequestID, "request-"+strings.ReplaceAll(t.Name(), "/", "-"))
	req.Header.Set(workloadauth.HeaderTenantID, "tenant_1")
	req.Header.Set(workloadauth.HeaderUserID, "42")
	signers := newTestWorkloadSigners(t, caller, audience)
	if err := signers.Sign(audience, req, body); err != nil {
		t.Fatal(err)
	}
	return req
}
