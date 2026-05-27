package consumer

import (
	"context"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestCardVaultOwnedOperationsUseOnlyCardVaultSchema(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{
		Store: storeSvc,
		NoebsConfig: ebs_fields.NoebsConfig{
			PaymentLinkBase: "https://pay.example/token/",
		},
	}
	ctx := context.Background()
	userID := int64(42)
	mobile := "0912141660"
	pan := "9222081700000000"

	if err := service.AddCardsForUserID(ctx, tenantID, userID, mobile, []ebs_fields.Card{{
		Pan:    pan,
		Expiry: "2912",
		Name:   "Primary",
		IPIN:   "1234",
	}}); err != nil {
		t.Fatalf("add card with card-vault schema: %v", err)
	}

	cards, main, err := service.GetCardsByUserID(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("get cards with card-vault schema: %v", err)
	}
	if len(cards) != 1 || main == nil || main.Pan != pan {
		t.Fatalf("cards = %+v main = %+v, want one card with pan %s", cards, main, pan)
	}
	byMobile, err := service.ResolveCardByMobile(ctx, tenantID, CardByMobileCommand{Mobile: mobile})
	if err != nil {
		t.Fatalf("resolve card by mobile with card-vault schema: %v", err)
	}
	if byMobile.PAN != pan || byMobile.ExpDate != "2912" {
		t.Fatalf("card by mobile = %+v, want pan=%s exp=2912", byMobile, pan)
	}
	masked, err := service.ListMaskedCardsForUserID(ctx, tenantID, userID, MaskedCardsCommand{})
	if err != nil {
		t.Fatalf("list masked cards with card-vault schema: %v", err)
	}
	if len(masked.MaskedPANs) != 1 || masked.MaskedPANs[0] != "922208*****0000" {
		t.Fatalf("masked cards = %+v", masked)
	}

	if err := service.SetMainCardForUserID(ctx, tenantID, userID, pan); err != nil {
		t.Fatalf("set main card with card-vault schema: %v", err)
	}
	if err := service.EditCardForUserID(ctx, tenantID, userID, ebs_fields.Card{
		CardIdx: pan,
		Pan:     pan,
		Expiry:  "3012",
		Name:    "Updated",
		IPIN:    "5678",
	}); err != nil {
		t.Fatalf("edit card with card-vault schema: %v", err)
	}

	created, encoded, paymentLink, err := service.GeneratePaymentTokenForUserID(ctx, tenantID, userID, ebs_fields.Token{Amount: 25})
	if err != nil {
		t.Fatalf("generate payment token with card-vault schema: %v", err)
	}
	if created.UUID == "" || encoded == "" {
		t.Fatalf("token UUID/encoded must be set: created=%+v encoded=%q", created, encoded)
	}
	if paymentLink != "https://pay.example/token/"+created.UUID {
		t.Fatalf("payment link = %q, want base plus token UUID", paymentLink)
	}
	if created.ToCard == pan {
		t.Fatalf("created token must not return raw PAN")
	}

	resolution, err := service.ResolveQuickPaymentTokenForUserID(ctx, tenantID, userID, QuickPaymentTokenResolveCommand{
		UUID:   created.UUID,
		Amount: created.Amount,
	})
	if err != nil {
		t.Fatalf("resolve quick-pay token with card-vault schema: %v", err)
	}
	if resolution.UUID != created.UUID || resolution.Amount != created.Amount || resolution.ToCard != pan {
		t.Fatalf("quick-pay resolution = %+v, want uuid=%s amount=%d raw PAN", resolution, created.UUID, created.Amount)
	}
	if err := service.MarkQuickPaymentTokenPaidForUserID(ctx, tenantID, userID, QuickPaymentTokenPaidCommand{UUID: created.UUID}); err != nil {
		t.Fatalf("mark quick-pay token paid with card-vault schema: %v", err)
	}
	paidToken, err := storeSvc.GetTokenByUUID(ctx, tenantID, created.UUID)
	if err != nil {
		t.Fatalf("get paid token with card-vault schema: %v", err)
	}
	if !paidToken.IsPaid {
		t.Fatalf("quick-pay token was not marked paid")
	}

	tokens, token, err := service.GetPaymentTokenForUserID(ctx, tenantID, userID, "")
	if err != nil {
		t.Fatalf("list payment tokens with card-vault schema: %v", err)
	}
	if token != nil || len(tokens) != 1 || tokens[0].ToCard == pan {
		t.Fatalf("tokens = %+v token = %+v, want one masked list result", tokens, token)
	}

	tokens, token, err = service.GetPaymentTokenForUserID(ctx, tenantID, userID, created.UUID)
	if err != nil {
		t.Fatalf("get payment token with card-vault schema: %v", err)
	}
	if len(tokens) != 0 || token == nil || token.UUID != created.UUID || token.ToCard == pan {
		t.Fatalf("tokens = %+v token = %+v, want one masked token result", tokens, token)
	}

	requested, encodedRequest, paymentRequestLink, err := service.PaymentRequestForUserID(ctx, tenantID, userID, PaymentRequestData{Amount: 75})
	if err != nil {
		t.Fatalf("create payment request with card-vault schema: %v", err)
	}
	if requested.UUID == "" || encodedRequest == "" {
		t.Fatalf("payment request UUID/encoded must be set: requested=%+v encoded=%q", requested, encodedRequest)
	}
	if paymentRequestLink != "https://pay.example/token/"+requested.UUID {
		t.Fatalf("payment request link = %q, want base plus token UUID", paymentRequestLink)
	}
	if requested.ToCard == pan {
		t.Fatalf("payment request must not return raw PAN")
	}

	if err := service.RemoveCardForUserID(ctx, tenantID, userID, pan); err != nil {
		t.Fatalf("remove card with card-vault schema: %v", err)
	}
	if _, _, err := service.GetCardsByUserID(ctx, tenantID, userID); err == nil {
		t.Fatalf("expected no cards after removal")
	}
}
