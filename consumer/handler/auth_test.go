package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/utils"
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
