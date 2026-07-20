package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFundedCardOperationClaimIsOwnedStableAndSingleGrant(t *testing.T) {
	ctx := context.Background()
	db := newMigrationAuthorityDB(t, MigrationScopeCardVault)
	const tenantID = "tenant-funded-claim"
	if err := MigrateScope(ctx, db, MigrationScopeCardVault); err != nil {
		t.Fatalf("migrate card vault: %v", err)
	}
	storeSvc := New(db, WithDataKey("funded-operation-test-key"))
	provisionTestTenant(t, ctx, storeSvc, tenantID, "Funded Claim Tenant")
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	first := enrollVerifiedCard(t, storeSvc, tenantID, 101, "4242420000004242", "Daily", now)
	second := enrollVerifiedCard(t, storeSvc, tenantID, 101, "4242421111114242", "Backup", now.Add(time.Minute))
	foreign := enrollVerifiedCard(t, storeSvc, tenantID, 202, "4242429999994242", "Foreign", now.Add(2*time.Minute))

	claim := FundedOperationClaim{
		UserID:           101,
		CardID:           first.CardID,
		RailUUID:         "123e4567-e89b-42d3-a456-426614174001",
		Purpose:          FundedPurposeBalanceInquiry,
		RailTranDateTime: "180726120000",
	}
	bodyClaim, err := FundedOperationBodyClaim(claim.CardID, claim.Purpose)
	if err != nil {
		t.Fatalf("body claim: %v", err)
	}
	claim.BodyClaim = bodyClaim
	grant, err := storeSvc.ClaimFundedCardOperation(ctx, tenantID, claim, now)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !grant.Granted || grant.PAN != "4242420000004242" || grant.Expiry != "2912" || grant.RailTranDateTime != claim.RailTranDateTime {
		t.Fatalf("first grant = %+v", grant)
	}

	retry := claim
	retry.RailTranDateTime = "180726120100"
	replayed, err := storeSvc.ClaimFundedCardOperation(ctx, tenantID, retry, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if replayed.Granted || replayed.PAN != "" || replayed.Expiry != "" || replayed.RailTranDateTime != claim.RailTranDateTime {
		t.Fatalf("retry grant = %+v", replayed)
	}

	changedBody := claim
	changedBody.BodyClaim = "v1:" + strings.Repeat("b", 64)
	if _, err := storeSvc.ClaimFundedCardOperation(ctx, tenantID, changedBody, now); !errors.Is(err, ErrFundedClaimMismatch) {
		t.Fatalf("changed body error = %v, want %v", err, ErrFundedClaimMismatch)
	}
	changedCard := claim
	changedCard.CardID = second.CardID
	changedCard.BodyClaim, err = FundedOperationBodyClaim(changedCard.CardID, changedCard.Purpose)
	if err != nil {
		t.Fatalf("changed-card body claim: %v", err)
	}
	if _, err := storeSvc.ClaimFundedCardOperation(ctx, tenantID, changedCard, now); !errors.Is(err, ErrFundedClaimMismatch) {
		t.Fatalf("changed owned card error = %v, want %v", err, ErrFundedClaimMismatch)
	}
	foreignClaim := claim
	foreignClaim.RailUUID = "123e4567-e89b-42d3-a456-426614174002"
	foreignClaim.CardID = foreign.CardID
	foreignClaim.BodyClaim, err = FundedOperationBodyClaim(foreignClaim.CardID, foreignClaim.Purpose)
	if err != nil {
		t.Fatalf("foreign body claim: %v", err)
	}
	if _, err := storeSvc.ClaimFundedCardOperation(ctx, tenantID, foreignClaim, now); !errors.Is(err, ErrCardNotFound) {
		t.Fatalf("foreign card error = %v, want %v", err, ErrCardNotFound)
	}

	if err := storeSvc.RetireActiveCard(ctx, tenantID, 101, second.CardID); err != nil {
		t.Fatalf("retire second card: %v", err)
	}
	retiredClaim := claim
	retiredClaim.RailUUID = "123e4567-e89b-42d3-a456-426614174003"
	retiredClaim.CardID = second.CardID
	retiredClaim.BodyClaim = changedCard.BodyClaim
	if _, err := storeSvc.ClaimFundedCardOperation(ctx, tenantID, retiredClaim, now); !errors.Is(err, ErrCardNotFound) {
		t.Fatalf("retired card error = %v, want %v", err, ErrCardNotFound)
	}

	concurrent := claim
	concurrent.RailUUID = "123e4567-e89b-42d3-a456-426614174004"
	const callers = 16
	start := make(chan struct{})
	results := make(chan FundedOperationGrant, callers)
	errs := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	for range callers {
		go func() {
			ready.Done()
			<-start
			result, err := storeSvc.ClaimFundedCardOperation(ctx, tenantID, concurrent, now)
			results <- result
			errs <- err
		}()
	}
	ready.Wait()
	close(start)
	grants := 0
	for range callers {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent claim: %v", err)
		}
		result := <-results
		if result.Granted {
			grants++
			if result.PAN != "4242420000004242" || result.Expiry != "2912" {
				t.Fatalf("concurrent secret grant = %+v", result)
			}
		} else if result.PAN != "" || result.Expiry != "" {
			t.Fatalf("non-winning claim exposed secrets: %+v", result)
		}
	}
	if grants != 1 {
		t.Fatalf("concurrent grants = %d, want 1", grants)
	}

	var rows int
	if err := db.GetContext(ctx, &rows, db.Rebind(`SELECT COUNT(*) FROM card_funded_operation_claims WHERE tenant_id = ?`), tenantID); err != nil {
		t.Fatalf("count funded claims: %v", err)
	}
	if rows != 2 {
		t.Fatalf("funded claim rows = %d, want 2", rows)
	}
	var stored string
	if err := db.GetContext(ctx, &stored, db.Rebind(`SELECT body_claim || ':' || purpose || ':' || rail_tran_date_time
		FROM card_funded_operation_claims WHERE tenant_id = ? AND rail_uuid = ?::uuid`), tenantID, claim.RailUUID); err != nil {
		t.Fatalf("read durable claim: %v", err)
	}
	if strings.Contains(stored, "4242420000004242") || strings.Contains(strings.ToLower(stored), "ipin") {
		t.Fatalf("durable claim contains card secret: %q", stored)
	}
}

func TestFundedCardOperationRejectsNonCanonicalIdentityBeforeStorage(t *testing.T) {
	claim := FundedOperationClaim{
		UserID:           101,
		CardID:           "123e4567-e89b-42d3-a456-426614174000",
		RailUUID:         "123e4567-e89b-42d3-a456-426614174001",
		Purpose:          FundedPurposeBalanceInquiry,
		RailTranDateTime: "180726120000",
	}
	bodyClaim, err := FundedOperationBodyClaim(claim.CardID, claim.Purpose)
	if err != nil {
		t.Fatalf("body claim: %v", err)
	}
	claim.BodyClaim = bodyClaim
	storeSvc := &Store{}
	for name, mutate := range map[string]func(*FundedOperationClaim){
		"card ID":    func(value *FundedOperationClaim) { value.CardID += " " },
		"rail UUID":  func(value *FundedOperationClaim) { value.RailUUID = " " + value.RailUUID },
		"body claim": func(value *FundedOperationClaim) { value.BodyClaim = strings.ToUpper(value.BodyClaim) },
		"purpose":    func(value *FundedOperationClaim) { value.Purpose = "purchase" },
		"rail time":  func(value *FundedOperationClaim) { value.RailTranDateTime += " " },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := claim
			mutate(&invalid)
			if _, err := storeSvc.ClaimFundedCardOperation(context.Background(), "tenant", invalid, time.Now()); err == nil || errors.Is(err, ErrMissingDataKey) {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}
