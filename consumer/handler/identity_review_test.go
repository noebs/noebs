package handler

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

func reviewRequest(method, path, role, permission string, values url.Values) *http.Request {
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set(gateway.GatewayTenantIDHeader, "tenant-a")
	request.Header.Set(gateway.GatewayIssuerHeader, "https://identity.example/realms/noebs")
	request.Header.Set(gateway.GatewaySubjectHeader, "operator-a")
	request.Header.Set(gateway.GatewayOrganizationIDHeader, "organization-a")
	request.Header.Set(gateway.GatewayAuthorizedPartyHeader, "noebs-backoffice")
	request.Header.Set(gateway.GatewayRolesHeader, role)
	request.Header.Set(gateway.GatewayPermissionHeader, permission)
	request.Header.Set(gateway.GatewaySourceIPHeader, "192.0.2.1")
	request.Header.Set(gateway.GatewayTokenExpiresAtHeader, strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	request.Header.Set(backofficeauth.HeaderCSRFToken, base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationForm)
	return request
}

func TestIncompleteEvidenceReviewExplainsRequiredAction(t *testing.T) {
	app := fiber.New()
	app.Post("/decision", func(c *fiber.Ctx) error { return identityReviewError(c, store.ErrIdentityReviewIncomplete) })
	request := httptest.NewRequest(http.MethodPost, "/decision", nil)
	request.Header.Set("HX-Request", "true")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnprocessableEntity || response.Header.Get("HX-Retarget") != "#review-feedback" || !strings.Contains(string(body), "review every evidence image") {
		t.Fatalf("status=%d target=%q body=%s", response.StatusCode, response.Header.Get("HX-Retarget"), body)
	}
}

func TestIdentityReviewRejectsUnauthorizedAndInvalidCommandsBeforeService(t *testing.T) {
	app := fiber.New()
	RegisterIdentityReviewRoutes(app.Group("/admin/identity", gateway.InternalPrincipalIdentityMiddleware()), &Handler{})
	decisionPath := "/admin/identity/42/" + identityTestID + "/decision"
	good := url.Values{"operation_id": {identityTestID}, "revision": {"3"}, "decision": {"approved"}, "reason": {"Evidence reviewed"}, "policy_reference": {"review-v1"}, "evidence_reviewed": {"true"}}
	tests := []struct {
		name, method, path, role, permission string
		form                                 url.Values
		status                               int
	}{
		{"read permission cannot decide", http.MethodPost, decisionPath, "tenant-admin", "identity:review:read", good, 403},
		{"backoffice cannot decide", http.MethodPost, decisionPath, "backoffice", "identity:review:decide", good, 403},
		{"user cannot read", http.MethodGet, "/admin/identity", "user", "identity:review:read", nil, 403},
		{"missing tenant cannot be defaulted", http.MethodGet, "/admin/identity?tenant_id=tenant-b", "tenant-admin", "identity:review:read", nil, 400},
		{"invalid page", http.MethodGet, "/admin/identity?limit=0", "tenant-admin", "identity:review:read", nil, 400},
		{"invalid target", http.MethodGet, "/admin/identity/0/" + identityTestID, "tenant-admin", "identity:review:read", nil, 422},
		{"invalid revision", http.MethodPost, decisionPath, "tenant-admin", "identity:review:decide", url.Values{"operation_id": {identityTestID}, "revision": {"0"}, "decision": {"approved"}, "reason": {"reason"}, "policy_reference": {"v1"}, "evidence_reviewed": {"true"}}, 422},
		{"duplicate operation", http.MethodPost, decisionPath, "tenant-admin", "identity:review:decide", url.Values{"operation_id": {identityTestID, identityTestID}}, 422},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response, err := app.Test(reviewRequest(tc.method, tc.path, tc.role, tc.permission, tc.form))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d", response.StatusCode, tc.status)
			}
		})
	}
	request := reviewRequest(http.MethodGet, "/admin/identity", "tenant-admin", string(tenantauth.PermissionIdentityReviewRead), nil)
	request.Header.Del(backofficeauth.HeaderCSRFToken)
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing signed CSRF context status=%d", response.StatusCode)
	}
	request = reviewRequest(http.MethodPost, decisionPath, "tenant-admin", string(tenantauth.PermissionIdentityReviewDecide), url.Values{})
	request.Header.Set("HX-Request", "true")
	response, err = app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 422 || response.Header.Get("HX-Retarget") != "#review-feedback" {
		t.Fatalf("invalid HTMX form: status=%d target=%s", response.StatusCode, response.Header.Get("HX-Retarget"))
	}
}
