package store

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/statusevent"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/google/uuid"
)

func TestStatusNotificationsReplayIsolationAndConflict(t *testing.T) {
	db := newMigrationAuthorityDB(t, MigrationScopeNotificationChat)
	if err := MigrateScope(t.Context(), db, MigrationScopeNotificationChat); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	provisionTestTenants(t, t.Context(), s, tenantcatalog.Tenant{ID: "other", Name: "Other"}, tenantcatalog.Tenant{ID: "tenant", Name: "Notifications"})
	user := int64(7)
	event := statusevent.Event{ID: uuid.New(), Type: statusevent.Verification, TenantID: "tenant", UserID: &user, AggregateType: "verification", AggregateID: uuid.NewString(), Version: 3, Status: "pending", Substatus: "manual_review", OccurredAt: time.Now().UTC()}
	var group sync.WaitGroup
	for range 6 {
		group.Go(func() {
			if err := s.StoreStatusNotification(t.Context(), event); err != nil {
				t.Errorf("concurrent replay: %v", err)
			}
		})
	}
	group.Wait()
	for _, scope := range []struct {
		tenant string
		user   int64
		count  int
	}{{"tenant", user, 1}, {"other", user, 0}, {"tenant", user + 1, 0}} {
		events, err := s.ListStatusNotifications(t.Context(), scope.tenant, scope.user, 50)
		if err != nil || len(events) != scope.count {
			t.Fatalf("scope=%+v events=%+v err=%v", scope, events, err)
		}
	}
	changed := event
	changed.Status = "verified"
	changed.Substatus = "approved"
	if err := s.StoreStatusNotification(t.Context(), changed); !errors.Is(err, ErrStatusNotificationConflict) {
		t.Fatalf("changed duplicate=%v", err)
	}
	changed = event
	changed.ID = uuid.New()
	if err := s.StoreStatusNotification(t.Context(), changed); !errors.Is(err, ErrStatusNotificationConflict) {
		t.Fatalf("duplicate aggregate version=%v", err)
	}
	events, err := s.ListStatusNotifications(t.Context(), "tenant", user, 50)
	if err != nil || len(events) != 1 || events[0].Status != "pending" {
		t.Fatalf("conflicting delivery changed history=%+v,%v", events, err)
	}
}

func TestStatusNotificationsRequireExplicitOwnerAndScope(t *testing.T) {
	s := &Store{}
	for _, test := range []struct {
		tenant string
		user   int64
		err    error
	}{{"", 1, ErrMissingTenantID}, {" tenant ", 1, ErrInvalidTenantID}, {"tenant", 0, ErrInvalidUserID}} {
		if _, err := s.ListStatusNotifications(t.Context(), test.tenant, test.user, 50); !errors.Is(err, test.err) {
			t.Fatalf("scope=%+v error=%v", test, err)
		}
	}
}
