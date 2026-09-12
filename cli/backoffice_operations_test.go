package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/gofiber/fiber/v2"
)

func TestBackofficeOperationMutationsRequireCSRFAndTenantMembership(t *testing.T) {
	claims := backofficeBoundaryClaims(t, []backofficeBoundaryMembership{{tenant: "tenant-a", organization: "org-a", roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, permissions: []tenantauth.Permission{tenantauth.PermissionIdentityReviewRead, tenantauth.PermissionIdentityReviewDecide, tenantauth.PermissionWalletTransactionResolve}}})
	fixture := newBackofficeBoundaryFixture(t, claims)
	cookie, csrf := fixture.completeSession(t, claims, backofficeHomePath)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) }))
	defer upstream.Close()
	previous := workloadSigners
	previousTLS := internalTransportClientTLS
	internalTransportClientTLS = nil
	workloadSigners = newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleWalletAPI), string(serviceRoleAdminReporting), string(serviceRoleIdentityAuth))
	t.Cleanup(func() { workloadSigners = previous; internalTransportClientTLS = previousTLS })
	app := fiber.New()
	if err := registerBackofficeProxyRoutes(app, ebs_fields.NoebsConfig{ServiceDiscovery: map[string]string{string(serviceRoleWalletAPI): upstream.URL, string(serviceRoleAdminReporting): upstream.URL, string(serviceRoleIdentityAuth): upstream.URL}}, fixture.handler); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/backoffice/t/tenant-a/verifications/42/43c5e722-e622-4c3c-a3c6-5fa8c954e49d/decision", "/backoffice/t/tenant-a/wallet/transactions/ref-1/resolve"} {
		for _, tc := range []struct {
			name, path, token, origin string
			want                      int
		}{
			{"missing csrf", path, "", "https://" + backofficeBoundaryHost, 403},
			{"foreign origin", path, csrf, "https://other.example", 403},
			{"other tenant", strings.Replace(path, "tenant-a", "tenant-b", 1), csrf, "https://" + backofficeBoundaryHost, 403},
			{"authorized", path, csrf, "https://" + backofficeBoundaryHost, 204},
		} {
			before := calls
			request := backofficeBoundaryRequest(t, http.MethodPost, tc.path, strings.NewReader(url.Values{"_csrf": {tc.token}}.Encode()))
			request.Header.Set("X-Request-ID", "operation-test")
			request.AddCookie(cookie)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", tc.origin)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			response := backofficeBoundaryDo(t, app, request)
			responseBody := backofficeBoundaryBody(t, response)
			if response.StatusCode != tc.want {
				t.Fatalf("%s %s status=%d want=%d body=%s", tc.name, path, response.StatusCode, tc.want, responseBody)
			}
			if tc.want != 204 && calls != before {
				t.Fatal("rejected command reached upstream")
			}
		}
	}
}
