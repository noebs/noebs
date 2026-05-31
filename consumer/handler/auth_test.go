package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/utils"
	"github.com/gofiber/fiber/v2"
)

func TestGenerateSignInCodeErrorResponse(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing store",
			err:        consumer.ErrMissingStore,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "service_unavailable",
		},
		{
			name:       "not found",
			err:        sql.ErrNoRows,
			wantStatus: http.StatusBadRequest,
			wantCode:   "not_found",
		},
		{
			name:       "sms delivery",
			err:        fmt.Errorf("%w: gateway returned 502 Bad Gateway", utils.ErrSMSDeliveryFailed),
			wantStatus: http.StatusBadGateway,
			wantCode:   "sms_delivery_failed",
		},
		{
			name:       "unexpected",
			err:        errors.New("database failed"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "service_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := generateSignInCodeErrorResponse(tt.err)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}
			if body["code"] != tt.wantCode {
				t.Fatalf("code = %v, want %s", body["code"], tt.wantCode)
			}
		})
	}
}

func TestAuthRecoveryHandlersRejectMalformedJSONBeforeService(t *testing.T) {
	handler := &Handler{}
	app := fiber.New()
	app.Post("/balance-step", handler.BalanceStep)
	app.Post("/signin-code", handler.GenerateSignInCode)

	for _, path := range []string{"/balance-step", "/signin-code"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{"))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}
