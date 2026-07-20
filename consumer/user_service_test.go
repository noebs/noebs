package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestUserServiceTenantValidationFailsBeforeDB(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"AddDeviceToken", func(tenantID string) error {
			return service.AddDeviceToken(ctx, tenantID, 1, "device-token")
		}},
		{"GetUserProfile", func(tenantID string) error {
			_, err := service.GetUserProfile(ctx, tenantID, 1)
			return err
		}},
		{"UpdateUserProfile", func(tenantID string) error {
			return service.UpdateUserProfile(ctx, tenantID, 1, ebs_fields.UserProfile{Fullname: "User"})
		}},
		{"GetUserLanguage", func(tenantID string) error {
			_, err := service.GetUserLanguage(ctx, tenantID, 1)
			return err
		}},
		{"SetUserLanguage", func(tenantID string) error {
			return service.SetUserLanguage(ctx, tenantID, 1, "en")
		}},
		{"UpdateKYC", func(tenantID string) error {
			return service.UpdateKYC(ctx, tenantID, 1, ebs_fields.KYCPassport{})
		}},
		{"GetTransactionByUUIDForUser", func(tenantID string) error {
			_, err := service.GetTransactionByUUIDForUser(ctx, tenantID, 1, "uuid")
			return err
		}},
	}
	for _, test := range cases {
		for _, tenant := range []struct {
			id   string
			want error
		}{{"", store.ErrMissingTenantID}, {"default", store.ErrInvalidTenantID}} {
			t.Run(test.name+"/"+tenant.id, func(t *testing.T) {
				if err := test.run(tenant.id); !errors.Is(err, tenant.want) {
					t.Fatalf("error = %v, want %v", err, tenant.want)
				}
			})
		}
	}
}

func TestUserServiceRequiresExactProfileInputs(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	ctx := context.Background()
	if err := service.AddDeviceToken(ctx, "tenant", 0, "device-token"); !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("invalid device-token user error = %v, want %v", err, store.ErrInvalidUserID)
	}
	if err := service.AddDeviceToken(ctx, "tenant", 1, " "); !errors.Is(err, store.ErrMissingDeviceToken) {
		t.Fatalf("blank device token error = %v, want %v", err, store.ErrMissingDeviceToken)
	}
	if err := service.SetUserLanguage(ctx, "tenant", 1, " "); !errors.Is(err, store.ErrMissingLanguage) {
		t.Fatalf("blank language error = %v, want %v", err, store.ErrMissingLanguage)
	}
	if err := service.UpdateUserProfile(ctx, "tenant", 1, ebs_fields.UserProfile{}); !errors.Is(err, store.ErrMissingData) {
		t.Fatalf("empty profile error = %v, want %v", err, store.ErrMissingData)
	}
	if err := service.UpdateUserProfile(ctx, "tenant", 1, ebs_fields.UserProfile{Fullname: " User "}); !errors.Is(err, store.ErrInvalidProfileName) {
		t.Fatalf("non-exact fullname error = %v, want %v", err, store.ErrInvalidProfileName)
	}
}

func TestUserServiceProfileAndKYCUseNumericProjection(t *testing.T) {
	ctx := context.Background()
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	service := &Service{Store: storeSvc}
	profile := seedProfile(t, storeSvc, tenantID, "0990000000")

	if err := service.UpdateUserProfile(ctx, tenantID, profile.UserID, ebs_fields.UserProfile{
		Fullname: "Updated Owner",
		Email:    "updated@example.test",
	}); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if err := service.SetUserLanguage(ctx, tenantID, profile.UserID, "en"); err != nil {
		t.Fatalf("set language: %v", err)
	}
	if err := service.AddDeviceToken(ctx, tenantID, profile.UserID, "device-token"); err != nil {
		t.Fatalf("set device token: %v", err)
	}
	if err := service.UpdateKYC(ctx, tenantID, profile.UserID, ebs_fields.KYCPassport{
		Selfie:      "selfie",
		PassportImg: "passport-image",
		Passport:    ebs_fields.Passport{PassportNumber: "P123"},
	}); err != nil {
		t.Fatalf("update KYC: %v", err)
	}

	stored, err := storeSvc.FindProfileProjectionByUserID(ctx, tenantID, profile.UserID)
	if err != nil {
		t.Fatalf("find projection: %v", err)
	}
	if stored.Fullname != "Updated Owner" || stored.Email != "updated@example.test" || stored.Language != "en" || stored.DeviceToken != "device-token" {
		t.Fatalf("stored projection = %+v", stored)
	}
	kyc, passport, err := storeSvc.GetProfileKYC(ctx, tenantID, profile.UserID)
	if err != nil {
		t.Fatalf("get KYC: %v", err)
	}
	if kyc == nil || kyc.UserID != profile.UserID || passport == nil || passport.UserID != profile.UserID || passport.PassportNumber != "P123" {
		t.Fatalf("KYC/passport = %+v / %+v", kyc, passport)
	}
}
