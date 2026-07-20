package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/tenantcatalog"
)

func TestOpaqueIdentifiersRequireExactCanonicalUUIDs(t *testing.T) {
	const canonical = "0f8fad5b-d9cb-469f-a165-70867728950e"
	tests := []struct {
		name      string
		normalize func(string) (string, error)
		missing   error
		invalid   error
	}{
		{name: "card ID", normalize: NormalizeCardID, missing: ErrMissingCardID, invalid: ErrInvalidCardID},
		{name: "enrollment ID", normalize: NormalizeEnrollmentID, missing: ErrInvalidEnrollmentIntent, invalid: ErrInvalidEnrollmentIntent},
		{name: "rail UUID", normalize: NormalizeRailUUID, missing: ErrMissingRailUUID, invalid: ErrInvalidRailUUID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.normalize(canonical)
			if err != nil || got != canonical {
				t.Fatalf("canonical value = %q, %v", got, err)
			}
			if _, err := tt.normalize(""); !errors.Is(err, tt.missing) {
				t.Fatalf("empty error = %v, want %v", err, tt.missing)
			}
			for _, value := range []string{" " + canonical, canonical + " ", "\t" + canonical, strings.ToUpper(canonical)} {
				if _, err := tt.normalize(value); !errors.Is(err, tt.invalid) {
					t.Fatalf("normalize(%q) error = %v, want %v", value, err, tt.invalid)
				}
			}
		})
	}
}

func TestOpaqueCardLifecycleIsTenantScopedAndPANIndependent(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	const (
		tenantA = "tenant-opaque-a"
		tenantB = "tenant-opaque-b"
		userA   = int64(101)
		userB   = int64(202)
	)
	if err := MigrateScope(ctx, db, MigrationScopeCardVault); err != nil {
		t.Fatalf("migrate card vault: %v", err)
	}
	storeSvc := New(db, WithDataKey("opaque-card-test-key"))
	provisionTestTenants(t, ctx, storeSvc,
		tenantcatalog.Tenant{ID: tenantA, Name: "Tenant A"},
		tenantcatalog.Tenant{ID: tenantB, Name: "Tenant B"},
	)

	baseTime := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	first := enrollVerifiedCard(t, storeSvc, tenantA, userA, "4242424242424242", "Daily", baseTime)
	second := enrollVerifiedCard(t, storeSvc, tenantA, userA, "5555555555554242", "Daily", baseTime.Add(time.Minute))
	if first.CardID == second.CardID {
		t.Fatal("different enrollments reused a card_id")
	}
	if first.MaskedPAN != "****4242" || second.MaskedPAN != "****4242" {
		t.Fatalf("canonical masks = %q, %q", first.MaskedPAN, second.MaskedPAN)
	}
	if !first.IsMain || second.IsMain {
		t.Fatalf("automatic main selection = first:%v second:%v", first.IsMain, second.IsMain)
	}

	cards, err := storeSvc.ListActiveCardSummaries(ctx, tenantA, userA)
	if err != nil {
		t.Fatalf("list owner cards: %v", err)
	}
	if len(cards) != 2 || cards[0].CardID != first.CardID || cards[1].CardID != second.CardID {
		t.Fatalf("owner cards = %+v", cards)
	}
	assertSafeCardSummaryJSON(t, cards)

	for _, tc := range []struct {
		name     string
		tenantID string
		userID   int64
	}{
		{name: "other user", tenantID: tenantA, userID: userB},
		{name: "other tenant same numeric user", tenantID: tenantB, userID: userA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listed, err := storeSvc.ListActiveCardSummaries(ctx, tc.tenantID, tc.userID)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(listed) != 0 {
				t.Fatalf("foreign cards leaked: %+v", listed)
			}
			if err := storeSvc.UpdateActiveCardName(ctx, tc.tenantID, tc.userID, first.CardID, "stolen"); !errors.Is(err, ErrCardNotFound) {
				t.Fatalf("foreign update error = %v, want %v", err, ErrCardNotFound)
			}
			if err := storeSvc.SetActiveMainCard(ctx, tc.tenantID, tc.userID, first.CardID); !errors.Is(err, ErrCardNotFound) {
				t.Fatalf("foreign main error = %v, want %v", err, ErrCardNotFound)
			}
			if err := storeSvc.RetireActiveCard(ctx, tc.tenantID, tc.userID, first.CardID); !errors.Is(err, ErrCardNotFound) {
				t.Fatalf("foreign retire error = %v, want %v", err, ErrCardNotFound)
			}
		})
	}

	var wait sync.WaitGroup
	errCh := make(chan error, 2)
	for _, cardID := range []string{first.CardID, second.CardID} {
		cardID := cardID
		wait.Add(1)
		go func() {
			defer wait.Done()
			errCh <- storeSvc.SetActiveMainCard(ctx, tenantA, userA, cardID)
		}()
	}
	wait.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent main selection: %v", err)
		}
	}
	var mainCount int
	countStmt := db.Rebind(`SELECT COUNT(*) FROM cards
		WHERE tenant_id = ? AND user_id = ? AND status = 'active' AND is_main = TRUE`)
	if err := db.GetContext(ctx, &mainCount, countStmt, tenantA, userA); err != nil {
		t.Fatalf("count main cards: %v", err)
	}
	if mainCount != 1 {
		t.Fatalf("active main count = %d, want 1", mainCount)
	}

	if err := storeSvc.SetActiveMainCard(ctx, tenantA, userA, first.CardID); err != nil {
		t.Fatalf("restore first as main: %v", err)
	}
	if err := storeSvc.RetireActiveCard(ctx, tenantA, userA, first.CardID); err != nil {
		t.Fatalf("retire first: %v", err)
	}
	cards, err = storeSvc.ListActiveCardSummaries(ctx, tenantA, userA)
	if err != nil {
		t.Fatalf("list after main retirement: %v", err)
	}
	if len(cards) != 1 || cards[0].CardID != second.CardID || !cards[0].IsMain {
		t.Fatalf("replacement main after retirement = %+v", cards)
	}
	reissued := enrollVerifiedCard(t, storeSvc, tenantA, userB, "4242424242424242", "Reissued", baseTime.Add(2*time.Minute))
	if reissued.CardID == first.CardID {
		t.Fatal("reissued PAN reused retired card_id")
	}
	otherTenant := enrollVerifiedCard(t, storeSvc, tenantB, userA, "4242424242424242", "Tenant B", baseTime)
	if otherTenant.CardID == first.CardID || otherTenant.CardID == reissued.CardID {
		t.Fatal("card_id was reused across tenants")
	}

	var fingerprint, ciphertext string
	secretStmt := db.Rebind(`SELECT pan_fingerprint, pan_ciphertext
		FROM cards WHERE tenant_id = ? AND card_id = ?::uuid`)
	if err := db.QueryRowContext(ctx, secretStmt, tenantA, reissued.CardID).Scan(&fingerprint, &ciphertext); err != nil {
		t.Fatalf("read stored secrets: %v", err)
	}
	for label, value := range map[string]string{
		"fingerprint": fingerprint, "ciphertext": ciphertext,
	} {
		if value == "4242424242424242" {
			t.Fatalf("%s stored clear PAN", label)
		}
	}
	if !strings.HasPrefix(fingerprint, "v1:h:") || !strings.HasPrefix(ciphertext, "enc:") {
		t.Fatalf("stored secret formats fingerprint=%q ciphertext=%q", fingerprint, ciphertext)
	}
}

func TestOpaqueCardEnrollmentIntentExpiryReplayAndConflict(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	const tenantID = "tenant-opaque-intents"
	if err := MigrateScope(ctx, db, MigrationScopeCardVault); err != nil {
		t.Fatalf("migrate card vault: %v", err)
	}
	storeSvc := New(db, WithDataKey("opaque-intent-test-key"))
	provisionTestTenant(t, ctx, storeSvc, tenantID, "Intent Tenant")
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	intent, err := storeSvc.CreateCardEnrollmentIntent(ctx, tenantID, 10, now, 5*time.Minute)
	if err != nil {
		t.Fatalf("create intent: %v", err)
	}
	if intent.EnrollmentID == intent.RailUUID || intent.Status != EnrollmentIntentPending {
		t.Fatalf("intent = %+v", intent)
	}
	attempt := CardEnrollmentAttempt{
		PAN: "4000000000001234", Expiry: "2912", Name: "Primary", OperationKind: CardEnrollmentOperation,
	}
	if _, err := storeSvc.CreateCardEnrollmentIntent(ctx, tenantID, 10, now.Add(time.Second), 5*time.Minute); !errors.Is(err, ErrEnrollmentIntentOpen) {
		t.Fatalf("second open intent error = %v, want %v", err, ErrEnrollmentIntentOpen)
	}
	if _, err := storeSvc.BeginCardEnrollmentIntent(ctx, tenantID, 11, intent.EnrollmentID, attempt, now); !errors.Is(err, ErrEnrollmentIntentNotFound) {
		t.Fatalf("foreign begin error = %v, want %v", err, ErrEnrollmentIntentNotFound)
	}
	if _, err := storeSvc.BeginCardEnrollmentIntent(ctx, tenantID, 10, intent.EnrollmentID, attempt, now); err != nil {
		t.Fatalf("begin intent: %v", err)
	}
	claimed, err := storeSvc.ClaimCardEnrollmentRailSubmission(ctx, tenantID, 10, intent.EnrollmentID, now)
	if err != nil || !claimed {
		t.Fatalf("claim rail submission = %v, %v", claimed, err)
	}
	claimed, err = storeSvc.ClaimCardEnrollmentRailSubmission(ctx, tenantID, 10, intent.EnrollmentID, now.Add(time.Millisecond))
	if err != nil || claimed {
		t.Fatalf("duplicate rail submission claim = %v, %v", claimed, err)
	}
	card, err := storeSvc.CompleteCardEnrollmentIntent(ctx, tenantID, 10, intent.EnrollmentID, VerifiedCardEnrollment{
		PAN: "4000000000001234", Expiry: "2912", Name: "Primary", VerificationMethod: "ebs_balance_v1",
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("complete intent: %v", err)
	}
	retry, err := storeSvc.BeginCardEnrollmentIntent(ctx, tenantID, 10, intent.EnrollmentID, attempt, now.Add(2*time.Second))
	if err != nil || retry.CompletedCard == nil || retry.CompletedCard.CardID != card.CardID {
		t.Fatalf("idempotent completed begin = %+v, %v", retry, err)
	}
	replayedCard, err := storeSvc.CompleteCardEnrollmentIntent(ctx, tenantID, 10, intent.EnrollmentID, VerifiedCardEnrollment{
		PAN: "4000000000001234", Expiry: "2912", Name: "Primary", VerificationMethod: "ebs_balance_v1",
	}, now.Add(2*time.Second))
	if err != nil || replayedCard.CardID != card.CardID {
		t.Fatalf("idempotent completion = %+v, %v", replayedCard, err)
	}
	changedAttempt := attempt
	changedAttempt.Expiry = "3012"
	if _, err := storeSvc.BeginCardEnrollmentIntent(ctx, tenantID, 10, intent.EnrollmentID, changedAttempt, now.Add(2*time.Second)); !errors.Is(err, ErrEnrollmentClaimMismatch) {
		t.Fatalf("changed replay error = %v, want %v", err, ErrEnrollmentClaimMismatch)
	}

	collisionIntent, err := storeSvc.CreateCardEnrollmentIntent(ctx, tenantID, 11, now.Add(3*time.Second), 5*time.Minute)
	if err != nil {
		t.Fatalf("create collision intent: %v", err)
	}
	collisionAttempt := CardEnrollmentAttempt{
		PAN: "4000000000001234", Expiry: "2912", Name: "Other", OperationKind: CardEnrollmentOperation,
	}
	if _, err := storeSvc.BeginCardEnrollmentIntent(ctx, tenantID, 11, collisionIntent.EnrollmentID, collisionAttempt, now.Add(3*time.Second)); err != nil {
		t.Fatalf("begin collision intent: %v", err)
	}
	if claimed, err := storeSvc.ClaimCardEnrollmentRailSubmission(ctx, tenantID, 11, collisionIntent.EnrollmentID, now.Add(3*time.Second)); err != nil || !claimed {
		t.Fatalf("claim collision rail = %v, %v", claimed, err)
	}
	if _, err := storeSvc.CompleteCardEnrollmentIntent(ctx, tenantID, 11, collisionIntent.EnrollmentID, VerifiedCardEnrollment{
		PAN: "4000000000001234", Expiry: "2912", Name: "Other", VerificationMethod: "ebs_balance_v1",
	}, now.Add(4*time.Second)); !errors.Is(err, ErrCardEnrollmentConflict) {
		t.Fatalf("active fingerprint collision error = %v, want %v", err, ErrCardEnrollmentConflict)
	}
	if err := storeSvc.FailCardEnrollmentIntent(ctx, tenantID, 11, collisionIntent.EnrollmentID, "duplicate_active_enrollment", now.Add(5*time.Second)); err != nil {
		t.Fatalf("fail collision intent: %v", err)
	}

	expiring, err := storeSvc.CreateCardEnrollmentIntent(ctx, tenantID, 12, now, time.Second)
	if err != nil {
		t.Fatalf("create expiring intent: %v", err)
	}
	if _, err := storeSvc.BeginCardEnrollmentIntent(ctx, tenantID, 12, expiring.EnrollmentID, CardEnrollmentAttempt{
		PAN: "4111111111111111", Expiry: "2912", Name: "Expired", OperationKind: CardEnrollmentOperation,
	}, now.Add(time.Second)); !errors.Is(err, ErrEnrollmentIntentExpired) {
		t.Fatalf("expired begin error = %v, want %v", err, ErrEnrollmentIntentExpired)
	}

	if err := storeSvc.RetireActiveCard(ctx, tenantID, 10, card.CardID); err != nil {
		t.Fatalf("retire enrolled card: %v", err)
	}
	reissue := enrollVerifiedCard(t, storeSvc, tenantID, 11, "4000000000001234", "Reissue", now.Add(10*time.Second))
	if reissue.CardID == card.CardID {
		t.Fatal("retired card id was reused")
	}
}

func enrollVerifiedCard(t *testing.T, storeSvc *Store, tenantID string, userID int64, pan, name string, now time.Time) ebs_fields.CardSummary {
	t.Helper()
	intent, err := storeSvc.CreateCardEnrollmentIntent(context.Background(), tenantID, userID, now, 5*time.Minute)
	if err != nil {
		t.Fatalf("create enrollment intent: %v", err)
	}
	attempt := CardEnrollmentAttempt{PAN: pan, Expiry: "2912", Name: name, OperationKind: CardEnrollmentOperation}
	if _, err := storeSvc.BeginCardEnrollmentIntent(context.Background(), tenantID, userID, intent.EnrollmentID, attempt, now); err != nil {
		t.Fatalf("begin enrollment intent: %v", err)
	}
	if claimed, err := storeSvc.ClaimCardEnrollmentRailSubmission(context.Background(), tenantID, userID, intent.EnrollmentID, now); err != nil || !claimed {
		t.Fatalf("claim enrollment rail = %v, %v", claimed, err)
	}
	card, err := storeSvc.CompleteCardEnrollmentIntent(context.Background(), tenantID, userID, intent.EnrollmentID, VerifiedCardEnrollment{
		PAN: pan, Expiry: "2912", Name: name, VerificationMethod: "ebs_balance_v1",
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("complete enrollment intent: %v", err)
	}
	return card
}

func assertSafeCardSummaryJSON(t *testing.T, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal card summary: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode card summary: %v", err)
	}
	forbidden := map[string]struct{}{
		"pan": {}, "ipin": {}, "pin": {}, "id": {}, "user_id": {},
		"pan_fingerprint": {}, "pan_ciphertext": {}, "ciphertext": {},
		"card_index": {}, "mobile": {},
	}
	var visit func(any)
	visit = func(node any) {
		switch typed := node.(type) {
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			for key, item := range typed {
				if _, found := forbidden[strings.ToLower(key)]; found {
					t.Fatalf("public card JSON contains forbidden key %q: %s", key, payload)
				}
				visit(item)
			}
		}
	}
	visit(decoded)
}
