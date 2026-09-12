package store

import (
	"bytes"
	"os"
	"testing"

	"github.com/adonese/noebs/internal/statusevent"
)

func TestIdentityEventRecoveryReplaysStableEventsIntoExistingNotifications(t *testing.T) {
	s := newIdentityAuthTestStore(t, t.Context())
	owner := identityTestOwner(t, s, "tenant", "recovery-owner")
	_ = newReviewSubmission(t, s, owner, false)
	notificationDB := newMigrationAuthorityDB(t, MigrationScopeNotificationChat)
	if err := MigrateScope(t.Context(), notificationDB, MigrationScopeNotificationChat); err != nil {
		t.Fatal(err)
	}
	notifications := New(notificationDB)
	provisionTestTenant(t, t.Context(), notifications, owner.TenantID, "Recovery notifications")
	publisher := &IdentityStatusOutbox{Store: s, Topic: "status"}
	originals := []TransactionEvent{}
	for range 2 {
		events, err := publisher.ClaimPendingTransactionEvents(t.Context(), 10)
		if err != nil || len(events) != 1 {
			t.Fatalf("initial event=%+v error=%v", events, err)
		}
		originals = append(originals, events[0])
		if err := publisher.MarkTransactionEventPublished(t.Context(), events[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	event, err := statusevent.Parse(originals[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := notifications.StoreStatusNotification(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	// The second event was acknowledged by Kafka but absent from the sink snapshot.
	recovery, err := os.ReadFile("../scripts/exe/recovery/identity_auth.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := s.DB.ExecContext(t.Context(), string(recovery)); err != nil {
			t.Fatal(err)
		}
	}
	restoredPublisher := &IdentityStatusOutbox{Store: s, Topic: "status"}
	for _, original := range originals {
		replay, err := restoredPublisher.ClaimPendingTransactionEvents(t.Context(), 10)
		if err != nil || len(replay) != 1 || replay[0].ID != original.ID || !bytes.Equal(replay[0].Payload, original.Payload) {
			t.Fatalf("replay=%+v error=%v", replay, err)
		}
		event, err := statusevent.Parse(replay[0].Payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := notifications.StoreStatusNotification(t.Context(), event); err != nil {
			t.Fatal(err)
		}
		if err := restoredPublisher.MarkTransactionEventPublished(t.Context(), replay[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := notifications.ListStatusNotifications(t.Context(), owner.TenantID, owner.UserID, 10)
	if err != nil || len(stored) != 2 {
		t.Fatalf("recovery duplicated or lost events=%+v error=%v", stored, err)
	}
}
