package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/adonese/noebs/internal/statusevent"
	"github.com/google/uuid"
)

func accountVerification(t *testing.T, s *Store, owner IdentityOwner, status, substatus string, source *uuid.UUID) IdentityVerification {
	t.Helper()
	result, err := s.GetIdentityVerification(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != status || result.Substatus != substatus || result.Revision < 1 || result.UpdatedAt.IsZero() ||
		(source == nil) != (result.SourceSessionID == nil) || source != nil && *source != *result.SourceSessionID {
		t.Fatalf("account state=%+v want=%s/%s source=%v", result, status, substatus, source)
	}
	return result
}

func approveIdentity(t *testing.T, s *Store, owner IdentityOwner, session IdentitySession) IdentitySession {
	t.Helper()
	reviewer := IdentityReviewer{TenantID: owner.TenantID, Actor: "account-reviewer"}
	reviewAccess(t, s, reviewer, owner, session)
	approved, err := s.DecideIdentityReview(t.Context(), reviewParams(reviewer, owner, session, "approved"))
	if err != nil {
		t.Fatal(err)
	}
	return approved
}

func TestIdentityVerificationApprovalSurvivesRenewalAndRecomputesAfterWithdrawal(t *testing.T) {
	s := newIdentityAuthTestStore(t, t.Context())
	owner := identityTestOwner(t, s, "tenant", "account-renewal")
	initial := accountVerification(t, s, owner, "unverified", "documents_required", nil)
	if initial.Revision != 1 {
		t.Fatalf("initial revision=%d", initial.Revision)
	}
	first := approveIdentity(t, s, owner, newReviewSubmission(t, s, owner, false))
	verified := accountVerification(t, s, owner, "verified", "approved", &first.ID)

	second := newReviewSubmission(t, s, owner, false)
	unchanged := accountVerification(t, s, owner, "verified", "approved", &first.ID)
	if unchanged.Revision != verified.Revision {
		t.Fatal("renewal changed live approval")
	}
	reviewer := IdentityReviewer{TenantID: owner.TenantID, Actor: "account-reviewer"}
	review, err := s.ReadIdentityReviewCase(t.Context(), reviewer, owner, second.ID)
	if err != nil || review.Session.Verification.Status != "pending" || review.AccountVerification.Status != "verified" {
		t.Fatalf("renewal review case=%+v error=%v", review, err)
	}
	second = approveIdentity(t, s, owner, second)
	replaced := accountVerification(t, s, owner, "verified", "approved", &second.ID)
	if replaced.Revision <= verified.Revision {
		t.Fatal("new approved source did not advance account revision")
	}
	if _, err := s.WithdrawIdentitySession(t.Context(), owner, second.ID, second.Revision); err != nil {
		t.Fatal(err)
	}
	fallback := accountVerification(t, s, owner, "verified", "approved", &first.ID)
	if fallback.Revision <= replaced.Revision {
		t.Fatal("fallback source did not advance account revision")
	}
	if _, err := s.WithdrawIdentitySession(t.Context(), owner, first.ID, first.Revision); err != nil {
		t.Fatal(err)
	}
	withdrawn := accountVerification(t, s, owner, "unverified", "withdrawn", &second.ID)
	if withdrawn.Revision <= fallback.Revision {
		t.Fatal("withdrawal did not advance account revision")
	}

	_ = newReviewSubmission(t, s, owner, true)
	afterSynthetic := accountVerification(t, s, owner, "unverified", "withdrawn", &second.ID)
	if afterSynthetic.Revision != withdrawn.Revision {
		t.Fatal("synthetic case changed real account")
	}
	var payloads [][]byte
	if err := s.DB.SelectContext(t.Context(), &payloads, `SELECT payload FROM identity_status_events WHERE tenant_id=$1 AND user_id=$2 ORDER BY revision`, owner.TenantID, owner.UserID); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 4 {
		t.Fatalf("renewal emitted contradictory or duplicate state events: count=%d", len(payloads))
	}
	want := []string{"unverified", "pending", "verified", "unverified"}
	for i, payload := range payloads {
		event, err := statusevent.Parse(payload)
		if err != nil || event.AggregateID != fmt.Sprintf("user:%d", owner.UserID) || event.UserID == nil || *event.UserID != owner.UserID || event.Status != want[i] {
			t.Fatalf("event[%d]=%+v error=%v", i, event, err)
		}
	}
}

func TestIdentityVerificationSelectsNewestCaseWithoutLiveApproval(t *testing.T) {
	s := newIdentityAuthTestStore(t, t.Context())
	owner := identityTestOwner(t, s, "tenant", "account-corrections")
	first := newReviewSubmission(t, s, owner, false)
	reviewer := IdentityReviewer{TenantID: owner.TenantID, Actor: "account-reviewer"}
	reviewAccess(t, s, reviewer, owner, first)
	if _, err := s.DecideIdentityReview(t.Context(), reviewParams(reviewer, owner, first, "needs_information")); err != nil {
		t.Fatal(err)
	}
	accountVerification(t, s, owner, "pending", "information_required", &first.ID)
	second := newReviewSubmission(t, s, owner, false)
	accountVerification(t, s, owner, "pending", "manual_review", &second.ID)
	reviewAccess(t, s, reviewer, owner, second)
	if _, err := s.DecideIdentityReview(t.Context(), reviewParams(reviewer, owner, second, "rejected")); err != nil {
		t.Fatal(err)
	}
	accountVerification(t, s, owner, "rejected", "rejected", &second.ID)
}

func TestIdentityVerificationRejectedRenewalKeepsApprovalUntilWithdrawal(t *testing.T) {
	s := newIdentityAuthTestStore(t, t.Context())
	owner := identityTestOwner(t, s, "tenant", "account-rejected-renewal")
	approved := approveIdentity(t, s, owner, newReviewSubmission(t, s, owner, false))
	before := accountVerification(t, s, owner, "verified", "approved", &approved.ID)
	renewal := newReviewSubmission(t, s, owner, false)
	reviewer := IdentityReviewer{TenantID: owner.TenantID, Actor: "account-reviewer"}
	reviewAccess(t, s, reviewer, owner, renewal)
	if _, err := s.DecideIdentityReview(t.Context(), reviewParams(reviewer, owner, renewal, "rejected")); err != nil {
		t.Fatal(err)
	}
	after := accountVerification(t, s, owner, "verified", "approved", &approved.ID)
	if after.Revision != before.Revision {
		t.Fatal("rejected renewal changed live approval")
	}
	if _, err := s.WithdrawIdentitySession(t.Context(), owner, approved.ID, approved.Revision); err != nil {
		t.Fatal(err)
	}
	accountVerification(t, s, owner, "rejected", "rejected", &renewal.ID)
}

func TestIdentityVerificationEventFailureRollsBackDecisionAndAccount(t *testing.T) {
	s := newIdentityAuthTestStore(t, t.Context())
	owner := identityTestOwner(t, s, "tenant", "account-event-rollback")
	session := newReviewSubmission(t, s, owner, false)
	before := accountVerification(t, s, owner, "pending", "manual_review", &session.ID)
	reviewer := IdentityReviewer{TenantID: owner.TenantID, Actor: "account-reviewer"}
	reviewAccess(t, s, reviewer, owner, session)
	if _, err := s.DB.ExecContext(t.Context(), `ALTER TABLE identity_status_events ADD CONSTRAINT reject_verified_fixture CHECK (payload->>'status'<>'verified')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideIdentityReview(t.Context(), reviewParams(reviewer, owner, session, "approved")); err == nil {
		t.Fatal("decision committed without its status event")
	}
	after := accountVerification(t, s, owner, "pending", "manual_review", &session.ID)
	stored, err := s.GetIdentitySession(t.Context(), owner, session.ID)
	if err != nil || stored.Status != "submitted" || stored.Revision != session.Revision || after.Revision != before.Revision {
		t.Fatalf("partial decision case=%+v account=%+v error=%v", stored, after, err)
	}
	var count int
	if err := s.DB.GetContext(t.Context(), &count, `SELECT count(*) FROM identity_review_events WHERE session_id=$1 AND action='decision'`, session.ID); err != nil || count != 0 {
		t.Fatalf("decision audit survived rollback: count=%d error=%v", count, err)
	}
}

func TestIdentityVerificationConcurrentApprovalsSerializePerOwner(t *testing.T) {
	s := newIdentityAuthTestStore(t, t.Context())
	owner := identityTestOwner(t, s, "tenant", "account-concurrent")
	first := newReviewSubmission(t, s, owner, false)
	second := newReviewSubmission(t, s, owner, false)
	reviewer := IdentityReviewer{TenantID: owner.TenantID, Actor: "account-reviewer"}
	reviewAccess(t, s, reviewer, owner, first)
	reviewAccess(t, s, reviewer, owner, second)
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	var wg sync.WaitGroup
	for _, session := range []IdentitySession{first, second} {
		wg.Add(1)
		go func(session IdentitySession) {
			defer wg.Done()
			<-start
			_, err := s.DecideIdentityReview(t.Context(), reviewParams(reviewer, owner, session, "approved"))
			outcomes <- err
		}(session)
	}
	close(start)
	wg.Wait()
	close(outcomes)
	for err := range outcomes {
		if err != nil {
			t.Fatal(err)
		}
	}
	accountVerification(t, s, owner, "verified", "approved", &second.ID)
	var count int
	if err := s.DB.GetContext(t.Context(), &count, `SELECT count(*) FROM identity_status_events WHERE tenant_id=$1 AND user_id=$2 AND payload->>'status'='verified'`, owner.TenantID, owner.UserID); err != nil || count != 1 {
		t.Fatalf("concurrent approval events=%d error=%v", count, err)
	}
}

func TestIdentityVerificationRuntimeAuthorityAndOwnerValidation(t *testing.T) {
	for _, tc := range []struct {
		owner IdentityOwner
		err   error
	}{
		{IdentityOwner{UserID: 1}, ErrMissingTenantID}, {IdentityOwner{TenantID: " tenant ", UserID: 1}, ErrInvalidTenantID}, {IdentityOwner{TenantID: "tenant"}, ErrInvalidUserID},
	} {
		if _, err := (&Store{}).GetIdentityVerification(t.Context(), tc.owner); !errors.Is(err, tc.err) {
			t.Fatalf("owner=%+v error=%v want=%v", tc.owner, err, tc.err)
		}
	}
	migration := newIdentityAuthTestStore(t, t.Context())
	owner := identityTestOwner(t, migration, "tenant", "account-authority")
	runtime := New(openMigrationAuthorityRoleDB(t, "identity_auth", "identity_auth_runtime"))
	t.Cleanup(func() { _ = runtime.DB.Close() })
	accountVerification(t, runtime, owner, "unverified", "documents_required", nil)
	other := IdentityOwner{TenantID: "tenant-other", UserID: owner.UserID}
	if _, err := runtime.GetIdentityVerification(t.Context(), other); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-tenant read=%v", err)
	}
	approved := approveIdentity(t, runtime, owner, newReviewSubmission(t, runtime, owner, false))
	accountVerification(t, runtime, owner, "verified", "approved", &approved.ID)
	for _, query := range []string{
		`UPDATE identity_verifications SET status='verified',substatus='approved'`,
		`DELETE FROM identity_verifications`,
		`INSERT INTO identity_verifications SELECT * FROM identity_verifications`,
		`INSERT INTO identity_status_events(tenant_id,user_id,revision,payload) SELECT tenant_id,user_id,revision+1,'{}' FROM identity_verifications`,
		`UPDATE identity_status_events SET payload='{}'`,
	} {
		if _, err := runtime.DB.ExecContext(t.Context(), query); err == nil {
			t.Fatalf("runtime allowed direct mutation: %s", query)
		}
	}
	outbox := &IdentityStatusOutbox{Store: runtime, Topic: "status"}
	events, err := outbox.ClaimPendingTransactionEvents(t.Context(), 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("runtime claim=%+v error=%v", events, err)
	}
	if err := outbox.MarkTransactionEventPublished(t.Context(), events[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityVerificationMigrationBackfillsExistingAccounts(t *testing.T) {
	db := newMigrationAuthorityDB(t, MigrationScopeIdentityAuth)
	migrateTestScopeThroughVersion(t, db, MigrationScopeIdentityAuth, 3)
	s := New(db)
	owner := identityTestOwner(t, s, "tenant", "account-backfill")
	approved := newReviewSubmission(t, s, owner, false)
	if _, err := s.DB.ExecContext(t.Context(), `UPDATE identity_sessions SET status='approved',revision=revision+1,
	 review='{"decision":"approved","reason":"existing approved case","method":"manual","reviewed_at":"2026-01-01T00:00:00Z"}'
	 WHERE tenant_id=$1 AND user_id=$2 AND id=$3`, owner.TenantID, owner.UserID, approved.ID); err != nil {
		t.Fatal(err)
	}
	_ = newReviewSubmission(t, s, owner, false)
	empty := identityTestOwner(t, s, "tenant", "account-backfill-empty")
	if err := MigrateScope(t.Context(), s.DB, MigrationScopeIdentityAuth); err != nil {
		t.Fatal(err)
	}
	accountVerification(t, s, owner, "verified", "approved", &approved.ID)
	accountVerification(t, s, empty, "unverified", "documents_required", nil)
	var count int
	if err := s.DB.GetContext(t.Context(), &count, `SELECT count(*) FROM identity_status_events`); err != nil || count != 0 {
		t.Fatalf("backfill replayed historical events: count=%d error=%v", count, err)
	}
	after := identityTestOwner(t, s, "tenant", "account-after-backfill")
	accountVerification(t, s, after, "unverified", "documents_required", nil)
}
