package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/pressly/goose/v3"
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
	if err := MigrateScope(ctx, db, tenantA, MigrationScopeCardVault); err != nil {
		t.Fatalf("migrate card vault: %v", err)
	}
	storeSvc := New(db, WithDataKey("opaque-card-test-key"))
	for _, tenantID := range []string{tenantA, tenantB} {
		if err := storeSvc.EnsureTenant(ctx, tenantID); err != nil {
			t.Fatalf("ensure %s: %v", tenantID, err)
		}
	}

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
	if err := MigrateScope(ctx, db, tenantID, MigrationScopeCardVault); err != nil {
		t.Fatalf("migrate card vault: %v", err)
	}
	storeSvc := New(db, WithDataKey("opaque-intent-test-key"))
	if err := storeSvc.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
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

func TestLegacyCardRowsAreQuarantinedFromOpaqueList(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	const tenantID = "tenant-opaque-legacy"
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	goose.SetTableName(migrationScopeTableNames[MigrationScopeCardVault])
	goose.SetBaseFS(postgresMigrations)
	if err := goose.UpToContext(ctx, db.DB.DB, migrationScopePaths[MigrationScopeCardVault], 104); err != nil {
		t.Fatalf("migrate card vault to legacy version: %v", err)
	}
	if _, err := db.ExecContext(ctx, db.Rebind(`INSERT INTO tenants(id, name, created_at) VALUES(?, ?, ?)`), tenantID, tenantID, time.Now().UTC()); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO cards(
		tenant_id, user_id, mobile, pan, pan_enc, expiry, name, ipin, ipin_enc,
		is_main, is_valid, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, TRUE, TRUE, ?, ?)`)
	if _, err := db.ExecContext(ctx, stmt,
		tenantID, 41, "0912000041", "4242424242424242", "enc:legacy-pan-ciphertext",
		"2912", "Legacy", "1234", "enc:legacy-ipin", now, now,
	); err != nil {
		t.Fatalf("insert legacy card: %v", err)
	}
	if err := MigrateScope(ctx, db, tenantID, MigrationScopeCardVault); err != nil {
		t.Fatalf("apply opaque cutover: %v", err)
	}
	storeSvc := New(db, WithDataKey("opaque-legacy-test-key"))
	var cardID, status string
	readStmt := db.Rebind(`SELECT card_id::text, status
		FROM legacy_card_quarantine
		WHERE tenant_id = ? AND user_id = ?`)
	if err := db.QueryRowContext(ctx, readStmt, tenantID, 41).Scan(&cardID, &status); err != nil {
		t.Fatalf("read legacy card: %v", err)
	}
	if _, err := NormalizeCardID(cardID); err != nil {
		t.Fatalf("legacy card_id = %q: %v", cardID, err)
	}
	if status != CardStatusLegacyUnverified {
		t.Fatalf("legacy state status=%q", status)
	}
	cards, err := storeSvc.ListActiveCardSummaries(ctx, tenantID, 41)
	if err != nil {
		t.Fatalf("list opaque cards: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("legacy card leaked in public list: %+v", cards)
	}
	for _, table := range []string{"cards", "legacy_card_quarantine"} {
		for _, column := range []string{
			"pan", "pan_enc", "legacy_pan_fingerprint", "legacy_pan_ciphertext",
			"ipin", "ipin_enc", "mobile", "is_valid",
		} {
			var count int
			if err := db.GetContext(ctx, &count, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`, table, column); err != nil {
				t.Fatalf("inspect %s.%s: %v", table, column, err)
			}
			if count != 0 {
				t.Fatalf("unsafe legacy column survived: %s.%s", table, column)
			}
		}
	}
	var cacheTable sql.NullString
	if err := db.GetContext(ctx, &cacheTable, `SELECT to_regclass('cache_cards')::text`); err != nil {
		t.Fatalf("inspect cache_cards: %v", err)
	}
	if cacheTable.Valid {
		t.Fatalf("cache_cards survived opaque cutover: %q", cacheTable.String)
	}
	if err := goose.DownToContext(ctx, db.DB.DB, migrationScopePaths[MigrationScopeCardVault], 104); err == nil || !strings.Contains(err.Error(), "migration 105 is irreversible") {
		t.Fatalf("irreversible down error = %v", err)
	}
	version, err := goose.GetDBVersionContext(ctx, db.DB.DB)
	if err != nil {
		t.Fatalf("read card-vault migration version: %v", err)
	}
	if version != 105 {
		t.Fatalf("card-vault migration version after rejected down = %d, want 105", version)
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
