package consumer

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestCardTokenTenantValidationFailsBeforeDBOrHTTP(t *testing.T) {
	service := &Service{
		Store:      &store.Store{},
		HTTPClient: http.DefaultClient,
	}
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"ResolveCardByMobile", func(tenantID string) error {
			_, err := service.ResolveCardByMobile(ctx, tenantID, CardByMobileCommand{Mobile: "0990000000"})
			return err
		}},
		{"ResolveCardByMobilePAN", func(tenantID string) error {
			_, err := service.ResolveCardByMobilePAN(ctx, tenantID, CardByMobilePANCommand{Mobile: "0990000000", PAN: "9222081700000000"})
			return err
		}},
		{"ListMaskedCardsForUserID", func(tenantID string) error {
			_, err := service.ListMaskedCardsForUserID(ctx, tenantID, 1, MaskedCardsCommand{})
			return err
		}},
		{"ResolveMaskedCardByMobile", func(tenantID string) error {
			_, err := service.ResolveMaskedCardByMobile(ctx, tenantID, MaskedCardByMobileCommand{Mobile: "0990000000"})
			return err
		}},
		{"GeneratePaymentTokenForUserID", func(tenantID string) error {
			_, _, _, err := service.GeneratePaymentTokenForUserID(ctx, tenantID, 1, ebs_fields.Token{Amount: 100})
			return err
		}},
		{"GetPaymentTokenForUserID", func(tenantID string) error {
			_, _, err := service.GetPaymentTokenForUserID(ctx, tenantID, 1, "token-uuid")
			return err
		}},
		{"NoebsQuickPayment", func(tenantID string) error {
			_, err := service.NoebsQuickPayment(ctx, tenantID, 1, ebs_fields.QuickPaymentFields{}, "token-uuid", "")
			return err
		}},
		{"ResolveQuickPaymentTokenForUserID", func(tenantID string) error {
			_, err := service.ResolveQuickPaymentTokenForUserID(ctx, tenantID, 1, QuickPaymentTokenResolveCommand{UUID: "token-uuid"})
			return err
		}},
		{"FinalizeQuickPaymentTokenForUserID", func(tenantID string) error {
			return service.FinalizeQuickPaymentTokenForUserID(ctx, tenantID, 1, QuickPaymentTokenFinalizationCommand{UUID: "token-uuid", RailUUID: "rail-uuid", Status: ebs_fields.PaymentTokenStatusPaid})
		}},
		{"ListMaskedCardsInCardVault", func(tenantID string) error {
			_, err := service.ListMaskedCardsInCardVault(ctx, tenantID, 1)
			return err
		}},
		{"ResolveMaskedCardByMobileInCardVault", func(tenantID string) error {
			_, err := service.ResolveMaskedCardByMobileInCardVault(ctx, tenantID, "0990000000")
			return err
		}},
		{"ResolveCardByMobileInCardVault", func(tenantID string) error {
			_, err := service.ResolveCardByMobileInCardVault(ctx, tenantID, "0990000000")
			return err
		}},
		{"ResolveCardByMobilePANInCardVault", func(tenantID string) error {
			_, err := service.ResolveCardByMobilePANInCardVault(ctx, tenantID, "0990000000", "9222081700000000")
			return err
		}},
		{"ResolveQuickPaymentTokenFromCardVault", func(tenantID string) error {
			_, err := service.ResolveQuickPaymentTokenFromCardVault(ctx, tenantID, 1, QuickPaymentTokenResolveCommand{UUID: "token-uuid"})
			return err
		}},
		{"FinalizeQuickPaymentTokenInCardVault", func(tenantID string) error {
			return service.FinalizeQuickPaymentTokenInCardVault(ctx, tenantID, 1, QuickPaymentTokenFinalizationCommand{UUID: "token-uuid", RailUUID: "rail-uuid", Status: ebs_fields.PaymentTokenStatusPaid})
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

func TestGeneratePaymentTokenRejectsNegativeAmountBeforeStore(t *testing.T) {
	service := &Service{Store: &store.Store{}}

	_, _, _, err := service.GeneratePaymentTokenForUserID(context.Background(), "tenant-a", 1, ebs_fields.Token{Amount: -1})
	if !errors.Is(err, store.ErrInvalidAmount) {
		t.Fatalf("GeneratePaymentTokenForUserID() error = %v, want %v", err, store.ErrInvalidAmount)
	}
}

func TestGeneratePaymentTokenRejectsMalformedCardSelectorBeforeStore(t *testing.T) {
	service := Service{}
	_, _, _, err := service.generatePaymentTokenForCards(context.Background(), "tenant-a", 1, []ebs_fields.Card{
		{Pan: "9222081700000000"},
	}, ebs_fields.Token{ToCard: "****0000", Amount: 100})
	if !errors.Is(err, ebs_fields.ErrInvalidCardQuery) {
		t.Fatalf("generatePaymentTokenForCards() error = %v, want %v", err, ebs_fields.ErrInvalidCardQuery)
	}
}
