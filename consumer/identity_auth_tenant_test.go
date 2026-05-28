package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestIdentityAuthTenantValidationFailsBeforeDBOrHTTP(t *testing.T) {
	service := &Service{
		Store:      &store.Store{},
		HTTPClient: testHTTPClient(),
		Auth:       &refreshAuthStub{},
		NoebsConfig: ebs_fields.NoebsConfig{
			GoogleClientID: "google-client",
		},
	}
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"GoogleAuth", func(tenantID string) error {
			_, _, _, err := service.GoogleAuth(ctx, tenantID, "google-code", "", "")
			return err
		}},
		{"CompleteProfile", func(tenantID string) error {
			_, _, err := service.CompleteProfile(ctx, tenantID, 1, "0990000000", "User")
			return err
		}},
		{"AuthMe", func(tenantID string) error {
			_, err := service.AuthMe(ctx, tenantID, 1)
			return err
		}},
		{"findOrCreateUserFromGoogle", func(tenantID string) error {
			_, _, err := service.findOrCreateUserFromGoogle(ctx, tenantID, googleUserInfo{Sub: "google-user", Email: "user@example.test"})
			return err
		}},
		{"IssueRecoveryJWT", func(tenantID string) error {
			_, err := service.IssueRecoveryJWT(ctx, tenantID, RecoveryJWTCommand{UserID: 1, Mobile: "0990000000"})
			return err
		}},
		{"IssueRecoveryJWTInIdentityAuth", func(tenantID string) error {
			_, err := service.IssueRecoveryJWTInIdentityAuth(ctx, tenantID, RecoveryJWTCommand{UserID: 1, Mobile: "0990000000"})
			return err
		}},
		{"ResolveIdentityUserByMobile", func(tenantID string) error {
			_, err := service.ResolveIdentityUserByMobile(ctx, tenantID, IdentityUserByMobileCommand{Mobile: "0990000000"})
			return err
		}},
		{"ResolveIdentityUserByMobileInIdentityAuth", func(tenantID string) error {
			_, err := service.ResolveIdentityUserByMobileInIdentityAuth(ctx, tenantID, "0990000000")
			return err
		}},
	}
	tenantCases := []struct {
		tenantID string
		wantErr  error
	}{
		{"", store.ErrMissingTenantID},
		{"default", store.ErrInvalidTenantID},
	}
	for _, tc := range cases {
		for _, tenantCase := range tenantCases {
			t.Run(tc.name+"/"+tenantCase.tenantID, func(t *testing.T) {
				err := tc.run(tenantCase.tenantID)
				if !errors.Is(err, tenantCase.wantErr) {
					t.Fatalf("expected %v, got %v", tenantCase.wantErr, err)
				}
			})
		}
	}
}
