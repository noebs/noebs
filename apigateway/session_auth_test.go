package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestAuthMiddlewareFailsClosedOnSessionValidation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		validation error
		wantStatus int
		wantCode   string
	}{
		{name: "revoked", validation: ErrSessionRevoked, wantStatus: http.StatusUnauthorized, wantCode: "session_revoked"},
		{name: "unavailable", validation: errors.New("identity auth unavailable"), wantStatus: http.StatusServiceUnavailable, wantCode: "session_validation_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &JWTAuth{
				Key: []byte("test-key"),
				Sessions: SessionValidatorFunc(func(_ context.Context, tenantID string, userID, epoch int64) error {
					if tenantID != "tenant" || userID != 42 || epoch != 7 {
						t.Fatalf("session identity = %q/%d/%d", tenantID, userID, epoch)
					}
					return tc.validation
				}),
			}
			token, err := auth.GenerateJWTWithSessionEpoch(42, "0990000000", "tenant", 7)
			if err != nil {
				t.Fatalf("generate JWT: %v", err)
			}
			app := fiber.New()
			app.Get("/", auth.AuthMiddleware(), func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test(): %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", body.Code, tc.wantCode)
			}
		})
	}
}
