package store

import (
	"errors"
	"testing"

	"github.com/adonese/noebs/internal/statusevent"
	"github.com/google/uuid"
)

func TestIdentityLifecycleEventsAreAtomicOrderedAndFenced(t *testing.T) {
	s := newIdentityAuthTestStore(t, t.Context())
	owner := identityTestOwner(t, s, "tenant", "lifecycle-owner")
	session := newReviewSubmission(t, s, owner, false)
	a := &IdentityStatusOutbox{Store: s, Topic: "status"}
	b := &IdentityStatusOutbox{Store: s, Topic: "status"}
	first, err := a.ClaimPendingTransactionEvents(t.Context(), 100)
	if err != nil || len(first) != 1 {
		t.Fatalf("first=%+v,%v", first, err)
	}
	event, err := statusevent.Parse(first[0].Payload)
	if err != nil || event.Status != "unverified" || event.Substatus != "documents_required" {
		t.Fatalf("event=%+v,%v", event, err)
	}
	if pending, err := b.ClaimPendingTransactionEvents(t.Context(), 100); err != nil || len(pending) != 0 {
		t.Fatalf("concurrent claim=%+v,%v", pending, err)
	}
	if _, err := s.DB.ExecContext(t.Context(), `UPDATE identity_status_events SET claimed_until=clock_timestamp()-interval '1 second' WHERE id=$1`, first[0].ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := b.ClaimPendingTransactionEvents(t.Context(), 100)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != first[0].ID {
		t.Fatalf("reclaimed=%+v,%v", reclaimed, err)
	}
	if err := a.MarkTransactionEventPublished(t.Context(), first[0].ID); !errors.Is(err, ErrIdentityEventLeaseLost) {
		t.Fatalf("stale publisher=%v", err)
	}
	if err := b.MarkTransactionEventPublished(t.Context(), first[0].ID); err != nil {
		t.Fatal(err)
	}
	second, err := a.ClaimPendingTransactionEvents(t.Context(), 100)
	if err != nil || len(second) != 1 {
		t.Fatalf("second=%+v,%v", second, err)
	}
	event, err = statusevent.Parse(second[0].Payload)
	account, accountErr := s.GetIdentityVerification(t.Context(), owner)
	if err != nil || accountErr != nil || event.Status != "pending" || event.Version != account.Revision {
		t.Fatalf("submission event=%+v,%v", event, err)
	}
	if _, err := s.DB.ExecContext(t.Context(), `UPDATE identity_sessions SET status='draft',revision=revision+1 WHERE tenant_id=$1 AND user_id=$2 AND id=$3`, owner.TenantID, owner.UserID, session.ID); err == nil {
		t.Fatal("database accepted backward transition")
	}
	var count int
	if err := s.DB.GetContext(t.Context(), &count, `SELECT count(*) FROM identity_status_events WHERE tenant_id=$1 AND user_id=$2`, owner.TenantID, owner.UserID); err != nil || count != 2 {
		t.Fatalf("failed transition leaked event count=%d,error=%v", count, err)
	}
	stored, err := s.GetIdentitySession(t.Context(), owner, session.ID)
	if err != nil || stored.Verification.Status != "pending" || stored.Verification.Substatus != "manual_review" {
		t.Fatalf("state=%+v,%v", stored, err)
	}
}

func TestSyntheticIdentityDoesNotNotifyCustomer(t *testing.T) {
	s := newIdentityAuthTestStore(t, t.Context())
	owner := identityTestOwner(t, s, "tenant", "synthetic-events")
	var before int
	if err := s.DB.GetContext(t.Context(), &before, `SELECT count(*) FROM identity_status_events`); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateIdentitySession(t.Context(), CreateIdentitySessionParams{Owner: owner, SessionID: uuid.New(), DocumentType: "passport", Synthetic: true})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB.GetContext(t.Context(), &count, `SELECT count(*) FROM identity_status_events`); err != nil || count != before {
		t.Fatalf("synthetic notifications=%d,%v", count, err)
	}
}
