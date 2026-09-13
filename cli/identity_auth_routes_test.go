package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	consumerhandler "github.com/adonese/noebs/consumer/handler"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/fiber/v2"
)

type routeEnrollmentFixture struct {
	progress *accountenrollment.Progress
	member   bool
}

func (f *routeEnrollmentFixture) Inspect(context.Context, string) (accountenrollment.Account, error) {
	memberships := map[string][]string{}
	if f.member {
		memberships["noebs"] = []string{"user"}
	}
	return accountenrollment.Account{Email: "signup@example.test", EmailVerified: true, Memberships: memberships}, nil
}
func (f *routeEnrollmentFixture) EnsureUserMembership(context.Context, string, string) error {
	f.member = true
	return nil
}
func (f *routeEnrollmentFixture) WithIdentity(_ context.Context, _ accountenrollment.Identity, fn func(accountenrollment.ProgressAccess) error) error {
	return fn(f)
}
func (f *routeEnrollmentFixture) Load(context.Context) (*accountenrollment.Progress, error) {
	return f.progress, nil
}
func (f *routeEnrollmentFixture) Save(_ context.Context, progress accountenrollment.Progress) error {
	f.progress = &progress
	return nil
}

func TestIdentityAuthRoutesPermitSignedEnrollmentBeforeTenantMembership(t *testing.T) {
	previousConfig, previousService := noebsConfig, accountEnrollmentService
	t.Cleanup(func() { noebsConfig, accountEnrollmentService = previousConfig, previousService })
	policy := accountenrollment.RuntimeConfig{
		Enabled: true, Issuer: "https://example.test/auth/realms/noebs",
		TenantIDs: []string{"noebs"}, AllowVerifiedAccounts: true,
		KeycloakBaseURL: "https://keycloak.test/auth", KeycloakClientID: "noebs-account-enroller", KeycloakClientSecret: "synthetic",
	}
	noebsConfig = ebs_fields.NoebsConfig{AccountEnrollment: policy}
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "noebs", Name: "Noebs"}})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &routeEnrollmentFixture{}
	accountEnrollmentService, err = accountenrollment.New(policy, catalog, fixture, fixture)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{ErrorHandler: gateway.JSONErrorHandler})
	app.Use(signedWorkloadBoundary(serviceRoleIdentityAuth, newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleAPIGateway))))
	// Use production registration and real principal/user middleware: registering
	// the shared /internal/identity-auth group first must fail this regression.
	registerIdentityAuthRoutes(app, gateway.InternalPrincipalIdentityMiddleware(), gateway.InternalUserIdentityMiddleware(), &consumerhandler.Handler{})
	signers := newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleIdentityAuth))
	for _, tc := range []struct {
		name, method, path, body, enrollmentStatus string
		signed                                     bool
		status                                     int
	}{
		{"unsigned context", "GET", accountContextInternalPath, "", "", false, 401},
		{"unsigned enrollment", "POST", accountEnrollmentInternalPath, "{}", "", false, 401},
		{"unassigned context", "GET", accountContextInternalPath, "", "required", true, 200},
		{"unassigned enrollment", "POST", accountEnrollmentInternalPath, "{}", "complete", true, 200},
		{"tenant principal still required", "POST", "/internal/identity-auth/principals/resolve", "{}", "", true, 401},
		{"profile still requires membership", "POST", "/consumer/auth/profile", "{}", "", true, 401},
		{"user still requires membership", "GET", "/consumer/user", "", "", true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(workloadauth.HeaderRequestID, "signup-route-regression")
			request.Header.Set(workloadauth.HeaderTenantID, "noebs")
			request.Header.Set(workloadauth.HeaderIssuer, policy.Issuer)
			request.Header.Set(workloadauth.HeaderSubject, "unassigned-subject")
			request.Header.Set(workloadauth.HeaderAuthorizedParty, "noebs-web")
			request.Header.Set(workloadauth.HeaderSourceIP, "203.0.113.9")
			request.Header.Set(workloadauth.HeaderTokenExpiresAt, strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
			if tc.signed {
				if err := signers.Sign(string(serviceRoleIdentityAuth), request, []byte(tc.body)); err != nil {
					t.Fatal(err)
				}
			}
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d", response.StatusCode, tc.status)
			}
			if tc.status == http.StatusOK {
				var result accountenrollment.Context
				if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.EnrollmentStatus != tc.enrollmentStatus {
					t.Fatalf("context=%+v error=%v", result, err)
				}
			}
		})
	}
}
