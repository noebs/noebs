package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
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
	if byMobile.UserID != userID || byMobile.PAN != pan || byMobile.ExpDate != "2912" {
		t.Fatalf("card by mobile = %+v, want pan=%s exp=2912", byMobile, pan)
	}
	masked, err := service.ListMaskedCardsForUserID(ctx, tenantID, userID, MaskedCardsCommand{})
	if err != nil {
		t.Fatalf("list masked cards with card-vault schema: %v", err)
	}
	if len(masked.MaskedPANs) != 1 || masked.MaskedPANs[0] != "922208*****0000" {
		t.Fatalf("masked cards = %+v", masked)
	}
	maskedByMobile, err := service.ResolveMaskedCardByMobile(ctx, tenantID, MaskedCardByMobileCommand{Mobile: mobile})
	if err != nil {
		t.Fatalf("resolve masked card by mobile with card-vault schema: %v", err)
	}
	if maskedByMobile.MaskedPAN != "922208*****0000" {
		t.Fatalf("masked card by mobile = %+v", maskedByMobile)
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
	if resolution.UUID != created.UUID || resolution.RailUUID != created.RailUUID || resolution.Amount != created.Amount || resolution.ToCard != pan || resolution.RecipientUserID != userID {
		t.Fatalf("quick-pay resolution = %+v, want uuid=%s amount=%d raw PAN", resolution, created.UUID, created.Amount)
	}
	if err := service.FinalizeQuickPaymentTokenForUserID(ctx, tenantID, userID, QuickPaymentTokenFinalizationCommand{UUID: created.UUID, RailUUID: created.RailUUID, Status: ebs_fields.PaymentTokenStatusPaid}); err != nil {
		t.Fatalf("finalize quick-pay token with card-vault schema: %v", err)
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

	openRequest, _, _, err := service.GeneratePaymentTokenForUserID(ctx, tenantID, userID, ebs_fields.Token{})
	if err != nil {
		t.Fatalf("create open amount payment token with card-vault schema: %v", err)
	}
	openResolution, err := service.ResolveQuickPaymentTokenForUserID(ctx, tenantID, userID, QuickPaymentTokenResolveCommand{
		UUID:   openRequest.UUID,
		Amount: 125,
	})
	if err != nil {
		t.Fatalf("resolve open amount quick-pay token with card-vault schema: %v", err)
	}
	if openResolution.Amount != 125 || openResolution.ToCard != pan {
		t.Fatalf("open amount resolution = %+v, want amount=125 raw PAN", openResolution)
	}
	if _, err := service.ResolveQuickPaymentTokenForUserID(ctx, tenantID, userID, QuickPaymentTokenResolveCommand{UUID: openRequest.UUID}); !errors.Is(err, store.ErrInvalidAmount) {
		t.Fatalf("resolve open amount quick-pay token without amount error = %v, want %v", err, store.ErrInvalidAmount)
	}

	if err := service.RemoveCardForUserID(ctx, tenantID, userID, pan); err != nil {
		t.Fatalf("remove card with card-vault schema: %v", err)
	}
	cards, main, err = service.GetCardsByUserID(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("empty card list with card-vault schema: %v", err)
	}
	if cards == nil || len(cards) != 0 || main != nil {
		t.Fatalf("empty cards = %#v main = %#v, want non-nil empty cards and nil main", cards, main)
	}
}

func TestSetMainCardForUserIDRejectsMissingPAN(t *testing.T) {
	service := &Service{Store: store.New(&store.DB{})}

	err := service.SetMainCardForUserID(context.Background(), "tenant-1", 42, " ")
	if !errors.Is(err, store.ErrMissingPAN) {
		t.Fatalf("expected ErrMissingPAN, got %v", err)
	}
}

func TestSetMainCardForUserIDRejectsUnknownCard(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{Store: storeSvc}

	err := service.SetMainCardForUserID(context.Background(), tenantID, 42, "9222081700000000")
	if !errors.Is(err, ErrCardNotFound) {
		t.Fatalf("expected ErrCardNotFound, got %v", err)
	}
}

func TestPaymentTokenUUIDIsTenantScopedPayerCapability(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{Store: storeSvc}
	ctx := context.Background()
	creatorID := int64(42)
	payerID := int64(84)
	pan := "4242424242424242"

	if err := service.AddCardsForUserID(ctx, tenantID, creatorID, "0990000000", []ebs_fields.Card{{
		Pan:    pan,
		Expiry: "3012",
		Name:   "Creator",
	}}); err != nil {
		t.Fatalf("add creator card: %v", err)
	}
	created, _, _, err := service.GeneratePaymentTokenForUserID(ctx, tenantID, creatorID, ebs_fields.Token{Amount: 25})
	if err != nil {
		t.Fatalf("create payment token: %v", err)
	}

	creatorTokens, _, err := service.GetPaymentTokenForUserID(ctx, tenantID, creatorID, "")
	if err != nil || len(creatorTokens) != 1 {
		t.Fatalf("creator token list = %+v, err = %v", creatorTokens, err)
	}
	payerTokens, _, err := service.GetPaymentTokenForUserID(ctx, tenantID, payerID, "")
	if err != nil || len(payerTokens) != 0 {
		t.Fatalf("payer token list = %+v, err = %v; list must remain owner-scoped", payerTokens, err)
	}

	_, detail, err := service.GetPaymentTokenForUserID(ctx, tenantID, payerID, created.UUID)
	if err != nil {
		t.Fatalf("payer get by UUID: %v", err)
	}
	if detail == nil || detail.UUID != created.UUID || detail.ToCard != "424242*****4242" {
		t.Fatalf("payer detail = %+v, want masked capability detail", detail)
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal payer detail: %v", err)
	}
	for _, privateField := range []string{"UserID", "CreatedAt", "RailUUID", "PayerUserID", "ClaimedAmount"} {
		if strings.Contains(string(detailJSON), privateField) {
			t.Fatalf("payer detail leaks %s: %s", privateField, detailJSON)
		}
	}
	resolution, err := service.ResolveQuickPaymentTokenForUserID(ctx, tenantID, payerID, QuickPaymentTokenResolveCommand{
		UUID:   created.UUID,
		Amount: 25,
	})
	if err != nil {
		t.Fatalf("payer resolve by UUID: %v", err)
	}
	if resolution.UUID != created.UUID || resolution.RailUUID != created.RailUUID || resolution.ToCard != pan || resolution.Amount != 25 || resolution.RecipientUserID != creatorID {
		t.Fatalf("payer resolution = %+v", resolution)
	}
	if err := storeSvc.UpdateTokenCard(ctx, tenantID, created.UUID, "5555555555554444"); err == nil {
		t.Fatal("claimed token destination remained mutable")
	}

	otherTenant := "other-tenant"
	if _, _, err := service.GetPaymentTokenForUserID(ctx, otherTenant, payerID, created.UUID); !store.ErrNotFound(err) {
		t.Fatalf("cross-tenant detail error = %v, want not found", err)
	}
	if _, err := service.ResolveQuickPaymentTokenForUserID(ctx, otherTenant, payerID, QuickPaymentTokenResolveCommand{UUID: created.UUID, Amount: 25}); !store.ErrNotFound(err) {
		t.Fatalf("cross-tenant resolve error = %v, want not found", err)
	}
	if err := service.FinalizeQuickPaymentTokenForUserID(ctx, otherTenant, payerID, QuickPaymentTokenFinalizationCommand{UUID: created.UUID, RailUUID: created.RailUUID, Status: ebs_fields.PaymentTokenStatusPaid}); !store.ErrNotFound(err) {
		t.Fatalf("cross-tenant finalize error = %v, want not found", err)
	}

	if _, err := service.ResolveQuickPaymentTokenForUserID(ctx, tenantID, payerID, QuickPaymentTokenResolveCommand{UUID: created.UUID, Amount: 25}); !errors.Is(err, store.ErrPaymentTokenUnavailable) {
		t.Fatalf("sequential replay error = %v, want %v", err, store.ErrPaymentTokenUnavailable)
	}
	if err := service.FinalizeQuickPaymentTokenForUserID(ctx, tenantID, creatorID, QuickPaymentTokenFinalizationCommand{UUID: created.UUID, RailUUID: created.RailUUID, Status: ebs_fields.PaymentTokenStatusPaid}); !errors.Is(err, store.ErrPaymentTokenUnavailable) {
		t.Fatalf("wrong-payer finalize error = %v, want %v", err, store.ErrPaymentTokenUnavailable)
	}
	if err := service.FinalizeQuickPaymentTokenForUserID(ctx, tenantID, payerID, QuickPaymentTokenFinalizationCommand{UUID: created.UUID, RailUUID: "wrong-rail", Status: ebs_fields.PaymentTokenStatusPaid}); !errors.Is(err, store.ErrPaymentTokenUnavailable) {
		t.Fatalf("wrong-rail finalize error = %v, want %v", err, store.ErrPaymentTokenUnavailable)
	}
	if err := service.FinalizeQuickPaymentTokenForUserID(ctx, tenantID, payerID, QuickPaymentTokenFinalizationCommand{UUID: created.UUID, RailUUID: created.RailUUID, Status: ebs_fields.PaymentTokenStatusPaid}); err != nil {
		t.Fatalf("payer finalize by UUID: %v", err)
	}
	paid, err := storeSvc.GetTokenByUUID(ctx, tenantID, created.UUID)
	if err != nil {
		t.Fatalf("read paid token: %v", err)
	}
	if !paid.IsPaid {
		t.Fatal("payer capability did not mark token paid")
	}
	if paid.PaymentStatus != ebs_fields.PaymentTokenStatusPaid {
		t.Fatalf("payment status = %q, want paid", paid.PaymentStatus)
	}
	if err := service.FinalizeQuickPaymentTokenForUserID(ctx, tenantID, payerID, QuickPaymentTokenFinalizationCommand{UUID: created.UUID, RailUUID: created.RailUUID, Status: ebs_fields.PaymentTokenStatusPaid}); err != nil {
		t.Fatalf("idempotent paid finalization: %v", err)
	}
	if err := service.FinalizeQuickPaymentTokenForUserID(ctx, tenantID, payerID, QuickPaymentTokenFinalizationCommand{UUID: created.UUID, RailUUID: created.RailUUID, Status: ebs_fields.PaymentTokenStatusFailed}); !errors.Is(err, store.ErrPaymentTokenUnavailable) {
		t.Fatalf("conflicting finalization error = %v, want %v", err, store.ErrPaymentTokenUnavailable)
	}
	if _, err := service.ResolveQuickPaymentTokenForUserID(ctx, tenantID, payerID, QuickPaymentTokenResolveCommand{UUID: created.UUID, Amount: 25}); !errors.Is(err, store.ErrPaymentTokenUnavailable) {
		t.Fatalf("paid replay error = %v, want %v", err, store.ErrPaymentTokenUnavailable)
	}
}

func TestQuickPaymentTokenHasOneConcurrentClaimantAndStableRetryState(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{Store: storeSvc}
	ctx := context.Background()
	creatorID := int64(42)
	pan := "4242424242424242"

	if err := service.AddCardsForUserID(ctx, tenantID, creatorID, "0990000000", []ebs_fields.Card{{
		Pan:    pan,
		Expiry: "3012",
		Name:   "Creator",
	}}); err != nil {
		t.Fatalf("add creator card: %v", err)
	}
	created, _, _, err := service.GeneratePaymentTokenForUserID(ctx, tenantID, creatorID, ebs_fields.Token{Amount: 25})
	if err != nil {
		t.Fatalf("create payment token: %v", err)
	}

	const claimants = 16
	type claimResult struct {
		payerUserID int64
		err         error
	}
	start := make(chan struct{})
	results := make(chan claimResult, claimants)
	var ready sync.WaitGroup
	ready.Add(claimants)
	for i := range claimants {
		payerUserID := int64(100 + i)
		go func() {
			ready.Done()
			<-start
			_, err := service.ResolveQuickPaymentTokenForUserID(ctx, tenantID, payerUserID, QuickPaymentTokenResolveCommand{
				UUID:   created.UUID,
				Amount: created.Amount,
			})
			results <- claimResult{payerUserID: payerUserID, err: err}
		}()
	}
	ready.Wait()
	close(start)

	successes := 0
	conflicts := 0
	var winningPayerID int64
	for range claimants {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			winningPayerID = result.payerUserID
		case errors.Is(result.err, store.ErrPaymentTokenUnavailable):
			conflicts++
		default:
			t.Fatalf("claim error = %v", result.err)
		}
	}
	if successes != 1 || conflicts != claimants-1 {
		t.Fatalf("claim results: successes=%d conflicts=%d", successes, conflicts)
	}
	processing, err := storeSvc.GetTokenByUUID(ctx, tenantID, created.UUID)
	if err != nil {
		t.Fatalf("read processing token: %v", err)
	}
	if processing.PaymentStatus != ebs_fields.PaymentTokenStatusProcessing || processing.PayerUserID == nil || *processing.PayerUserID != winningPayerID || processing.ClaimedAmount == nil || *processing.ClaimedAmount != created.Amount {
		t.Fatalf("processing token = %+v", processing)
	}

	if err := service.FinalizeQuickPaymentTokenForUserID(ctx, tenantID, winningPayerID, QuickPaymentTokenFinalizationCommand{
		UUID:     created.UUID,
		RailUUID: created.RailUUID,
		Status:   ebs_fields.PaymentTokenStatusFailed,
	}); err != nil {
		t.Fatalf("finalize failed payment: %v", err)
	}
	failed, err := storeSvc.GetTokenByUUID(ctx, tenantID, created.UUID)
	if err != nil {
		t.Fatalf("read failed token: %v", err)
	}
	if failed.IsPaid || failed.PaymentStatus != ebs_fields.PaymentTokenStatusFailed {
		t.Fatalf("failed token = %+v", failed)
	}

	if _, err := service.ResolveQuickPaymentTokenForUserID(ctx, tenantID, winningPayerID, QuickPaymentTokenResolveCommand{
		UUID:   created.UUID,
		Amount: created.Amount,
	}); !errors.Is(err, store.ErrPaymentTokenUnavailable) {
		t.Fatalf("failed replay error = %v, want %v", err, store.ErrPaymentTokenUnavailable)
	}
}
