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
