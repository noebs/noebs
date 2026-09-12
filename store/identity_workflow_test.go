package store

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func newReviewSubmission(t *testing.T, s *Store, owner IdentityOwner, synthetic bool) IdentitySession {
	t.Helper()
	draft, err := s.CreateIdentitySession(t.Context(), CreateIdentitySessionParams{Owner: owner, SessionID: uuid.New(), DocumentType: "national_id", Synthetic: synthetic})
	if err != nil {
		t.Fatal(err)
	}
	draft = identityUpload(t, s, owner, draft, "document_front")
	draft = identityUpload(t, s, owner, draft, "selfie")
	draft = identityUpload(t, s, owner, draft, "document_back")
	result, err := s.SubmitIdentitySession(t.Context(), owner, draft.ID, IdentitySubmission{Revision: draft.Revision, ConsentVersion: IdentityConsentVersion, FieldsReviewed: true, HolderName: "Fixture Review Applicant", DocumentNumber: "ISOLATED-FIXTURE-123"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func reviewAccess(t *testing.T, s *Store, reviewer IdentityReviewer, owner IdentityOwner, session IdentitySession) {
	t.Helper()
	if _, err := s.ReadIdentityReviewCase(t.Context(), reviewer, owner, session.ID); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range session.Evidence {
		if _, err := s.ReadIdentityReviewEvidence(t.Context(), reviewer, owner, session.ID, session.Revision, evidence.Kind); err != nil {
			t.Fatal(err)
		}
	}
}

func reviewParams(reviewer IdentityReviewer, owner IdentityOwner, session IdentitySession, decision string) IdentityReviewDecisionParams {
	return IdentityReviewDecisionParams{Reviewer: reviewer, Owner: owner, SessionID: session.ID, OperationID: uuid.New(), Revision: session.Revision, Decision: decision, Reason: "Review completed using the documented account policy.", PolicyReference: "manual-policy-fixture-v1", EvidenceReviewed: true}
}

func TestIdentityReviewDecisionRequiresActualAccessAndIsImmutable(t *testing.T) {
	ctx := t.Context()
	migration := newIdentityAuthTestStore(t, ctx)
	owner := identityTestOwner(t, migration, "tenant", "review-owner")
	runtime := New(openMigrationAuthorityRoleDB(t, "identity_auth", "identity_auth_runtime"))
	t.Cleanup(func() { _ = runtime.DB.Close() })
	session := newReviewSubmission(t, runtime, owner, false)
	reviewer := IdentityReviewer{TenantID: owner.TenantID, Actor: "reviewer-one"}
	params := reviewParams(reviewer, owner, session, "approved")
	if _, err := runtime.DecideIdentityReview(ctx, params); !errors.Is(err, ErrIdentityReviewIncomplete) {
		t.Fatalf("approval without reading evidence: %v", err)
	}
	if _, err := runtime.ReadIdentityReviewCase(ctx, reviewer, owner, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ReadIdentityReviewEvidence(ctx, reviewer, owner, session.ID, session.Revision, "document_front"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.DecideIdentityReview(ctx, params); !errors.Is(err, ErrIdentityReviewIncomplete) {
		t.Fatalf("approval with incomplete access: %v", err)
	}
	reviewAccess(t, runtime, reviewer, owner, session)
	otherParams := params
	otherParams.Reviewer.Actor = "another-reviewer"
	if _, err := runtime.DecideIdentityReview(ctx, otherParams); !errors.Is(err, ErrIdentityReviewIncomplete) {
		t.Fatalf("reviewer reused someone else's access: %v", err)
	}
	result, err := runtime.DecideIdentityReview(ctx, params)
	if err != nil || result.Status != "approved" || result.Review == nil || result.Review.Method != "manual" || result.Revision != session.Revision+1 {
		t.Fatalf("manual decision=%+v, %v", result, err)
	}
	restarted := New(runtime.DB)
	retry, err := restarted.DecideIdentityReview(ctx, params)
	if err != nil || retry.Revision != result.Revision {
		t.Fatalf("exact retry=%+v,%v", retry, err)
	}
	changed := params
	changed.Reason = "Changed decision explanation"
	if _, err := runtime.DecideIdentityReview(ctx, changed); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("changed retry=%v", err)
	}
	changed = params
	changed.OperationID = uuid.New()
	changed.Decision = "rejected"
	if _, err := runtime.DecideIdentityReview(ctx, changed); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("terminal overwrite=%v", err)
	}
	if _, err := runtime.PutIdentityEvidence(ctx, PutIdentityEvidenceParams{Owner: owner, SessionID: session.ID, Revision: result.Revision, Kind: "selfie", JPEG: []byte("replacement")}); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("reviewed evidence changed=%v", err)
	}
	var count int
	if err := runtime.DB.GetContext(ctx, &count, `SELECT count(*) FROM identity_review_events WHERE session_id=$1 AND action='decision'`, session.ID); err != nil || count != 1 {
		t.Fatalf("decision count=%d,%v", count, err)
	}
	var actor string
	if err := runtime.DB.GetContext(ctx, &actor, `SELECT database_actor FROM identity_review_events WHERE session_id=$1 AND action='decision'`, session.ID); err != nil || actor != "identity_auth_runtime" {
		t.Fatalf("database actor=%s,%v", actor, err)
	}
	// The deployed runtime can append audit terms, but cannot rewrite receipts or
	// forge the database login/time established by PostgreSQL.
	for _, statement := range []string{
		`UPDATE identity_review_events SET actor='forged'`,
		`DELETE FROM identity_review_events`,
		`TRUNCATE identity_review_events`,
		`INSERT INTO identity_review_events(id,tenant_id,actor,action,request_sha256,details,database_actor) VALUES(gen_random_uuid(),'tenant','x','queue_read',repeat('a',64),'{}','forged')`,
		`INSERT INTO identity_review_events(id,tenant_id,actor,action,request_sha256,details,created_at) VALUES(gen_random_uuid(),'tenant','x','queue_read',repeat('a',64),'{}',clock_timestamp())`,
	} {
		if _, err := runtime.DB.ExecContext(ctx, statement); err == nil {
			t.Fatalf("runtime allowed audit mutation: %s", statement)
		}
	}
	// Reapplying migration authority must retain the exact audit restrictions.
	if err := MigrateScope(ctx, migration.DB, MigrationScopeIdentityAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.DB.ExecContext(ctx, `DELETE FROM identity_review_events`); err == nil {
		t.Fatal("authority reconciliation reopened audit deletion")
	}
}

func TestIdentityReviewLatestIsolationLegacyConsentAndCorrections(t *testing.T) {
	ctx := t.Context()
	s := newIdentityAuthTestStore(t, ctx)
	owner := identityTestOwner(t, s, "tenant", "latest-owner")
	other := identityTestOwner(t, s, "tenant", "latest-other")
	otherTenant := identityTestOwner(t, s, "tenant-other", "latest-owner")
	reviewer := IdentityReviewer{TenantID: owner.TenantID, Actor: "reviewer"}
	legacy := newReviewSubmission(t, s, owner, true)
	if _, err := s.LatestIdentitySession(ctx, owner); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy became customer latest=%v", err)
	}
	reviewAccess(t, s, reviewer, owner, legacy)
	if _, err := s.DecideIdentityReview(ctx, reviewParams(reviewer, owner, legacy, "approved")); !errors.Is(err, ErrInvalidIdentityEvidence) {
		t.Fatalf("test evidence approved=%v", err)
	}
	draft, err := s.CreateIdentitySession(ctx, CreateIdentitySessionParams{Owner: owner, SessionID: uuid.New(), DocumentType: "passport"})
	if err != nil || draft.Synthetic {
		t.Fatalf("ordinary create=%+v,%v", draft, err)
	}
	draft = identityUpload(t, s, owner, draft, "document_front")
	draft = identityUpload(t, s, owner, draft, "selfie")
	submission := IdentitySubmission{Revision: draft.Revision, ConsentVersion: LegacyIdentityConsentVersion, FieldsReviewed: true, HolderName: "Fixture Person"}
	if _, err := s.SubmitIdentitySession(ctx, owner, draft.ID, submission); !errors.Is(err, ErrInvalidIdentityEvidence) {
		t.Fatalf("old consent accepted real intake=%v", err)
	}
	submission.ConsentVersion = IdentityConsentVersion
	submitted, err := s.SubmitIdentitySession(ctx, owner, draft.ID, submission)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestIdentitySession(ctx, owner)
	if err != nil || latest.ID != submitted.ID {
		t.Fatalf("latest=%+v,%v", latest, err)
	}
	for _, intruder := range []IdentityOwner{other, otherTenant} {
		if _, err := s.LatestIdentitySession(ctx, intruder); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("cross-account latest=%v", err)
		}
		if _, err := s.WithdrawIdentitySession(ctx, intruder, submitted.ID, submitted.Revision); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("cross-account withdraw=%v", err)
		}
		if _, err := s.ReadIdentityReviewCase(ctx, reviewer, intruder, submitted.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("cross-account review read=%v", err)
		}
	}
	newParams := CreateIdentitySessionParams{Owner: owner, SessionID: uuid.New(), DocumentType: "national_id", PreviousSessionID: &submitted.ID}
	if _, err := s.CreateIdentitySession(ctx, newParams); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("correction from undecided case=%v", err)
	}
	reviewAccess(t, s, reviewer, owner, submitted)
	reviewed, err := s.DecideIdentityReview(ctx, reviewParams(reviewer, owner, submitted, "needs_information"))
	if err != nil {
		t.Fatal(err)
	}
	newParams.Owner = other
	if _, err := s.CreateIdentitySession(ctx, newParams); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("correction cross-owner=%v", err)
	}
	newParams.Owner = owner
	correction, err := s.CreateIdentitySession(ctx, newParams)
	if err != nil || correction.PreviousSessionID == nil || *correction.PreviousSessionID != submitted.ID {
		t.Fatalf("correction=%+v,%v", correction, err)
	}
	prior, err := s.GetIdentitySession(ctx, owner, submitted.ID)
	if err != nil || prior.Status != "needs_information" || prior.Revision != reviewed.Revision || len(prior.Evidence) != 2 {
		t.Fatalf("reviewed case mutated by correction=%+v,%v", prior, err)
	}
	// A correction's tombstone must remain latest, rather than bringing the older
	// completed case back as the current customer result.
	withdrawn, err := s.WithdrawIdentitySession(ctx, owner, correction.ID, correction.Revision)
	if err != nil {
		t.Fatal(err)
	}
	latest, err = s.LatestIdentitySession(ctx, owner)
	if err != nil || latest.ID != withdrawn.ID || latest.Status != "withdrawn" {
		t.Fatalf("withdrawal resurrected previous status=%+v,%v", latest, err)
	}
	queue, err := s.ListIdentityReviewQueue(ctx, reviewer, 50, 0)
	if err != nil || len(queue) != 0 {
		t.Fatalf("queue leaked legacy/completed cases=%+v,%v", queue, err)
	}
}

func TestIdentityReviewWithdrawalRemovesEvidenceAndClaimsButPreservesDecisionAudit(t *testing.T) {
	ctx := t.Context()
	s := newIdentityAuthTestStore(t, ctx)
	owner := identityTestOwner(t, s, "tenant", "withdraw-owner")
	reviewer := IdentityReviewer{TenantID: owner.TenantID, Actor: "reviewer"}
	session := newReviewSubmission(t, s, owner, false)
	reviewAccess(t, s, reviewer, owner, session)
	params := reviewParams(reviewer, owner, session, "approved")
	approved, err := s.DecideIdentityReview(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.WithdrawIdentitySession(ctx, owner, session.ID, approved.Revision)
	if err != nil || result.Status != "withdrawn" || len(result.Evidence) != 0 || len(result.Submission) != 0 || result.Review != nil {
		t.Fatalf("withdrawal leaked evidence or claims=%+v,%v", result, err)
	}
	for _, revision := range []int64{approved.Revision, result.Revision} {
		if retry, err := s.WithdrawIdentitySession(ctx, owner, session.ID, revision); err != nil || retry.Revision != result.Revision {
			t.Fatalf("withdraw retry=%+v,%v", retry, err)
		}
	}
	if retry, err := s.DecideIdentityReview(ctx, params); err != nil || retry.Status != "withdrawn" {
		t.Fatalf("decision retry after withdrawal=%+v,%v", retry, err)
	}
	if _, err := s.ReadIdentityReviewEvidence(ctx, reviewer, owner, session.ID, result.Revision, "selfie"); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("withdrawn image read=%v", err)
	}
	if _, err := s.PutIdentityEvidence(ctx, PutIdentityEvidenceParams{Owner: owner, SessionID: session.ID, Revision: result.Revision, Kind: "selfie", JPEG: []byte("late")}); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("late image resurrected=%v", err)
	}
	var count int
	if err = s.DB.GetContext(ctx, &count, `SELECT count(*) FROM identity_evidence WHERE session_id=$1`, session.ID); err != nil || count != 0 {
		t.Fatalf("stored images remain=%d,%v", count, err)
	}
	caseResult, err := s.ReadIdentityReviewCase(ctx, reviewer, owner, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	decisions, withdrawals := 0, 0
	for _, event := range caseResult.Events {
		if event.Action == "decision" {
			decisions++
		}
		if event.Action == "withdrawal" {
			withdrawals++
		}
		if strings.Contains(string(event.Details), "ISOLATED-FIXTURE-123") || strings.Contains(string(event.Details), "Fixture Review Applicant") {
			t.Fatal("audit copied private document claims")
		}
	}
	if decisions != 1 || withdrawals != 1 {
		t.Fatalf("decision/withdrawal audit=%d/%d", decisions, withdrawals)
	}
}

func TestIdentityReviewConcurrentDecisionAndWithdrawalHaveOneRevisionWinner(t *testing.T) {
	ctx := t.Context()
	s := newIdentityAuthTestStore(t, ctx)
	owner := identityTestOwner(t, s, "tenant", "race-review-owner")
	reviewer := IdentityReviewer{TenantID: owner.TenantID, Actor: "reviewer"}
	for range 4 {
		session := newReviewSubmission(t, s, owner, false)
		reviewAccess(t, s, reviewer, owner, session)
		params := reviewParams(reviewer, owner, session, "approved")
		start := make(chan struct{})
		outcomes := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, err := s.DecideIdentityReview(ctx, params); outcomes <- err }()
		go func() {
			defer wg.Done()
			<-start
			_, err := s.WithdrawIdentitySession(ctx, owner, session.ID, session.Revision)
			outcomes <- err
		}()
		close(start)
		wg.Wait()
		close(outcomes)
		successes, conflicts := 0, 0
		for err := range outcomes {
			if err == nil {
				successes++
			} else if errors.Is(err, ErrIdentityConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("decision-withdraw race=%d successes/%d conflicts", successes, conflicts)
		}
		result, err := s.GetIdentitySession(ctx, owner, session.ID)
		if err != nil || result.Revision != session.Revision+1 {
			t.Fatalf("race result=%+v,%v", result, err)
		}
		if result.Status == "withdrawn" && len(result.Evidence) != 0 {
			t.Fatal("withdrawal winner retained image bytes")
		}
	}
}
