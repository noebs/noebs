package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/store"
)

func TestStoreNotificationPushDataUsesNotificationScope(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeNotificationChat})
	service := &Service{Store: storeSvc}

	err := service.StoreNotificationPushData(context.Background(), tenantID, StorePushDataCommand{
		Data: PushData{
			UUID:       "notification-uuid",
			Type:       EBS_NOTIFICATION,
			Title:      "Payment Success",
			Body:       "stored by notification-chat",
			UserMobile: "0912141660",
		},
	})
	if err != nil {
		t.Fatalf("store notification push data: %v", err)
	}

	records, err := storeSvc.GetNotifications(context.Background(), tenantID, "0912141660")
	if err != nil {
		t.Fatalf("get notifications: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].UUID != "notification-uuid" || records[0].TenantID != tenantID {
		t.Fatalf("notification record = %+v", records[0])
	}
}

func TestStoreNotificationPushDataRejectsMissingInputs(t *testing.T) {
	service := &Service{Store: &store.Store{}}

	if err := service.StoreNotificationPushData(context.Background(), "", StorePushDataCommand{Data: PushData{UUID: "uuid"}}); !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("missing tenant error = %v, want %v", err, store.ErrMissingTenantID)
	}
	if err := service.StoreNotificationPushData(context.Background(), "tenant-a", StorePushDataCommand{}); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("missing uuid error = %v, want %v", err, ErrMissingUUID)
	}
}

func TestNotificationRecordForEventRequiresTransactionUUID(t *testing.T) {
	if _, err := notificationRecordForEvent(PushData{}, "sender"); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("notificationRecordForEvent() error = %v, want %v", err, ErrMissingUUID)
	}
}
