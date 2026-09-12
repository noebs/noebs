package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/gofiber/fiber/v2"
)

func TestAccountDisplayNameRequiresVerifiedUserAndStrictTarget(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		user       bool
		status     int
	}{
		{"no principal", `{"user_id":2}`, false, http.StatusUnauthorized},
		{"missing target", `{}`, true, http.StatusBadRequest},
		{"negative target", `{"user_id":-1}`, true, http.StatusBadRequest},
		{"caller selected tenant", `{"user_id":2,"tenant_id":"other"}`, true, http.StatusBadRequest},
		{"extra profile fields", `{"user_id":2,"email":"private@example.test"}`, true, http.StatusBadRequest},
		{"trailing data", `{"user_id":2}{}`, true, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			if tc.user {
				app.Use(func(c *fiber.Ctx) error {
					for k, v := range map[string]string{
						gateway.GatewayTenantIDHeader: "tenant", gateway.GatewayIssuerHeader: "https://identity.example",
						gateway.GatewaySubjectHeader: "actor", gateway.GatewayOrganizationIDHeader: "organization",
						gateway.GatewayAuthorizedPartyHeader: "noebs-mobile", gateway.GatewayRolesHeader: "user",
						gateway.GatewayUserIDHeader: "1", gateway.GatewaySourceIPHeader: "203.0.113.8",
						gateway.GatewayTokenExpiresAtHeader: strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10),
					} {
						c.Request().Header.Set(k, v)
					}
					return c.Next()
				}, gateway.InternalPrincipalIdentityMiddleware())
			}
			RegisterIdentityInternalRoutes(app.Group("/internal/identity-auth"), &Handler{})
			response, err := app.Test(httptest.NewRequest(http.MethodPost, "/internal/identity-auth/accounts/display-name", bytes.NewBufferString(tc.body)))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.status)
			}
		})
	}
}
