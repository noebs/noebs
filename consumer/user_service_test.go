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
		{"GetCardsByUserID", func(tenantID string) error {
			_, _, err := service.GetCardsByUserID(ctx, tenantID, 1)
			return err
		}},
		{"AddDeviceToken", func(tenantID string) error {
			return service.AddDeviceToken(ctx, tenantID, "0990000000", "device-token")
		}},
		{"ListBeneficiariesForUserID", func(tenantID string) error {
			_, err := service.ListBeneficiariesForUserID(ctx, tenantID, 1)
			return err
		}},
		{"UpsertBeneficiaryForUserID", func(tenantID string) error {
			return service.UpsertBeneficiaryForUserID(ctx, tenantID, 1, ebs_fields.Beneficiary{Data: "meter", BillType: "electricity"})
		}},
		{"DeleteBeneficiaryForUserID", func(tenantID string) error {
			return service.DeleteBeneficiaryForUserID(ctx, tenantID, 1, "meter")
		}},
		{"AddCardsForUserID", func(tenantID string) error {
			return service.AddCardsForUserID(ctx, tenantID, 1, "0990000000", []ebs_fields.Card{{Pan: "9222081700000000"}})
		}},
		{"EditCardForUserID", func(tenantID string) error {
			return service.EditCardForUserID(ctx, tenantID, 1, ebs_fields.Card{CardIdx: "9222081700000000"})
		}},
		{"RemoveCardForUserID", func(tenantID string) error {
			return service.RemoveCardForUserID(ctx, tenantID, 1, "9222081700000000")
		}},
		{"NecToName", func(tenantID string) error {
			_, err := service.NecToName(ctx, tenantID, "nec")
			return err
		}},
		{"Notifications", func(tenantID string) error {
			_, err := service.Notifications(ctx, tenantID, "0990000000")
			return err
		}},
		{"GetUserProfile", func(tenantID string) error {
			_, err := service.GetUserProfile(ctx, tenantID, "0990000000")
			return err
		}},
		{"UpdateUserProfile", func(tenantID string) error {
			return service.UpdateUserProfile(ctx, tenantID, "0990000000", ebs_fields.UserProfile{Fullname: "User"})
		}},
		{"GetUserLanguage", func(tenantID string) error {
			_, err := service.GetUserLanguage(ctx, tenantID, "0990000000")
			return err
		}},
		{"SetUserLanguage", func(tenantID string) error {
			return service.SetUserLanguage(ctx, tenantID, "0990000000", "en")
		}},
		{"UpdateKYC", func(tenantID string) error {
			return service.UpdateKYC(ctx, tenantID, ebs_fields.KYCPassport{Passport: ebs_fields.Passport{Mobile: "0990000000"}})
		}},
		{"GetTransactionByUUID", func(tenantID string) error {
			_, err := service.GetTransactionByUUID(ctx, tenantID, "uuid")
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

func TestUserServiceCardWritesRequirePAN(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	ctx := context.Background()
	if err := service.AddCardsForUserID(ctx, "tenant", 1, "0990000000", []ebs_fields.Card{{Pan: " "}}); !errors.Is(err, store.ErrMissingPAN) {
		t.Fatalf("AddCardsForUserID(missing pan) error = %v, want %v", err, store.ErrMissingPAN)
	}
	if err := service.EditCardForUserID(ctx, "tenant", 1, ebs_fields.Card{CardIdx: "9222081700000000", Pan: " "}); !errors.Is(err, store.ErrMissingPAN) {
		t.Fatalf("EditCardForUserID(missing pan) error = %v, want %v", err, store.ErrMissingPAN)
	}
}

func TestAddDeviceTokenRequiresExplicitInputs(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	ctx := context.Background()

	if err := service.AddDeviceToken(ctx, "tenant", " ", "device-token"); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("AddDeviceToken(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
	if err := service.AddDeviceToken(ctx, "tenant", "0990000000", " "); !errors.Is(err, store.ErrMissingToken) {
		t.Fatalf("AddDeviceToken(missing token) error = %v, want %v", err, store.ErrMissingToken)
	}
}

func TestUserServiceIdentityInputsFailBeforeStore(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	ctx := context.Background()

	if _, err := service.GetUserLanguage(ctx, "tenant", " "); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("GetUserLanguage(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
	if err := service.SetUserLanguage(ctx, "tenant", " ", "en"); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("SetUserLanguage(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
	if err := service.SetUserLanguage(ctx, "tenant", "0990000000", " "); !errors.Is(err, store.ErrMissingLanguage) {
		t.Fatalf("SetUserLanguage(missing language) error = %v, want %v", err, store.ErrMissingLanguage)
	}
	if err := service.UpdateUserProfile(ctx, "tenant", " ", ebs_fields.UserProfile{Fullname: "User"}); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("UpdateUserProfile(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
	if err := service.UpdateUserProfile(ctx, "tenant", "0990000000", ebs_fields.UserProfile{}); !errors.Is(err, store.ErrMissingData) {
		t.Fatalf("UpdateUserProfile(empty profile) error = %v, want %v", err, store.ErrMissingData)
	}
	if err := service.UpdateUserProfile(ctx, "tenant", "0990000000", ebs_fields.UserProfile{Fullname: " "}); !errors.Is(err, store.ErrMissingData) {
		t.Fatalf("UpdateUserProfile(blank profile) error = %v, want %v", err, store.ErrMissingData)
	}
	if err := service.UpdateUserProfile(ctx, "tenant", "0990000000", ebs_fields.UserProfile{Username: " "}); !errors.Is(err, store.ErrMissingUsername) {
		t.Fatalf("UpdateUserProfile(missing username) error = %v, want %v", err, store.ErrMissingUsername)
	}
	if err := service.UpdateUserProfile(ctx, "tenant", "0990000000", ebs_fields.UserProfile{Email: " "}); !errors.Is(err, store.ErrMissingEmail) {
		t.Fatalf("UpdateUserProfile(missing email) error = %v, want %v", err, store.ErrMissingEmail)
	}
}

func TestUpdateKYCRequiresExistingUser(t *testing.T) {
	ctx := context.Background()
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	service := &Service{Store: storeSvc}

	err := service.UpdateKYC(ctx, tenantID, ebs_fields.KYCPassport{
		Selfie:      "selfie",
		PassportImg: "passport-image",
		Passport:    ebs_fields.Passport{Mobile: "0990000000", PassportNumber: "P123"},
	})
	if !store.ErrNotFound(err) {
		t.Fatalf("error = %v, want not found", err)
	}
}

func TestUpdateKYCPersistsForExistingUser(t *testing.T) {
	ctx := context.Background()
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	service := &Service{Store: storeSvc}
	mobile := "0990000000"
	seedUser(t, storeSvc, tenantID, mobile, "password")

	if err := service.UpdateKYC(ctx, tenantID, ebs_fields.KYCPassport{
		Selfie:      "selfie",
		PassportImg: "passport-image",
		Passport:    ebs_fields.Passport{Mobile: " " + mobile + " ", PassportNumber: "P123"},
	}); err != nil {
		t.Fatalf("UpdateKYC(): %v", err)
	}

	_, kyc, passport, err := storeSvc.GetUserWithKYC(ctx, tenantID, mobile)
	if err != nil {
		t.Fatalf("GetUserWithKYC(): %v", err)
	}
	if kyc == nil {
		t.Fatal("kyc is nil, want persisted record")
	}
	if kyc.UserMobile != mobile || kyc.Mobile != mobile {
		t.Fatalf("kyc mobile = (%q, %q), want %q", kyc.UserMobile, kyc.Mobile, mobile)
	}
	if passport == nil {
		t.Fatal("passport is nil, want persisted record")
	}
	if passport.Mobile != mobile {
		t.Fatalf("passport mobile = %q, want %q", passport.Mobile, mobile)
	}
	if passport.PassportNumber != "P123" {
		t.Fatalf("passport number = %q, want P123", passport.PassportNumber)
	}
}
