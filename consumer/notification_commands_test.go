package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestStoreNotificationPushDataUsesNotificationScope(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeNotificationChat})
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

	var stored struct {
		UUID     string `db:"uuid"`
		TenantID string `db:"tenant_id"`
	}
	stmt := db.Rebind(`SELECT uuid, tenant_id FROM push_data WHERE tenant_id = ? AND uuid = ?`)
	if err := db.GetContext(context.Background(), &stored, stmt, tenantID, "notification-uuid"); err != nil {
		t.Fatalf("read persisted notification: %v", err)
	}
	if stored.UUID != "notification-uuid" || stored.TenantID != tenantID {
		t.Fatalf("notification record = %+v", stored)
	}
}

func TestStoreNotificationPushDataRejectsMissingInputs(t *testing.T) {
	service := &Service{Store: &store.Store{}}

	if err := service.StoreNotificationPushData(context.Background(), "", StorePushDataCommand{Data: PushData{UUID: "uuid"}}); !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("missing tenant error = %v, want %v", err, store.ErrMissingTenantID)
	}
	if err := service.StoreNotificationPushData(context.Background(), "default", StorePushDataCommand{Data: PushData{UUID: "uuid"}}); !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("reserved tenant error = %v, want %v", err, store.ErrInvalidTenantID)
	}
	if err := service.StoreNotificationPushData(context.Background(), "tenant-a", StorePushDataCommand{}); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("missing uuid error = %v, want %v", err, ErrMissingUUID)
	}
	if err := service.StoreNotificationPushData(context.Background(), "tenant-a", StorePushDataCommand{Data: PushData{UUID: "uuid"}}); !errors.Is(err, store.ErrMissingPushTarget) {
		t.Fatalf("missing push target error = %v, want %v", err, store.ErrMissingPushTarget)
	}
}

func TestNotificationRecordForEventRequiresTransactionUUID(t *testing.T) {
	if _, err := notificationRecordForEvent(PushData{}, "sender"); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("notificationRecordForEvent() error = %v, want %v", err, ErrMissingUUID)
	}
}

func TestNotificationTransactionUUIDIsDistinctAndTapAuthorizationUsesParticipants(t *testing.T) {
	_, transactionStore, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	notificationDB, notificationStore, notificationTenantID := newTestDBWithScopes(t, []string{store.MigrationScopeNotificationChat})
	if notificationTenantID != tenantID {
		t.Fatalf("notification tenant = %q, want %q", notificationTenantID, tenantID)
	}
	transactionService := &Service{
		Store:       transactionStore,
		NoebsConfig: ebs_fields.NoebsConfig{KafkaTransactionTopic: testKafkaTransactionTopic},
	}
	notificationService := &Service{Store: notificationStore}
	const transactionUUID = "ed9de23b-734f-4db4-91f0-b6299a7b80a2"
	actorCtx, err := WithTransactionActor(context.Background(), 42)
	if err != nil {
		t.Fatalf("bind actor: %v", err)
	}
	if err := transactionService.recordTransaction(actorCtx, tenantID, ebs_fields.EBSResponse{UUID: transactionUUID, ResponseCode: 0}); err != nil {
		t.Fatalf("record transaction: %v", err)
	}
	notification, err := notificationRecordForEvent(PushData{
		UUID: transactionUUID, UserMobile: "0912141660", Type: EBS_NOTIFICATION,
	}, "sender")
	if err != nil {
		t.Fatalf("build notification event: %v", err)
	}
	if notification.UUID != transactionUUID+":sender" || notification.TransactionUUID != transactionUUID || notification.EBSUUID != transactionUUID {
		t.Fatalf("notification identities = %+v", notification)
	}
	payload, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("marshal push payload: %v", err)
	}
	if !strings.Contains(string(payload), `"uuid":"`+transactionUUID+`:sender"`) ||
		!strings.Contains(string(payload), `"transaction_uuid":"`+transactionUUID+`"`) ||
		strings.Contains(string(payload), "ebs_uuid") {
		t.Fatalf("push payload identities = %s", payload)
	}
	if err := notificationService.StoreNotificationPushData(context.Background(), tenantID, StorePushDataCommand{Data: notification}); err != nil {
		t.Fatalf("store notification: %v", err)
	}
	var stored struct {
		UUID    string `db:"uuid"`
		EBSUUID string `db:"ebs_uuid"`
	}
	stmt := notificationDB.Rebind(`SELECT uuid, ebs_uuid FROM push_data WHERE tenant_id = ? AND uuid = ?`)
	if err := notificationDB.GetContext(context.Background(), &stored, stmt, tenantID, transactionUUID+":sender"); err != nil {
		t.Fatalf("read persisted notification: %v", err)
	}
	if stored.UUID != transactionUUID+":sender" || stored.EBSUUID != transactionUUID {
		t.Fatalf("stored notification identities = %+v", stored)
	}
	if _, err := transactionService.GetTransactionByUUIDForUser(context.Background(), tenantID, 42, stored.EBSUUID); err != nil {
		t.Fatalf("participant notification tap: %v", err)
	}
	if _, err := transactionService.GetTransactionByUUIDForUser(context.Background(), tenantID, 84, stored.EBSUUID); !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("non-participant notification tap error = %v, want %v", err, ErrTransactionNotFound)
	}
}

func TestNotificationRecordRejectsNonCanonicalTransactionUUID(t *testing.T) {
	for _, value := range []string{
		"not-a-uuid",
		"ED9DE23B-734F-4DB4-91F0-B6299A7B80A2",
		" ed9de23b-734f-4db4-91f0-b6299a7b80a2 ",
	} {
		if _, err := notificationRecordForEvent(PushData{UUID: value}, "sender"); !errors.Is(err, store.ErrInvalidTransactionUUID) {
			t.Fatalf("notificationRecordForEvent(%q) error = %v, want %v", value, err, store.ErrInvalidTransactionUUID)
		}
	}
}
