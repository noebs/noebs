package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestEBSAdapterTenantValidationFailsBeforeDBOrHTTP(t *testing.T) {
	service := &Service{
		Store:      &store.Store{},
		HTTPClient: testHTTPClient(),
	}
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"BillPayment", func(tenantID string) error {
			_, err := service.BillPayment(ctx, tenantID, ebs_fields.ConsumerBillPaymentFields{})
			return err
		}},
		{"GetBills", func(tenantID string) error {
			_, _, err := service.GetBills(ctx, tenantID, Bills{Phone: "0990000000", PayeeID: "0010010001"})
			return err
		}},
		{"GetBiller", func(tenantID string) error {
			_, err := service.GetBiller(ctx, tenantID, "0990000000")
			return err
		}},
		{"CardTransfer", func(tenantID string) error {
			_, err := service.CardTransfer(ctx, tenantID, ebs_fields.ConsumerCardTransferAndMobileFields{})
			return err
		}},
		{"MobileTransfer", func(tenantID string) error {
			_, err := service.MobileTransfer(ctx, tenantID, ebs_fields.ConsumerMobileTransferFields{Mobile: "0990000000"})
			return err
		}},
		{"GenerateVoucher", func(tenantID string) error {
			_, err := service.GenerateVoucher(ctx, tenantID, ebs_fields.ConsumerGenerateVoucherFields{})
			return err
		}},
		{"GenerateIpin", func(tenantID string) error {
			_, err := service.GenerateIpin(ctx, tenantID, ebs_fields.ConsumerGenerateIPin{})
			return err
		}},
		{"CompleteIpin", func(tenantID string) error {
			_, err := service.CompleteIpin(ctx, tenantID, ebs_fields.ConsumerGenerateIPinCompletion{})
			return err
		}},
		{"IPINKey", func(tenantID string) error {
			_, err := service.IPINKey(ctx, tenantID, ebs_fields.ConsumerGenerateIPINFields{})
			return err
		}},
		{"BalanceStep", func(tenantID string) error {
			_, err := service.BalanceStep(ctx, tenantID, BalanceStepRequest{Mobile: "0990000000", ConsumerBalanceFields: ebs_fields.ConsumerBalanceFields{}})
			return err
		}},
		{"isValidCard", func(tenantID string) error {
			_, err := service.isValidCard(ctx, tenantID, ebs_fields.CacheCards{Pan: "9222081700000000", Expiry: "2601"})
			return err
		}},
		{"GetIpinPubKey", func(tenantID string) error {
			return service.GetIpinPubKey(ctx, tenantID)
		}},
		{"RegisterWithCard", func(tenantID string) error {
			return service.RegisterWithCard(ctx, tenantID, ebs_fields.CacheCards{Mobile: "0990000000", PublicKey: "public-key", Pan: "9222081700000000", Expiry: "2601", Password: "password"})
		}},
		{"CompleteRegistration", func(tenantID string) error {
			_, err := service.CompleteRegistration(ctx, tenantID, ebs_fields.ConsumerCompleteRegistrationFields{Mobile: "0990000000", NoebsPassword: "password"})
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
