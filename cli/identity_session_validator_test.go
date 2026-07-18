package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
)

func TestIdentitySessionValidatorFailsClosedAndPropagatesIdentity(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr error
	}{
		{name: "current", status: http.StatusNoContent},
		{name: "revoked", status: http.StatusUnauthorized, wantErr: gateway.ErrSessionRevoked},
		{name: "unavailable", status: http.StatusInternalServerError, wantErr: gateway.ErrSessionValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/internal/identity-auth/sessions/validate" {
					t.Fatalf("path = %s", r.URL.Path)
				}
				if r.Header.Get(gateway.GatewayTenantIDHeader) != "tenant_1" ||
					r.Header.Get(gateway.GatewayAdminIdentityHeader) != gateway.GatewayAdminIdentityValue ||
					r.Header.Get(gateway.GatewayAdminRoleHeader) != gateway.GatewayAdminRoleValue {
					t.Fatalf("identity headers = %v", r.Header)
				}
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(server.Close)

			validator, err := newIdentitySessionValidator(ebs_fields.NoebsConfig{
				ServiceDiscovery: map[string]string{string(serviceRoleIdentityAuth): server.URL},
			})
			if err != nil {
				t.Fatalf("newIdentitySessionValidator(): %v", err)
			}
			err = validator.ValidateSession(context.Background(), "tenant_1", 42, 2)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateSession() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
